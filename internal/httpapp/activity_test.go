package httpapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"openchris/conselho/internal/memory"
)

func TestOfficeActivityRequiresSessionAndValidParameters(t *testing.T) {
	_, server := fixture(t)
	for _, path := range []string{"/api/office/history", "/api/office/replay?source_event_id=1"} {
		if res := call(t, server, "GET", path, "", nil, ""); res.StatusCode != 401 {
			t.Fatalf("unauthenticated %s: %d", path, res.StatusCode)
		}
	}
	cookies, _ := login(t, server)
	for _, path := range []string{
		"/api/office/history?agent=A00", "/api/office/history?agent=A24", "/api/office/history?agent=a02",
		"/api/office/history?before=0", "/api/office/history?before=-1", "/api/office/history?before=1.5", "/api/office/history?before=9223372036854775808",
		"/api/office/history?limit=0", "/api/office/history?limit=41", "/api/office/history?limit=abc", "/api/office/history?limit=",
		"/api/office/replay", "/api/office/replay?source_event_id=0", "/api/office/replay?source_event_id=-1", "/api/office/replay?source_event_id=no",
	} {
		if res := call(t, server, "GET", path, "", cookies, ""); res.StatusCode != 400 {
			t.Fatalf("invalid parameters %s: %d", path, res.StatusCode)
		}
	}
	if res := call(t, server, "GET", "/api/office/replay?source_event_id=999", "", cookies, ""); res.StatusCode != 404 {
		t.Fatalf("missing source: %d", res.StatusCode)
	}
}

