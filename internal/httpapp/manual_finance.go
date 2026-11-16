package httpapp

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
)

var moneyInput = regexp.MustCompile(`^(?:[0-9]+(?:[.,][0-9]{1,2})?|[0-9]{1,3}(?:\.[0-9]{3})+,[0-9]{1,2})$`)

func financeToday() string {
	zone, _ := time.LoadLocation("America/Sao_Paulo")
	return time.Now().In(zone).Format("2006-01-02")
}
func parseMoneyBRL(raw string) (int64, error) {
	text := strings.TrimSpace(raw)
	if !moneyInput.MatchString(text) {
		return 0, errors.New("Informe um valor em reais, como 123,45, com até duas casas decimais.")
	}
	if strings.Contains(text, ",") {
		text = strings.ReplaceAll(text, ".", "")
		text = strings.ReplaceAll(text, ",", ".")
	}
	parts := strings.Split(text, ".")
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > 10000000000 {
		return 0, errors.New("O valor ultrapassa o limite deste cadastro.")
	}
	fraction := "00"
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
	}
	cents, _ := strconv.ParseInt(fraction, 10, 64)
	cents += whole * 100
	if cents > 1000000000000 {
		return 0, errors.New("O valor ultrapassa o limite deste cadastro.")
	}
	return cents, nil
}
func financeDate(raw string) error {
	date, err := time.Parse("2006-01-02", raw)
	if err != nil || date.Year() < 1900 || raw > financeToday() {
		return errors.New("Informe uma data válida, até hoje.")
	}
	return nil
}
func financeSavedText(kind string) string {
	switch kind {
	case "expense":
		return "Gasto adicionado aos seus lançamentos."
	case "category":
		return "Categoria salva."
	case "net-worth":
		return "Patrimônio atualizado."
	}
	return ""
}

