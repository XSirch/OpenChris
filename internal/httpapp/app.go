package httpapp

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"openchris/conselho/assets"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/autonomy"
	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/council"
	"openchris/conselho/internal/memory"
	"openchris/conselho/internal/pluggy"
	"openchris/conselho/internal/telegram"
	"openchris/conselho/web"
)

type loginAttempt struct {
	since time.Time
	count int
}
type App struct {
	autonomyService *autonomy.Service
	councilService  *council.Service
	router          http.Handler
	telegramService *telegram.Service
	pluggyService   *pluggy.Service
	store           *memory.Store
	config          Config
	templates       *template.Template
	mu              sync.Mutex
	attempts        map[string]loginAttempt
	benchPolicy     string
}

var names = []string{"Atlas", "Mnema", "Nina", "Vigia", "Cora", "Maia", "Élio", "Davi", "Helena", "Teseu", "Bento", "Íris", "Lira", "Orion", "Sol", "Kira", "Dante", "Vera", "Lia", "Sofia", "Caio", "Prisma", "Otto"}

func Agents() []map[string]string {
	out := make([]map[string]string, 0, 23)
	for i, name := range names {
		out = append(out, map[string]string{"id": fmt.Sprintf("A%02d", i+1), "name": name, "state": "idle"})
	}
	return out
}

