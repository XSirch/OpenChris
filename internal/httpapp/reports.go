package httpapp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/autonomy"
	"openchris/conselho/internal/memory"
)

var reportID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func safeReturnTarget(raw string) string {
	if strings.ContainsAny(raw, "\\%") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "/dashboard"
	}
	u, e := url.Parse(raw)
	if e != nil || u.IsAbs() || u.Host != "" || u.User != nil {
		return "/dashboard"
	}
	if u.Path != "/reports" && u.Path != "/dashboard" && u.Path != "/finance/profile" {
		return "/dashboard"
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return "/dashboard"
	}
	for k, v := range q {
		if len(v) != 1 {
			return "/dashboard"
		}
		valid := (u.Path == "/reports" || u.Path == "/dashboard") && (k == "run" || k == "replay" && u.Path == "/dashboard") && reportID.MatchString(v[0])
		if k == "cursor" && u.Path == "/reports" {
			valid = regexp.MustCompile(`^[A-Za-z0-9_-]{1,512}$`).MatchString(v[0])
		}
		if !valid {
			return "/dashboard"
		}
	}

	if u.Fragment != "" {
		valid := u.Path == "/reports" && regexp.MustCompile(`^(task-[0-4]|report-reading|report-strategies|report-sources)$`).MatchString(u.Fragment)
		if u.Path == "/finance/profile" {
			valid = regexp.MustCompile(`^(goals|risk[-_]profile|experience|liquidity[-_]days|restrictions|reserve[-_]current[-_]cents|reserve[-_]target[-_]cents|commitments[-_]30[-_]days[-_]cents|minimum[-_]checking[-_]cents|protected[-_]checking[-_]cents|confirmed)$`).MatchString(u.Fragment)
		}
		if u.Path == "/dashboard" {
			valid = u.Fragment == "connections-panel"
		}
		if !valid {
			return "/dashboard"
		}
	}
	return u.String()
}
func loginTarget(w http.ResponseWriter, r *http.Request, target string) {
	http.Redirect(w, r, "/login?return_to="+url.QueryEscape(safeReturnTarget(target)), http.StatusSeeOther)
}

type reportTask struct {
	Stage                                         int
	Name, Role, Status, Text, Episode, Error, Raw string
	Assumptions, Unknowns, Evidence               []string
	StartedAt, CompletedAt                        *time.Time
}
type reportSource struct {
	Title, URL, Summary string
	PublishedAt         *time.Time
	ObservedAt          time.Time
}

type reportStrategy struct {
	Title, Hypothesis, Mandate, Status string
	TimeHorizon                        string `json:"time_horizon"`
	Conditions                         []string
	InvalidationConditions             []string `json:"invalidation_conditions"`
	EligibleInstruments                []string `json:"eligible_instruments"`
	Risks                              []string
	Version                            int
	Raw                                string
}

