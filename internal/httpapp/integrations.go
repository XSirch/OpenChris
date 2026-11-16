package httpapp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"openchris/conselho/internal/pluggy"
	"openchris/conselho/internal/telegram"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

func (a *App) RunIntegrations(ctx context.Context, onError func(error)) {
	var workers sync.WaitGroup
	if a.config.AutonomyEnabled {
		workers.Add(1)
		go func() { defer workers.Done(); a.autonomyService.Run(ctx, onError) }()
		if a.config.Telegram.Enabled {
			workers.Add(1)
			go func() { defer workers.Done(); a.runAutonomyNotices(ctx, onError) }()
		}
	}
	if a.config.Codex.Enabled {
		workers.Add(1)
		go func() { defer workers.Done(); a.councilService.Run(ctx, onError) }()
	}
	if a.config.Telegram.Enabled {
		workers.Add(1)
		go func() { defer workers.Done(); a.telegramService.Run(ctx, onError) }()
	}
	if a.config.Pluggy.Enabled {
		workers.Add(1)
		go func() { defer workers.Done(); a.pluggyService.Run(ctx, onError) }()
	}
	workers.Wait()
}

func (a *App) runAutonomyNotices(ctx context.Context, onError func(error)) {
	queue := func() {
		notices, err := a.autonomyService.PendingNotices(ctx)
		if err == nil {
			for _, notice := range notices {
				if err = a.telegramService.QueueAutonomyNotice(ctx, notice.ID); err != nil {
					break
				}
			}
		}
		if err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
	}
	queue()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			queue()
		}
	}
}
func (a *App) integrationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations", a.integrationStatus)
	mux.HandleFunc("GET /api/integrations/pluggy/data", a.integrationData)
	for _, route := range []string{
		"POST /api/integrations/telegram/setup", "POST /api/integrations/pluggy/setup",
		"POST /api/integrations/pluggy/connect-token", "POST /api/integrations/pluggy/items",
		"POST /api/integrations/pluggy/sync",
	} {
		mux.HandleFunc(route, a.integrationAction)
	}
	if a.config.Production && a.config.Telegram.Enabled {
		mux.HandleFunc("POST /webhooks/telegram", a.realTelegramWebhook)
	}
	if a.config.Production && a.config.Pluggy.Enabled {
		mux.HandleFunc("POST /webhooks/pluggy", a.pluggyWebhook)
	}
}
func (a *App) integrationStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	telegramStatus, err := a.telegramService.Status(r.Context())
	if err != nil {
		fail(w, 503, "Não foi possível consultar o canal Telegram.")
		return
	}
	pluggyStatus, err := a.pluggyService.Status(r.Context())
	if err != nil {
		fail(w, 503, "Não foi possível consultar as instituições.")
		return
	}
	telegramStatus["webhook_url"] = a.config.PublicURL + "/webhooks/telegram"
	telegramStatus["allowed_identity_configured"] = a.config.Telegram.AllowedUserID > 0 && a.config.Telegram.AllowedChatID > 0
	pluggyStatus["webhook_url"] = a.config.PublicURL + "/webhooks/pluggy"
	telegramStatus["missing_credentials"] = a.telegramMissing()
	pluggyStatus["missing_credentials"] = a.pluggyMissing()
	model := a.councilService.Status(r.Context())
	state := model.State
	if !a.config.Codex.Enabled {
		state = "not_configured"
	}
	respond(w, 200, map[string]any{"telegram": telegramStatus, "pluggy": pluggyStatus, "models": map[string]any{"configured": a.config.Codex.Enabled, "status": state, "model": model.Model}})
}
func (a *App) telegramMissing() []string {
	missing := []string{}
	for _, field := range []struct{ name, value string }{{"TELEGRAM_BOT_TOKEN", a.config.Telegram.BotToken}, {"TELEGRAM_WEBHOOK_SECRET", a.config.Telegram.WebhookSecret}} {
		if field.value == "" {
			missing = append(missing, field.name)
		}
	}
	if a.config.Telegram.AllowedUserID <= 0 {
		missing = append(missing, "TELEGRAM_ALLOWED_USER_ID")
	}
	if a.config.Telegram.AllowedChatID <= 0 {
		missing = append(missing, "TELEGRAM_ALLOWED_CHAT_ID")
	}
	return missing
}
func (a *App) pluggyMissing() []string {
	missing := []string{}
	for _, field := range []struct{ name, value string }{{"PLUGGY_CLIENT_ID", a.config.Pluggy.ClientID}, {"PLUGGY_CLIENT_SECRET", a.config.Pluggy.ClientSecret}, {"PLUGGY_WEBHOOK_SECRET", a.config.Pluggy.WebhookSecret}} {
		if field.value == "" {
			missing = append(missing, field.name)
		}
	}
	return missing
}
func (a *App) integrationData(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	if !a.config.Pluggy.Enabled {
		integrationDisabled(w)
		return
	}
	data, err := a.pluggyService.Data(r.Context())
	if err != nil {
		integrationFailure(w, err)
		return
	}
	respond(w, 200, data)
}
func integrationDisabled(w http.ResponseWriter) {
	respond(w, 503, map[string]string{"code": "integration_not_configured", "error": "A integração está desativada. Complete as credenciais e a configuração no servidor."})
}
func integrationFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, telegram.ErrDisabled), errors.Is(err, pluggy.ErrDisabled):
		integrationDisabled(w)
	case errors.Is(err, telegram.ErrUnauthorized), errors.Is(err, pluggy.ErrUnauthorized):
		fail(w, 403, "A integração não autorizou esta solicitação.")
	case errors.Is(err, telegram.ErrInvalid), errors.Is(err, pluggy.ErrInvalid):
		fail(w, 400, "A integração não aceitou os dados informados.")
	case errors.Is(err, telegram.ErrConflict), errors.Is(err, pluggy.ErrCapacity):
		fail(w, 409, "A solicitação conflita com o estado atual ou atingiu o limite de conexões.")
	default:
		fail(w, 503, "A integração não respondeu. Confira a configuração e tente novamente.")
	}
}
func (a *App) integrationAction(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, true); !ok {
		return
	}
	isTelegram := strings.HasPrefix(r.URL.Path, "/api/integrations/telegram/")
	if isTelegram && !a.config.Telegram.Enabled || !isTelegram && !a.config.Pluggy.Enabled {
		integrationDisabled(w)
		return
	}
	var err error
	switch r.URL.Path {
	case "/api/integrations/telegram/setup":
		err = a.telegramService.Setup(r.Context())
	case "/api/integrations/pluggy/setup":
		err = a.pluggyService.SetupWebhooks(r.Context())
	case "/api/integrations/pluggy/connect-token":
		var info map[string]any
		info, err = a.pluggyService.ConnectInfo(r.Context())
		if err != nil {
			break
		}
		err = a.pluggyService.SetupWebhooks(r.Context())
		if err != nil {
			break
		}
		var token string
		token, err = a.pluggyService.ConnectToken(r.Context())
		if err != nil {
			break
		}
		respond(w, 200, map[string]any{"accessToken": token, "connectorId": info["connectorId"]})
		return
	case "/api/integrations/pluggy/items", "/api/integrations/pluggy/sync":
		var body map[string]string
		if !decode(w, r, &body, 4096) {
			return
		}
		itemID := body["item_id"]
		if itemID == "" {
			fail(w, 400, "Informe a referência da conexão autorizada.")
			return
		}
		if r.URL.Path == "/api/integrations/pluggy/items" {
			err = a.pluggyService.RegisterItem(r.Context(), itemID)
		}
		if err == nil {
			err = a.pluggyService.QueueSync(r.Context(), itemID)
		}
		if err == nil {
			respond(w, 202, map[string]any{"ok": true, "status": "queued"})
			return
		}
	}
	if err != nil {
		integrationFailure(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func webhookBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, 413, "A notificação excedeu o limite.")
		return nil, false
	}
	if !utf8.Valid(data) {
		fail(w, 400, "A notificação deve usar UTF-8 válido.")
		return nil, false
	}
	return data, true
}
func (a *App) realTelegramWebhook(w http.ResponseWriter, r *http.Request) {
	data, ok := webhookBody(w, r)
	if !ok {
		return
	}
	duplicate, err := a.telegramService.HandleUpdate(r.Context(), r.Header.Get("X-Telegram-Bot-Api-Secret-Token"), data)
	if err != nil {
		integrationFailure(w, err)
		return
	}
	respond(w, 200, map[string]any{"ok": true, "duplicate": duplicate})
}
func (a *App) pluggyWebhook(w http.ResponseWriter, r *http.Request) {
	data, ok := webhookBody(w, r)
	if !ok {
		return
	}
	duplicate, err := a.pluggyService.HandleWebhook(r.Context(), r.Header.Get("X-Conselho-Webhook-Secret"), data)
	if err != nil {
		integrationFailure(w, err)
		return
	}
	respond(w, 200, map[string]any{"ok": true, "duplicate": duplicate})
}
func (a *App) telegramReply(ctx context.Context, command string) (string, error) {
	link := a.config.PublicURL + "/dashboard"
	normalized := strings.ToLower(strings.TrimSpace(command))
	if at := strings.IndexByte(normalized, '@'); at >= 0 {
		normalized = normalized[:at]
	}
	switch normalized {
	case "/start", "/ajuda", "/help":
		return "CONSELHO privado. Comandos: /resumo, /gastos, /patrimonio e /memoria. Somente resumos são enviados aqui. A IA ainda não está configurada.\n" + link, nil
	case "/memoria":
		return "Suas mensagens ficam registradas no histórico compartilhado. Consulte e revise a memória no dashboard autenticado.\n" + link, nil
	case "/resumo", "/gastos", "/patrimonio":
		summary, err := a.financeSummary(ctx, "")
		if err != nil {
			return "", err
		}
		if normalized == "/patrimonio" {
			return "Patrimônio líquido manual: " + summary.NetWorthText + ". Referência: " + dateOrMissing(summary.NetWorthDate) + ".\n" + link, nil
		}
		if normalized == "/gastos" {
			return "Gastos manuais do mês: " + summary.ExpenseText + ". Pendências: " + summary.PendingText + ".\n" + link, nil
		}
		return "Resumo dos cadastros manuais\nPatrimônio líquido: " + summary.NetWorthText + "\nGastos do mês: " + summary.ExpenseText + "\nPendências: " + summary.PendingText + "\nFontes bancárias detalhadas ficam no dashboard; não somamos fontes sem conciliação.\n" + link, nil
	default:
		return "Mensagem registrada na memória compartilhada. A conversa por IA ainda não está configurada. Consulte o histórico no dashboard autenticado.\n" + link, nil
	}
}
func dateOrMissing(value string) string {
	if value == "" {
		return "Não informado"
	}
	return value
}