func TestOfficeHistoryPaginationFilterAndReadOnlyReplay(t *testing.T) {
	s, server := fixture(t)
	cookies, _ := login(t, server)
	ctx := context.Background()
	var sourceIDs []int64
	for i := 0; i < 3; i++ {
		receipt, err := s.Ingest(ctx, memory.Input{Channel: "dashboard", Key: fmt.Sprint(i), Text: "Minha reserva é de 6 meses"})
		if err != nil {
			t.Fatal(err)
		}
		sourceIDs = append(sourceIDs, receipt.ID)
		if _, err = s.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := s.Ingest(ctx, memory.Input{Channel: "telegram", Key: "pending", Text: "texto privado"})
	if err != nil {
		t.Fatal(err)
	}
	before := officeHTTPCounts(t, s)
	type run struct {
		SourceID int64    `json:"source_event_id"`
		Status   string   `json:"status"`
		AgentIDs []string `json:"agent_ids"`
	}
	var page struct {
		Runs       []run  `json:"runs"`
		NextBefore *int64 `json:"next_before"`
		HasMore    bool   `json:"has_more"`
	}
	res := call(t, server, "GET", "/api/office/history?limit=2", "", cookies, "")
	if err = json.NewDecoder(res.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || len(page.Runs) != 2 || page.Runs[0].SourceID != pending.ID || page.Runs[0].Status != "pending" || len(page.Runs[0].AgentIDs) != 0 || page.Runs[1].SourceID != sourceIDs[2] || !page.HasMore || page.NextBefore == nil || *page.NextBefore != sourceIDs[2] {
		t.Fatalf("first page: %d %+v", res.StatusCode, page)
	}
	res = call(t, server, "GET", fmt.Sprintf("/api/office/history?before=%d&limit=2", *page.NextBefore), "", cookies, "")
	if err = json.NewDecoder(res.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) != 2 || page.Runs[0].SourceID != sourceIDs[1] || page.Runs[1].SourceID != sourceIDs[0] || page.HasMore || page.NextBefore != nil {
		t.Fatalf("second page: %+v", page)
	}
	res = call(t, server, "GET", "/api/office/history?agent=A02", "", cookies, "")
	if err = json.NewDecoder(res.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) != 3 || page.Runs[0].SourceID != sourceIDs[2] || len(page.Runs[0].AgentIDs) != 1 || page.Runs[0].AgentIDs[0] != "A02" {
		t.Fatalf("agent filter: %+v", page)
	}
	res = call(t, server, "GET", "/api/office/history?agent=A01", "", cookies, "")
	if err = json.NewDecoder(res.Body).Decode(&page); err != nil || len(page.Runs) != 0 {
		t.Fatalf("invented Atlas activity: %+v %v", page, err)
	}
	res = call(t, server, "GET", fmt.Sprintf("/api/office/replay?source_event_id=%d", sourceIDs[0]), "", cookies, "")
	var replay struct {
		Run    run `json:"run"`
		Events []struct {
			Sequence int64 `json:"sequence"`
		} `json:"events"`
		Truncated bool  `json:"truncated"`
		Through   int64 `json:"through_sequence"`
	}
	if err = json.NewDecoder(res.Body).Decode(&replay); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || replay.Run.Status != "complete" || len(replay.Events) != 4 || replay.Truncated || replay.Through != replay.Events[3].Sequence {
		t.Fatalf("replay: %d %+v", res.StatusCode, replay)
	}
	for i := 1; i < len(replay.Events); i++ {
		if replay.Events[i].Sequence <= replay.Events[i-1].Sequence {
			t.Fatalf("unordered replay: %+v", replay.Events)
		}
	}
	if after := officeHTTPCounts(t, s); before != after {
		t.Fatalf("GET changed persistent counts: before=%v after=%v", before, after)
	}
}

func TestOfficeActivityNeverReturnsRawConversationOrPayload(t *testing.T) {
	s, server := fixture(t)
	cookies, _ := login(t, server)
	ctx := context.Background()
	receipt, err := s.Ingest(ctx, memory.Input{Channel: "dashboard", Key: "private", Text: "PRIVATE_BODY_917 meu CPF e senha"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `INSERT INTO stream_events(user_id,event_type,payload) VALUES($1,'agent.started',jsonb_build_object('source_event_id',$2::bigint,'agent_id','A02','state','PRIVATE_STATE_917','task_title_safe','PRIVATE_TITLE_917','text','PRIVATE_TEXT_917','outcome','PRIVATE_OUTCOME_917'))`, memory.UserID, receipt.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/office/history", fmt.Sprintf("/api/office/replay?source_event_id=%d", receipt.ID)} {
		res := call(t, server, "GET", path, "", cookies, "")
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 || strings.Contains(string(body), "PRIVATE_") || strings.Contains(string(body), `"payload"`) || strings.Contains(string(body), `"task_title_safe"`) || strings.Contains(string(body), `"text"`) || strings.Contains(string(body), `"body"`) || strings.Contains(string(body), `"outcome"`) {
			t.Fatalf("unsafe projection %s: %d %s", path, res.StatusCode, body)
		}
	}
	const foreign = "00000000-0000-4000-8000-000000000002"
	var foreignID int64
	if err = s.DB.QueryRow(ctx, `INSERT INTO conversation_events(user_id,channel,external_key,direction,body) VALUES($1,'dashboard','foreign','incoming','FOREIGN_PRIVATE_917') RETURNING id`, foreign).Scan(&foreignID); err != nil {
		t.Fatal(err)
	}
	if res := call(t, server, "GET", fmt.Sprintf("/api/office/replay?source_event_id=%d", foreignID), "", cookies, ""); res.StatusCode != 404 {
		t.Fatalf("foreign source exposed: %d", res.StatusCode)
	}
}

func officeHTTPCounts(t *testing.T, s *memory.Store) [5]int {
	t.Helper()
	var out [5]int
	if err := s.DB.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM jobs),(SELECT count(*) FROM conversation_events),(SELECT count(*) FROM memory_facts),(SELECT count(*) FROM stream_events),(SELECT count(*) FROM episodes)`).Scan(&out[0], &out[1], &out[2], &out[3], &out[4]); err != nil {
		t.Fatal(err)
	}
	return out
}