func New(s *memory.Store, config Config) *App {
	config.PublicURL = localOrigin(config.PublicURL)
	config.Codex.PublicURL = config.PublicURL
	config.Telegram.AutonomyEnabled = config.AutonomyEnabled
	if err := config.Validate(); err != nil {
		panic(err)
	}
	zone, _ := time.LoadLocation("America/Sao_Paulo")
	tpl := template.Must(template.New("").Funcs(template.FuncMap{
		"reportTopic": reportTopic, "reportState": reportState, "reportError": reportError, "reportMandate": reportMandate,
		"stamp": func(t time.Time) string { return t.In(zone).Format("02/01 15:04") },
		"investmentStamp": func(t *time.Time) string {
			if t == nil {
				return "N/D"
			}
			return t.In(zone).Format("02/01/2006 15:04")
		},
		"investmentState": investmentStateText, "investmentCoverage": investmentCoverageText, "investmentEmpty": investmentEmptyText,
		"investmentType": investmentTypeText, "investmentStatus": investmentStatusText,
	}).ParseFS(web.Files, "dashboard.html", "login.html", "partials.html", "expenses.html", "investments.html", "autonomy.html", "reports.html", "finance-profile.html"))
	a := &App{store: s, config: config, templates: tpl, attempts: map[string]loginAttempt{}, benchPolicy: benchmarkPolicy()}
	var initErr error
	var integrationDB *pgxpool.Pool
	if s != nil {
		integrationDB = s.DB
	}
	var modelProvider council.Provider
	var autonomyProvider autonomy.Provider
	if config.Codex.Enabled {
		client, err := codex.NewClient(config.Codex.RunnerURL, config.Codex.RunnerToken)
		initErr = err
		modelProvider, autonomyProvider = client, client
		if initErr != nil {
			panic(initErr)
		}
	}
	councilConfig := config.Codex
	councilConfig.InvestmentsEnabled = config.Pluggy.Enabled
	a.councilService = council.New(integrationDB, modelProvider, councilConfig)
	a.autonomyService = autonomy.New(integrationDB, autonomyProvider, council.NewAutonomousBuilder(config.Pluggy.Enabled), autonomy.Config{Enabled: config.AutonomyEnabled, DailyLimit: config.Codex.DailyLimit, AutoDailyLimit: config.AutonomyDailyLimit})
	if config.TelegramClient != nil {
		a.telegramService, initErr = telegram.NewWithClient(integrationDB, config.Telegram, a.telegramReply, config.TelegramClient)
	} else {
		a.telegramService, initErr = telegram.New(integrationDB, config.Telegram, a.telegramReply)
	}
	if initErr != nil {
		panic(initErr)
	}
	a.telegramService.SetAnalysis(a.councilService)
	a.pluggyService, initErr = pluggy.New(integrationDB, config.Pluggy)
	if initErr != nil {
		panic(initErr)
	}
	officeFiles, err := fs.Sub(assets.Files, "office")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/dashboard", 303) })
	mux.HandleFunc("GET /login", a.login)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /readyz", a.ready)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(fileOnlyFS{web.Files})))
	mux.Handle("GET /static/office/", http.StripPrefix("/static/office/", http.FileServerFS(fileOnlyFS{officeFiles})))
	mux.HandleFunc("GET /static/reference.png", func(w http.ResponseWriter, r *http.Request) {
		data, _ := assets.Files.ReadFile("referencia_dashboard_opcao_1_refinada.png")
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	})
	mux.HandleFunc("GET /static/office-scene.webp", func(w http.ResponseWriter, r *http.Request) {
		data, _ := assets.Files.ReadFile("conselho-office-v2.webp")
		w.Header().Set("Content-Type", "image/webp")
		w.Write(data)
	})
	mux.HandleFunc("GET /dashboard", a.dashboard)
	mux.HandleFunc("GET /reports", a.reports)
	mux.HandleFunc("GET /api/state", a.state)
	a.integrationRoutes(mux)
	a.autonomyRoutes(mux)
	mux.HandleFunc("GET /api/overview", a.overview)
	mux.HandleFunc("GET /expenses", a.expensesPage)
	mux.HandleFunc("GET /api/expenses", a.expensesAPI)
	mux.HandleFunc("GET /expenses.csv", a.expensesCSV)
	mux.HandleFunc("POST /expenses/actions", a.financeAction)
	mux.HandleFunc("GET /api/finance/summary", a.financeSummaryAPI)
	mux.HandleFunc("GET /api/finance/events", a.financeEvents)
	a.personalFinanceRoutes(mux)
	mux.HandleFunc("GET /api/memory/history", a.memoryHistory)
	mux.HandleFunc("POST /api/conversations/turn", a.turn)
	mux.HandleFunc("GET /api/analyses/{id}", a.analysis)
	if !config.Production {
		mux.HandleFunc("POST /demo/telegram", a.turn)
		mux.HandleFunc("POST /webhooks/telegram", a.telegram)
	}
	mux.HandleFunc("GET /api/office/events", a.events)
	mux.HandleFunc("GET /api/office/history", a.officeHistory)
	mux.HandleFunc("GET /api/office/replay", a.officeReplay)
	mux.HandleFunc("GET /bench/query", a.benchmark)
	mux.HandleFunc("POST /bench/webhook", a.benchmark)
	mux.HandleFunc("GET /bench/events", a.benchmark)
	mux.HandleFunc("GET /bench/dashboard", a.benchmark)
	a.router = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if config.Pluggy.Enabled {
			policy := "default-src 'self'; script-src 'self' https://cdn.pluggy.ai/pluggy-connect/v2.8.2/pluggy-connect.js; style-src 'self'; img-src 'self' data:; connect-src 'self' https://connect.pluggy.ai https://api.pluggy.ai; frame-src https://connect.pluggy.ai; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
			if r.URL.Path == "/dashboard" || r.URL.Path == "/expenses" {
				// The pinned SDK injects modal styles without propagating its CSP nonce.
				// Allow style elements here; inline scripts and style attributes stay blocked.
				policy += "; style-src-elem 'self' 'unsafe-inline'"
			}
			w.Header().Set("Content-Security-Policy", policy)
		}
		if config.Production && strings.HasPrefix(r.URL.Path, "/static/") {
			for _, segment := range strings.Split(r.URL.Path, "/") {
				if segment == "." || segment == ".." {
					http.NotFound(w, r)
					return
				}
			}
			if strings.HasSuffix(strings.ToLower(r.URL.Path), ".html") || r.URL.Path == "/static/reference.png" {
				http.NotFound(w, r)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
	return a
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.router.ServeHTTP(w, r) }
func (a *App) memoryHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	versions, err := a.store.FactHistory(r.Context())
	if err != nil {
		fail(w, 503, "Não foi possível recuperar as versões. Tente novamente quando a conexão voltar.")
		return
	}
	respond(w, 200, map[string]any{"versions": versions})
}
func (a *App) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil {
		return
	}
}
func respond(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func fail(w http.ResponseWriter, status int, text string) {
	respond(w, status, map[string]string{"error": text})
}
func decode(w http.ResponseWriter, r *http.Request, out any, max int64) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		if _, ok := err.(*http.MaxBytesError); ok {
			fail(w, 413, "A mensagem excedeu o tamanho permitido.")
		} else {
			fail(w, 400, "Não foi possível ler a mensagem.")
		}
		return false
	}
	if !utf8.Valid(body) || json.Unmarshal(body, out) != nil {
		fail(w, 400, "Envie uma mensagem JSON válida em UTF-8.")
		return false
	}
	return true
}
func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	if id := r.URL.Query().Get("run"); id != "" {
		if !reportID.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		target := "/reports?run=" + id
		if _, ok := a.session(r); !ok {
			loginTarget(w, r, target)
			return
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	a.renderDashboard(w, r, http.StatusOK, "", nil)
}

func (a *App) state(w http.ResponseWriter, r *http.Request) {
	csrf, ok := a.authorized(w, r, false)
	if !ok {
		return
	}
	snap, err := a.store.Snapshot(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		fail(w, 503, "A consulta não foi concluída. Verifique a conexão e tente novamente.")
		return
	}
	mode, processor := "synthetic", "deterministic"
	capabilities := map[string]string{"finance": "manual", "conversation": "demo", "models": "not_configured", "telegram": "demo", "banks": "not_configured"}
	if a.config.Production {
		mode, processor = "production", "disabled"
		capabilities["conversation"], capabilities["telegram"] = "not_configured", "not_configured"
	}
	if a.config.Production {
		if a.config.Telegram.Enabled {
			capabilities["telegram"] = "configured"
		}
		if a.config.Pluggy.Enabled {
			capabilities["banks"] = "configured"
		}
	}
	model := a.councilService.Status(r.Context())
	if a.config.Production && a.config.Codex.Enabled {
		processor = "codex"
		capabilities["models"] = model.State
		if model.State == "available" || model.State == "busy" {
			capabilities["models"] = "configured"
			capabilities["conversation"] = "configured"
		}
	}
	respond(w, 200, map[string]any{"csrf": csrf, "snapshot": snap, "agents": Agents(), "mode": mode, "processor": processor, "capabilities": capabilities, "model": model})
}
func (a *App) turn(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, true); !ok {
		return
	}
	if a.config.Production && !a.config.Codex.Enabled {
		respond(w, http.StatusServiceUnavailable, map[string]string{"code": "integration_not_configured", "error": "A conversa está desativada até configurar a integração de IA."})
		return
	}
	var in memory.Input
	if !decode(w, r, &in, 16384) {
		return
	}
	in.Channel = "dashboard"
	if r.URL.Path == "/demo/telegram" {
		in.Channel = "telegram"
	}
	if a.config.Production {
		receipt, err := a.councilService.Ingest(r.Context(), in)
		if err != nil && err != memory.ErrConflict && err != memory.ErrInvalid {
			respond(w, 503, map[string]string{"code": codex.Code(err), "error": modelText(codex.Code(err)) + ". Consulte o estado em Conexões."})
			return
		}
		a.receipt(w, receipt, err, 202)
		return
	}
	receipt, err := a.store.Ingest(r.Context(), in)
	a.receipt(w, receipt, err, 202)
}
func (a *App) receipt(w http.ResponseWriter, receipt memory.Receipt, err error, status int) {
	if err == memory.ErrConflict {
		fail(w, 409, "Esta chave já foi usada com outro texto. Envie como uma nova mensagem.")
		return
	}
	if err == memory.ErrInvalid {
		fail(w, 400, "Escreva uma mensagem de até 2.000 caracteres e inclua uma chave de envio.")
		return
	}
	if err != nil {
		fail(w, 503, "A mensagem não foi confirmada. Tente novamente com a mesma chave.")
		return
	}
	respond(w, status, receipt)
}

type telegramUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		From struct {
			ID    int64 `json:"id"`
			IsBot bool  `json:"is_bot"`
		} `json:"from"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		ForwardOrigin json.RawMessage `json:"forward_origin"`
		ForwardDate   json.RawMessage `json:"forward_date"`
	} `json:"message"`
}

func (a *App) telegram(w http.ResponseWriter, r *http.Request) {
	if !equal(a.config.TelegramSecret, r.Header.Get("X-Telegram-Bot-Api-Secret-Token")) {
		fail(w, 403, "Origem não autorizada.")
		return
	}
	var in telegramUpdate
	if !decode(w, r, &in, 16384) {
		return
	}
	m := in.Message
	if m == nil || in.UpdateID <= 0 {
		fail(w, 400, "Update de texto sintético esperado.")
		return
	}
	if m.From.ID != 1001 || m.Chat.ID != 1001 || m.Chat.Type != "private" || m.From.IsBot || len(m.ForwardOrigin) > 0 || len(m.ForwardDate) > 0 {
		fail(w, 403, "Apenas a identidade privada de teste é aceita.")
		return
	}
	receipt, err := a.store.Ingest(r.Context(), memory.Input{Channel: "telegram", Key: strconv.FormatInt(in.UpdateID, 10), Text: m.Text})
	a.receipt(w, receipt, err, 200)
}
func (a *App) events(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("after")
	}
	var cursor int64
	var err error
	if raw != "" {
		cursor, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor < 0 {
			fail(w, 400, "Cursor inválido.")
			return
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "Streaming indisponível.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, valid := a.session(r); !valid {
			return
		}
		events, err := a.store.Events(r.Context(), cursor)
		if err != nil {
			return
		}
		for _, event := range events {
			b, _ := json.Marshal(event)
			if _, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.Sequence, b); err != nil {
				return
			}
			cursor = event.Sequence
		}
		if len(events) == 0 {
			if _, err = fmt.Fprint(w, ": conectado\n\n"); err != nil {
				return
			}
		}
		flusher.Flush()
		if r.URL.Query().Get("once") == "1" {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
func (a *App) benchmark(w http.ResponseWriter, r *http.Request) {
	if !a.checkBenchmark(w, r) {
		return
	}
	switch r.URL.Path {
	case "/bench/query":
		var count int
		if err := a.store.DB.QueryRow(r.Context(), `SELECT count(*) FROM benchmark_events WHERE backend='go'`).Scan(&count); err != nil {
			fail(w, 503, "Database unavailable")
			return
		}
		respond(w, 200, map[string]int{"count": count})
	case "/bench/webhook":
		var in struct {
			ID   string `json:"event_id"`
			Text string `json:"text"`
		}
		if !decode(w, r, &in, 65536) {
			return
		}
		if strings.TrimSpace(in.ID) == "" || strings.TrimSpace(in.Text) == "" || strings.ContainsRune(in.ID, 0) || strings.ContainsRune(in.Text, 0) || strings.ContainsRune(in.ID, '\uFFFD') || strings.ContainsRune(in.Text, '\uFFFD') {
			fail(w, 400, "Invalid event")
			return
		}
		if _, err := a.store.DB.Exec(r.Context(), `INSERT INTO benchmark_events(backend,event_id,payload) VALUES('go',$1,$2) ON CONFLICT DO NOTHING`, in.ID, in.Text); err != nil {
			fail(w, 503, "Database unavailable")
			return
		}
		respond(w, 200, map[string]bool{"accepted": true})
	case "/bench/events":
		agents := make([]map[string]string, 0, 23)
		for i := range names {
			agents = append(agents, map[string]string{"id": fmt.Sprintf("A%02d", i+1), "state": "idle"})
		}
		body, _ := json.Marshal(map[string]any{"agents": agents})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", body)
	case "/bench/dashboard":
		data, err := web.Files.ReadFile("benchmark.html")
		if err != nil {
			fail(w, 503, "Benchmark page unavailable")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", a.benchPolicy)
		w.Write(data)
	}
}
