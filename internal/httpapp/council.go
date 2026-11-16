package httpapp

import (
	"errors"
	"net/http"
	"strconv"

	"openchris/conselho/internal/council"
)

func modelText(state string) string {
	switch state {
	case "available":
		return "IA disponível"
	case "busy":
		return "IA ocupada"
	case "not_authenticated":
		return "Codex aguardando login"
	case "rate_limited":
		return "Limite da IA atingido"
	case "policy_violation":
		return "IA bloqueada para revisão"
	case "auth_storage_failed":
		return "Falha ao preservar o login da IA"
	case "disabled", "not_configured":
		return "IA não configurada"
	default:
		return "IA indisponível"
	}
}
func (a *App) analysis(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		fail(w, 400, "Escolha uma análise válida.")
		return
	}
	result, err := a.councilService.Analysis(r.Context(), id)
	if errors.Is(err, council.ErrNotFound) {
		fail(w, 404, "Esta análise não foi encontrada.")
		return
	}
	if err != nil {
		fail(w, 503, "Não foi possível consultar a análise. Tente novamente.")
		return
	}
	respond(w, 200, result)
}
