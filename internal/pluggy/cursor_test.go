package pluggy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestTransactionsUseCursorAPIWhenLegacyEndpointIsGone(t *testing.T) {
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			fmt.Fprint(w, `{"apiKey":"key"}`)
		case "/transactions":
			http.Error(w, "legacy endpoint removed", http.StatusGone)
		case "/v2/transactions":
			if r.Header.Get("X-API-KEY") != "key" || r.URL.Query().Get("accountId") != testAccount || r.URL.Query().Has("page") || r.URL.Query().Has("pageSize") {
				t.Error("invalid cursor transaction request")
			}
			cursor := r.URL.Query().Get("after")
			cursors = append(cursors, cursor)
			if cursor == "" {
				fmt.Fprint(w, `{"results":[{"id":"one","amount":9007199254740993.123456789,"currencyCode":"USD","paymentData":null}],"next":"?accountId=`+testAccount+`&after=opaque%2Bcursor%2F%3D"}`)
			} else {
				fmt.Fprint(w, `{"results":[{"id":"two","amount":null,"currencyCode":null}],"next":null}`)
			}
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := NewClient("id", "secret", WithBaseURL(server.URL))
	rows, err := c.Transactions(context.Background(), testAccount)
	if err != nil || len(rows) != 2 {
		t.Fatalf("cursor collection failed: rows=%d err=%v", len(rows), err)
	}
	if fmt.Sprint(cursors) != "[ opaque+cursor/=]" || rows[0]["amount"] != json.Number("9007199254740993.123456789") || rows[0]["currencyCode"] != "USD" || rows[0]["paymentData"] != nil || rows[1]["amount"] != nil {
		t.Fatal("cursor, precision, currency or null changed")
	}
}

func TestTransactionCursorRejectsScopeChangesMalformedPagesAndCycles(t *testing.T) {
	var outside atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { outside.Add(1) }))
	defer external.Close()
	for _, tc := range []struct{ name, body string }{
		{"external", `{"results":[],"next":"` + external.URL + `/v2/transactions?accountId=` + testAccount + `&after=x"}`},
		{"different-path", `{"results":[],"next":"/investments?accountId=` + testAccount + `&after=x"}`},
		{"different-account", `{"results":[],"next":"?accountId=` + testItem + `&after=x"}`},
		{"duplicate-account", `{"results":[],"next":"?accountId=` + testAccount + `&accountId=` + testAccount + `&after=x"}`},
		{"extra-filter", `{"results":[],"next":"?accountId=` + testAccount + `&after=x&ids=other"}`},
		{"missing-next", `{"results":[]}`},
		{"invalid-next", `{"results":[],"next":7}`},
		{"empty-next", `{"results":[],"next":""}`},
		{"invalid-record", `{"results":[7],"next":null}`},
		{"cycle", `{"results":[],"next":"?accountId=` + testAccount + `&after=x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/auth" {
					fmt.Fprint(w, `{"apiKey":"key"}`)
					return
				}
				if r.URL.Path != "/v2/transactions" {
					http.Error(w, "legacy endpoint removed", http.StatusGone)
					return
				}
				reads.Add(1)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			c := NewClient("id", "secret", WithBaseURL(server.URL))
			if _, err := c.Transactions(context.Background(), testAccount); err == nil || reads.Load() == 0 || reads.Load() > 2 {
				t.Fatalf("unsafe cursor accepted or not tested: reads=%d err=%v", reads.Load(), err)
			}
		})
	}
	if outside.Load() != 0 {
		t.Fatal("pagination exposed the API key to another origin")
	}
}

func TestTransactionCursorEnforcesTraversalAndRecordLimits(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(fmt.Sprint(oversized), func(t *testing.T) {
			var pages atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/auth" {
					fmt.Fprint(w, `{"apiKey":"key"}`)
					return
				}
				if r.URL.Path != "/v2/transactions" {
					http.Error(w, "legacy endpoint removed", http.StatusGone)
					return
				}
				page := pages.Add(1)
				rows := []Object{}
				if oversized {
					for range 501 {
						rows = append(rows, Object{"id": "record"})
					}
				}
				_ = json.NewEncoder(w).Encode(Object{"results": rows, "next": fmt.Sprintf("?accountId=%s&after=%d", testAccount, page)})
			}))
			defer server.Close()
			c := NewClient("id", "secret", WithBaseURL(server.URL))
			_, err := c.Transactions(context.Background(), testAccount)
			want := int32(20)
			if oversized {
				want = 1
			}
			if err == nil || pages.Load() != want {
				t.Fatalf("unbounded traversal: pages=%d err=%v", pages.Load(), err)
			}
		})
	}
}
