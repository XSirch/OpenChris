package httpapp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
)

func (a *App) personalFinanceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /finance/profile", a.financeProfilePage)
	mux.HandleFunc("GET /api/finance/personal", a.personalFinanceAPI)
	mux.HandleFunc("POST /api/finance/profile", a.saveFinanceProfile)
	mux.HandleFunc("POST /api/finance/classifications", a.saveFinanceClassification)
}

func (a *App) financeProfilePage(w http.ResponseWriter, r *http.Request) {
	csrf, ok := a.session(r)
	if !ok {
		loginTarget(w, r, r.URL.RequestURI())
		return
	}
	a.render(w, "finance-profile.html", map[string]any{"CSRF": csrf, "Production": a.config.Production, "FinanceProfilePage": true})
}

func (a *App) personalFinanceAPI(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	data, err := finance.ReadPersonalFinance(r.Context(), a.store.DB, memory.UserID, time.Now().UTC())
	if err != nil {
		fail(w, 503, "Não foi possível consultar suas finanças. Tente novamente.")
		return
	}
	respond(w, 200, data)
}

func decodeFinanceJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		fail(w, 400, "Revise os campos enviados e tente novamente.")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		fail(w, 400, "Envie somente um cadastro por vez.")
		return false
	}
	return true
}

func financeWriteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, finance.ErrValidation):
		fail(w, 400, "Revise os valores, as datas e as opções do cadastro.")
	case errors.Is(err, finance.ErrConflict):
		fail(w, 409, "Este registro mudou. Recarregue a página antes de salvar novamente.")
	case errors.Is(err, finance.ErrNotFound):
		fail(w, 404, "O lançamento não está disponível. Atualize a lista de gastos.")
	default:
		fail(w, 503, "Não foi possível salvar. Tente novamente.")
	}
}

func (a *App) saveFinanceProfile(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, true); !ok {
		return
	}
	var input finance.Profile
	if !decodeFinanceJSON(w, r, &input) {
		return
	}
	saved, err := finance.New(a.store.DB).SaveProfile(r.Context(), memory.UserID, input)
	if err != nil {
		financeWriteError(w, err)
		return
	}
	respond(w, 200, saved)
}

func (a *App) saveFinanceClassification(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, true); !ok {
		return
	}
	var input finance.ClassificationInput
	if !decodeFinanceJSON(w, r, &input) {
		return
	}
	if err := finance.New(a.store.DB).SaveClassification(r.Context(), memory.UserID, input); err != nil {
		financeWriteError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"saved": true})
}
