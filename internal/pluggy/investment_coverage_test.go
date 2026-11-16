package pluggy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"openchris/conselho/internal/memory"
)

func TestPersonalProxyReadsInvestmentsWhenCollectedProductsOmitThem(t *testing.T) {
	for _, tc := range []struct {
		name, connector string
		supported       []string
		want            int
	}{
		{"personal-proxy", "MeuPluggy", []string{"ACCOUNTS", "INVESTMENTS"}, 1},
		{"other-connector", "Other Bank", []string{"ACCOUNTS", "INVESTMENTS"}, 0},
		{"unsupported-product", "MeuPluggy", []string{"ACCOUNTS"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			var investmentReads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body any
				switch r.URL.Path {
				case "/auth":
					body = Object{"apiKey": "key"}
				case "/items/" + testItem:
					body = Object{"id": testItem, "clientUserId": memory.UserID, "status": "UPDATED", "executionStatus": "SUCCESS", "consentExpiresAt": "2099-01-01T00:00:00Z", "products": []string{"ACCOUNTS", "TRANSACTIONS"}, "connector": Object{"id": 200, "name": tc.connector, "products": tc.supported}}
				case "/accounts":
					body = Object{"totalPages": 1, "results": []Object{{"id": testAccount, "itemId": testItem, "balance": "10.00", "currencyCode": "BRL"}}}
				case "/v2/transactions":
					body = Object{"results": []Object{}, "next": nil}
				case "/investments":
					investmentReads.Add(1)
					body = Object{"totalPages": 1, "results": []Object{{"id": "available-investment", "itemId": testItem, "balance": "20.00", "currencyCode": "BRL"}}}
				default:
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			s, err := New(db, Config{Enabled: true, ClientID: "id", ClientSecret: "secret", WebhookSecret: testWebhookSecret, PublicURL: "https://conselho.example/webhooks/pluggy", APIBaseURL: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err = s.RegisterItem(ctx, testItem); err != nil {
				t.Fatal(err)
			}
			if err = s.SyncItem(ctx, testItem); err != nil {
				t.Fatal(err)
			}
			data, err := s.Data(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(data["investments"].([]Object)) != tc.want || int(investmentReads.Load()) != tc.want {
				t.Fatalf("available investments omitted or unauthorized product read: records=%d requests=%d", len(data["investments"].([]Object)), investmentReads.Load())
			}
		})
	}
}
