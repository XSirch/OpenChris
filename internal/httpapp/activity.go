package httpapp

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"openchris/conselho/internal/memory"
)

func officePositiveParameter(query url.Values, name string, required bool) (int64, bool) {
	values, exists := query[name]
	if !exists {
		return 0, !required
	}
	if len(values) != 1 || values[0] == "" {
		return 0, false
	}
	for _, char := range values[0] {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseInt(values[0], 10, 64)
	return value, err == nil && value > 0
}

func (a *App) officeHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	query := r.URL.Query()
	agent := query.Get("agent")
	if len(query["agent"]) > 1 || (agent != "" && !memory.ValidOfficeAgent(agent)) {
		fail(w, 400, "Escolha um personagem válido.")
		return
	}
	before, valid := officePositiveParameter(query, "before", false)
	if !valid {
		fail(w, 400, "O cursor do histórico é inválido.")
		return
	}
	limit, valid := officePositiveParameter(query, "limit", false)
	if !valid || limit > 40 {
		fail(w, 400, "Escolha um limite entre 1 e 40 atividades.")
		return
	}
	page, err := a.store.OfficeHistory(r.Context(), agent, before, int(limit))
	if err != nil {
		fail(w, 503, "Não foi possível recuperar as atividades. Tente novamente quando a conexão voltar.")
		return
	}
	respond(w, 200, page)
}

func (a *App) officeReplay(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	source, valid := officePositiveParameter(r.URL.Query(), "source_event_id", true)
	if !valid {
		fail(w, 400, "Escolha uma atividade válida para reproduzir.")
		return
	}
	replay, err := a.store.OfficeReplay(r.Context(), source)
	if errors.Is(err, memory.ErrActivityNotFound) {
		fail(w, 404, "Esta atividade não foi encontrada.")
		return
	}
	if err != nil {
		fail(w, 503, "Não foi possível recuperar as etapas. Tente novamente quando a conexão voltar.")
		return
	}
	respond(w, 200, replay)
}