func (a *App) financeAction(w http.ResponseWriter, r *http.Request) {
	csrf, ok := a.authorized(w, r, false)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if err := r.ParseForm(); err != nil {
		fail(w, 400, "Não foi possível ler o formulário. Revise o tamanho dos campos.")
		return
	}
	if !a.originOK(r) || !equal(csrf, r.FormValue("csrf")) {
		fail(w, 403, "Sua sessão não autorizou a alteração. Recarregue a página.")
		return
	}
	store := finance.New(a.store.DB)
	action := r.FormValue("action")
	var item any
	var err error
	status := http.StatusCreated
	redirect := "/expenses?source=manual&saved=category"
	switch action {
	case "category-create":
		item, err = store.CreateCategory(r.Context(), memory.UserID, finance.CategoryInput{Name: r.FormValue("name"), Color: r.FormValue("color")})
	case "category-update":
		version, parseErr := strconv.Atoi(r.FormValue("version"))
		if parseErr != nil {
			err = errors.New("Recarregue a categoria antes de salvar.")
			break
		}
		item, err = store.UpdateCategory(r.Context(), memory.UserID, r.FormValue("id"), finance.CategoryInput{Name: r.FormValue("name"), Color: r.FormValue("color"), Version: version})
		status = http.StatusOK
	case "expense-create":
		var amount int64
		amount, err = parseMoneyBRL(r.FormValue("amount"))
		if err != nil {
			break
		}
		if err = financeDate(r.FormValue("date")); err != nil {
			break
		}
		item, err = store.CreateExpense(r.Context(), memory.UserID, finance.ExpenseInput{Date: r.FormValue("date"), Description: r.FormValue("description"), CategoryID: r.FormValue("category_id"), Account: r.FormValue("account"), Status: r.FormValue("status"), AmountCents: amount, Recurring: r.FormValue("recurring") == "on", IdempotencyKey: r.FormValue("idempotency_key")})
		if err == nil {
			redirect = "/expenses?source=manual&saved=expense&month=" + url.QueryEscape(r.FormValue("date")[:7])
		}
	case "net-worth":
		var assets, liabilities int64
		assets, err = parseMoneyBRL(r.FormValue("assets"))
		if err != nil {
			break
		}
		liabilities, err = parseMoneyBRL(r.FormValue("liabilities"))
		if err != nil {
			break
		}
		if err = financeDate(r.FormValue("date")); err != nil {
			break
		}
		item, err = store.SaveNetWorth(r.Context(), memory.UserID, finance.NetWorthInput{AssetsCents: assets, LiabilitiesCents: liabilities, Date: r.FormValue("date"), IdempotencyKey: r.FormValue("idempotency_key")})
		redirect = "/dashboard?saved=net-worth"
	default:
		err = errors.New("Ação de cadastro desconhecida.")
	}
	if err != nil {
		message := err.Error()
		code := http.StatusBadRequest
		switch {
		case errors.Is(err, finance.ErrConflict):
			code = 409
			message = "O nome já existe ou o registro mudou em outra aba. Recarregue para conferir antes de salvar."
		case errors.Is(err, finance.ErrNotFound):
			code = 404
			message = "A categoria não foi encontrada. Recarregue a lista."
		case errors.Is(err, finance.ErrValidation):
			message = "Revise os campos: nome, data, categoria, conta e valor precisam ser válidos."
		default:
			if !strings.Contains(message, "Informe") && !strings.Contains(message, "valor") && !strings.Contains(message, "Recarregue") && !strings.Contains(message, "desconhecida") {
				code = 503
				message = "Não foi possível salvar agora. Seus campos foram mantidos; tente novamente."
			}
		}
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			fail(w, code, message)
			return
		}
		if action == "net-worth" {
			a.renderDashboard(w, r, code, message, r.PostForm)
			return
		}
		report, reportErr := a.expenseReport(r.Context(), url.Values{"source": {"manual"}})
		if reportErr != nil {
			fail(w, 503, "Não foi possível recuperar os lançamentos.")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		a.render(w, "expenses.html", map[string]any{"Production": a.config.Production, "ExpensePage": true, "Report": report, "CSRF": csrf, "Today": financeToday(), "ExpenseKey": financeFormValue(r.PostForm, "idempotency_key", memory.ID()), "FormError": message, "FormAction": action, "Form": r.PostForm})
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		respond(w, status, map[string]any{"item": item, "redirect": redirect})
		return
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func financeFormValue(values url.Values, key, fallback string) string {
	if value := values.Get(key); value != "" {
		return value
	}
	return fallback
}
func (a *App) renderDashboard(w http.ResponseWriter, r *http.Request, status int, message string, values url.Values) {
	csrf, ok := a.session(r)
	if !ok {
		loginTarget(w, r, r.URL.RequestURI())
		return
	}
	snap, err := a.store.Snapshot(r.Context(), "")
	if err != nil {
		fail(w, 503, "Não foi possível recuperar a memória.")
		return
	}
	live, err := a.financeSummary(r.Context(), r.URL.Query().Get("month"))
	if err != nil {
		fail(w, 503, "Não foi possível recuperar os indicadores financeiros.")
		return
	}
	if status != 200 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	reserveText := "Não declarada"
	if !a.config.Production {
		for _, fact := range snap.Facts {
			if fact.Predicate == "reserve_months" {
				reserveText = fact.Value + " meses"
				break
			}
		}
	}
	var overview Overview
	if a.config.Production {
		overview = manualOverview(live)
	} else {
		overview = syntheticOverview()
	}
	a.render(w, "dashboard.html", map[string]any{"CodexEnabled": a.config.Codex.Enabled, "Production": a.config.Production, "ReserveText": reserveText, "LiveMonth": r.URL.Query().Get("month"), "CSRF": csrf, "Snapshot": snap, "Agents": Agents(), "Overview": overview, "Live": live, "Today": financeToday(), "WealthKey": financeFormValue(values, "idempotency_key", memory.ID()), "FormError": message, "Form": values, "Saved": financeSavedText(r.URL.Query().Get("saved"))})
}