func reportPageData(page autonomy.ArchivePage, detail *autonomy.RunDetail, cursor string) map[string]any {
	data := map[string]any{"ReportsPage": true, "FinanceProfilePage": false, "Archive": page, "Cursor": cursor, "Detail": detail, "Tasks": []reportTask{}, "SummaryText": "", "SummaryStatus": "", "LatestCompleteURL": "", "RunningURL": ""}
	for _, run := range page.Runs {
		if (run.Status == "complete" || run.Status == "await_data") && data["LatestCompleteURL"] == "" {
			data["LatestCompleteURL"] = "/reports?run=" + run.ID + "#report-reading"
		}
		if (run.Status == "running" || run.Status == "pending") && data["RunningURL"] == "" {
			data["RunningURL"] = "/reports?run=" + run.ID + "#report-reading"
		}
	}
	if detail == nil {
		return data
	}
	tasks := []reportTask{}
	for stage := 0; stage < 5; stage++ {
		t := reportTask{Role: []string{"Pesquisa pública", "Estratégia", "Risco", "Crítica independente", "Síntese"}[stage], Stage: stage, Name: "Parecer " + strconv.Itoa(stage+1), Status: "Sem registro"}
		for _, stored := range detail.Tasks {
			if stored.Stage != stage {
				continue
			}
			t.Status = stored.Status
			t.Error = stored.ErrorCode
			t.StartedAt = stored.StartedAt
			t.CompletedAt = stored.CompletedAt
			for i, name := range names {
				if stored.AgentID == "A"+fmtAgent(i+1) {
					t.Name = name
				}
			}
			var result struct {
				Reply, Summary, Episode         string
				Assumptions, Unknowns, Evidence []string
			}
			if len(stored.Result) > 0 && string(stored.Result) != "null" {
				if json.Unmarshal(stored.Result, &result) == nil {
					t.Text = result.Reply
					if t.Text == "" {
						t.Text = result.Summary
					}
					t.Episode = result.Episode
					t.Assumptions = result.Assumptions
					t.Unknowns = result.Unknowns
					t.Evidence = result.Evidence
				}
				t.Raw = prettyJSON(stored.Result)
			}
		}
		tasks = append(tasks, t)
	}
	sources := []reportSource{}
	for _, e := range detail.Evidence {
		u, err := url.Parse(e.URL)
		link := ""
		if err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil {
			link = u.String()
		}
		sources = append(sources, reportSource{e.Title, link, e.Summary, e.PublishedAt, e.ObservedAt})
	}
	strategies := []reportStrategy{}
	for _, raw := range detail.Strategies {
		var strategy reportStrategy
		_ = json.Unmarshal(raw, &strategy)
		strategy.Raw = prettyJSON(raw)
		strategies = append(strategies, strategy)
	}
	data["Tasks"] = tasks
	for _, task := range tasks {
		if task.Stage == 4 && task.Status == "complete" && task.Text != "" {
			data["SummaryText"] = task.Text
			data["SummaryStatus"] = reportState(detail.Run.Status)
		}
	}
	data["Sources"] = sources
	data["Strategies"] = strategies
	return data
}
func fmtAgent(i int) string {
	if i < 10 {
		return "0" + strconv.Itoa(i)
	}
	return strconv.Itoa(i)
}
func prettyJSON(raw []byte) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return "Registro indisponível: formato inválido."
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
func (a *App) reports(w http.ResponseWriter, r *http.Request) {
	csrf, ok := a.session(r)
	if !ok {
		loginTarget(w, r, r.URL.RequestURI())
		return
	}
	page, err := a.autonomyService.Archive(r.Context(), r.URL.Query().Get("cursor"))
	if errors.Is(err, autonomy.ErrArchiveCursor) {
		fail(w, 400, "A página do arquivo é inválida. Abra /reports para recomeçar.")
		return
	}
	if err != nil {
		fail(w, 503, "Não foi possível abrir o arquivo. Tente novamente.")
		return
	}
	id := r.URL.Query().Get("run")
	var latestComplete string
	err = a.store.DB.QueryRow(r.Context(), `SELECT id::text FROM autonomy_runs WHERE user_id=$1 AND status IN ('complete','await_data') ORDER BY created_at DESC,id DESC LIMIT 1`, memory.UserID).Scan(&latestComplete)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		fail(w, 503, "Não foi possível consultar a última análise. Tente novamente.")
		return
	}
	if id == "" && len(page.Runs) > 0 {
		id = page.Runs[0].ID
		for _, run := range page.Runs {
			if run.Status == "complete" || run.Status == "await_data" {
				id = run.ID
				break
			}
		}
		if r.URL.Query().Get("cursor") == "" && latestComplete != "" {
			id = latestComplete
		}
	}
	var detail *autonomy.RunDetail
	if id != "" {
		if !reportID.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		d, e := a.autonomyService.RunDetail(r.Context(), id)
		if errors.Is(e, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if e != nil {
			fail(w, 503, "Não foi possível abrir a rodada. Tente novamente.")
			return
		}
		detail = &d
	}
	data := reportPageData(page, detail, r.URL.Query().Get("cursor"))
	if latestComplete != "" {
		data["LatestCompleteURL"] = "/reports?run=" + latestComplete + "#report-reading"
	}
	data["CSRF"] = csrf
	data["Production"] = a.config.Production
	a.render(w, "reports.html", data)
}

func reportTopic(raw string) string {
	labels := map[string]string{"personal_finance": "Suas finanças", "brazil_macro": "Cenário brasileiro", "fixed_income": "Renda fixa", "funds": "Fundos", "long_term": "Longo prazo", "global": "Cenário global", "crypto": "Criptoativos"}
	if v, ok := labels[raw]; ok {
		return v
	}
	if strings.Contains(raw, "_") {
		return "Tema registrado"
	}
	return raw
}
func reportState(raw string) string {
	labels := map[string]string{"waiting_budget": "Aguardando cota", "pending": "Na fila", "running": "Em execução", "complete": "Concluída", "partial": "Parcial", "failed": "Falhou", "failed_unknown": "Resultado não confirmado", "stale": "Contexto alterado", "await_data": "Aguardando dados", "cancelled": "Cancelada", "research": "Em pesquisa", "active": "Ativa", "invalidated": "Invalidada", "archived": "Arquivada", "draft": "Rascunho"}
	if v, ok := labels[raw]; ok {
		return v
	}
	return "Estado não identificado"
}
func reportError(raw string) string {
	labels := map[string]string{"waiting_budget": "Aguardando cota para concluir a análise", "personal_data_missing": "Complete e confirme seus dados e objetivos", "product_evidence_missing": "Faltam fontes atuais sobre o produto", "risk_profile_unknown": "Perfil de risco ainda não confirmado", "risk_profile_unconfirmed": "Perfil de risco ainda não confirmado", "source_unavailable": "Fonte indisponível", "dated_evidence_missing": "Falta evidência com data de publicação", "required_data_missing": "Faltam dados para avaliar a proposta", "context_changed": "Os dados mudaram durante a análise", "evidence_changed": "Uma fonte foi atualizada durante a análise", "rate_limited": "Limite diário atingido", "failed_unknown": "Resultado da execução não confirmado", "not_authenticated": "Serviço de análise aguardando autenticação", "unavailable": "Serviço de análise indisponível", "paused": "Acompanhamento pausado", "invalid_output": "O resultado não passou pela validação"}
	if v, ok := labels[raw]; ok {
		return v
	}
	return "A execução registrou uma limitação. Consulte o resultado completo."
}
func reportMandate(raw string) string {
	labels := map[string]string{"long_term": "Longo prazo", "tactical": "Tático", "cross_mandate": "Múltiplos objetivos", "not_applicable": "Não se aplica"}
	if v, ok := labels[raw]; ok {
		return v
	}
	return "Objetivo não informado"
}
