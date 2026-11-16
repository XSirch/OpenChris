package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/council"
)

type analysisProvider struct{ calls int }

func (p *analysisProvider) Status(context.Context) (codex.Status, error) {
	return codex.Status{State: "available", Model: codex.DefaultModel}, nil
}
func (p *analysisProvider) Run(_ context.Context, in codex.Request) (codex.Result, error) {
	p.calls++
	var data struct {
		Sources []struct {
			ID string `json:"id"`
		} `json:"sources"`
	}
	json.Unmarshal(in.Context, &data)
	return codex.Result{Reply: "PRIVATE_MODEL_TEXT stays on the dashboard.", Episode: "Conversa analisada.", Evidence: []string{data.Sources[0].ID}, Assumptions: []string{}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
}
func TestAnalysisWaitsForPersistedResultWithoutBackfillingOrSendingFreeText(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	sent := []string{}
	service := localService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&message)
		sent = append(sent, message.Text)
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":91}}`)
	})
	if _, err := service.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(1, "Mensagem anterior à IA")); err != nil {
		t.Fatal(err)
	}
	provider := &analysisProvider{}
	analysis := council.New(db, provider, council.Config{Enabled: true, PublicURL: "https://conselho.example.test", DailyLimit: 30})
	service.SetAnalysis(analysis)
	if _, err := service.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(2, "Cora, como organizar meus gastos?")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || provider.calls != 0 {
		t.Fatal("historical message was analyzed or inference ran in Telegram transaction")
	}
	if _, err := service.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || provider.calls != 0 {
		t.Fatal("sent an uncompleted analysis")
	}
	if ok, err := analysis.ProcessOne(ctx); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := service.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || strings.Contains(sent[1], "PRIVATE_MODEL_TEXT") || !strings.Contains(sent[1], "/dashboard#conversation") {
		t.Fatal("unsafe notification", sent)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM council_jobs`).Scan(&count); err != nil || count != 1 {
		t.Fatal("backfilled history", count, err)
	}
	var body string
	db.QueryRow(ctx, `SELECT body FROM conversation_events WHERE direction='outgoing' AND body LIKE 'PRIVATE_MODEL_TEXT%'`).Scan(&body)
	if body == "" {
		t.Fatal("analysis was replaced by Telegram notification")
	}
}

func TestAnalysisNotificationRechecksRevokedIdentity(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	sends := 0
	service := localService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		sends++
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":91}}`)
	})
	analysis := council.New(db, &analysisProvider{}, council.Config{Enabled: true, PublicURL: "https://conselho.example.test", DailyLimit: 30})
	service.SetAnalysis(analysis)
	if _, err := service.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(3, "Olá, Atlas")); err != nil {
		t.Fatal(err)
	}
	if _, err := analysis.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	service.cfg.AllowedUserID = 789
	service.cfg.AllowedChatID = 789
	if _, err := service.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if sends != 0 {
		t.Fatal("sent analysis notice after identity change")
	}
}
