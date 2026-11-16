package httpapp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
)

func (a *App) autonomyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/autonomy", a.autonomyStatus)
	mux.HandleFunc("GET /api/autonomy/strategies", a.autonomyStrategies)
	mux.HandleFunc("GET /api/autonomy/runs/{id}", a.autonomyRun)
	mux.HandleFunc("POST /api/autonomy/pause", a.autonomyPause)
	mux.HandleFunc("POST /api/autonomy/run", a.autonomyRequest)
}
func (a *App) autonomyStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	status, err := a.autonomyService.Status(r.Context())
	if err != nil {
		fail(w, 503, "Não foi possível consultar o supervisor.")
		return
	}
	respond(w, 200, status)
}
func (a *App) autonomyStrategies(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	result, err := a.autonomyService.Strategies(r.Context())
	if err != nil {
		fail(w, 503, "Não foi possível consultar as estratégias.")
		return
	}
	respond(w, 200, result)
}
func (a *App) autonomyRun(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	result, err := a.autonomyService.RunDetail(r.Context(), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		fail(w, 503, "Não foi possível consultar a rodada.")
		return
	}
	respond(w, 200, result)
}
func strictAutonomyInput(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 256)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		fail(w, 400, "Envie uma ação válida.")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		fail(w, 400, "Envie apenas uma ação.")
		return false
	}
	return true
}
func (a *App) autonomyPause(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, true); !ok {
		return
	}
	if !a.config.AutonomyEnabled {
		fail(w, 409, "O acompanhamento automático está desativado no servidor.")
		return
	}
	var input struct {
		Paused *bool `json:"paused"`
	}
	if !strictAutonomyInput(w, r, &input) {
		return
	}
	if input.Paused == nil {
		fail(w, 400, "Informe se deseja pausar ou retomar.")
		return
	}
	if err := a.autonomyService.SetPaused(r.Context(), *input.Paused); err != nil {
		fail(w, 503, "Não foi possível alterar o acompanhamento.")
		return
	}
	respond(w, 200, map[string]bool{"paused": *input.Paused})
}
func (a *App) autonomyRequest(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, true); !ok {
		return
	}
	var input struct{}
	if !strictAutonomyInput(w, r, &input) {
		return
	}
	status, err := a.autonomyService.Status(r.Context())
	if err != nil {
		fail(w, 503, "Não foi possível consultar o supervisor.")
		return
	}
	if !status.Enabled || status.Paused {
		fail(w, 409, "Retome o acompanhamento antes de solicitar uma rodada.")
		return
	}
	run, err := a.autonomyService.RequestRun(r.Context())
	if err != nil {
		fail(w, 503, "A solicitação não pôde ser registrada.")
		return
	}
	respond(w, 202, run)
}
