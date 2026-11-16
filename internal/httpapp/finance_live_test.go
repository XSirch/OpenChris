package httpapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
)

func readFinanceSnapshot(t *testing.T, reader *bufio.Reader) FinanceSummary {
	t.Helper()
	var event, payload string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream closed before a complete snapshot: %v", err)
		}
		if !utf8.ValidString(line) {
			t.Fatal("stream frame is not valid UTF-8")
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			if event == "finance.snapshot" {
				var summary FinanceSummary
				if err := json.Unmarshal([]byte(payload), &summary); err != nil {
					t.Fatal(err)
				}
				return summary
			}
			event, payload = "", ""
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			payload = strings.TrimPrefix(line, "data: ")
		}
	}
}

func TestFinanceOpenStreamFlushesInitialAndChangedSnapshotsOverHTTP(t *testing.T) {
	s, http1, cookies, _ := productionFixture(t)
	http2 := httptest.NewUnstartedServer(New(s, Config{Production: true, Password: "production-test-password-1234", PublicURL: "https://chris.example.test"}))
	http2.EnableHTTP2 = true
	http2.StartTLS()
	t.Cleanup(http2.Close)
	index := 0
	for _, protocol := range []struct {
		name   string
		server *httptest.Server
		major  int
	}{{"HTTP1", http1, 1}, {"HTTP2", http2, 2}} {
		for _, encoding := range []string{"identity", "gzip, deflate, br, zstd"} {
			t.Run(protocol.name+"/"+encoding, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				month := financeToday()[:7]
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, protocol.server.URL+"/api/finance/events?month="+month, nil)
				req.Header.Set("Accept", "text/event-stream")
				req.Header.Set("Accept-Encoding", encoding)
				req.AddCookie(cookies[0])
				start := time.Now()
				res, err := protocol.server.Client().Do(req)
				if err != nil {
					t.Fatalf("stream headers were not flushed: %v", err)
				}
				defer res.Body.Close()
				mediaType, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
				if res.StatusCode != 200 || res.ProtoMajor != protocol.major || err != nil || mediaType != "text/event-stream" || res.Header.Get("Content-Encoding") != "" || res.Uncompressed || res.Header.Get("X-Accel-Buffering") != "no" || !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
					t.Fatalf("unexpected stream contract: status=%d proto=%s headers=%v uncompressed=%t", res.StatusCode, res.Proto, res.Header, res.Uncompressed)
				}
				reader := bufio.NewReader(res.Body)
				before := readFinanceSnapshot(t, reader)
				if time.Since(start) > 2*time.Second || before.ExpenseCount != index {
					t.Fatalf("initial snapshot delayed or stale: elapsed=%v count=%d want=%d", time.Since(start), before.ExpenseCount, index)
				}
				_, err = finance.New(s.DB).CreateExpense(ctx, memory.UserID, finance.ExpenseInput{Date: financeToday(), Description: "Registro de atualização ao vivo", CategoryID: "10000000-0000-4000-8000-000000000002", Account: "Conta privada de teste", Status: "confirmed", AmountCents: 100, IdempotencyKey: fmt.Sprintf("live-stream-contract-%d", index)})
				if err != nil {
					t.Fatal(err)
				}
				after := readFinanceSnapshot(t, reader)
				if after.ExpenseCount != before.ExpenseCount+1 || after.ExpenseCents != before.ExpenseCents+100 || after.SnapshotAt == before.SnapshotAt || ctx.Err() != nil {
					t.Fatalf("changed snapshot was not delivered on the open stream: before=%+v after=%+v ctx=%v", before, after, ctx.Err())
				}
				cancel()
				if _, err := reader.ReadByte(); err == nil {
					t.Fatal("stream remained readable after cancellation")
				}
				index++
			})
		}
	}
}
