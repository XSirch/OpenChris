package httpapp

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"openchris/conselho/internal/council"
	"openchris/conselho/internal/pluggy"
	"openchris/conselho/internal/telegram"
	"strings"
	"time"
)

type Config struct {
	AutonomyEnabled                                 bool
	AutonomyDailyLimit                              int
	Codex                                           council.Config
	Telegram                                        telegram.Config
	Pluggy                                          pluggy.Config
	TelegramClient                                  *telegram.Client
	Demo                                            bool
	Production                                      bool
	Password, TelegramSecret, BenchToken, PublicURL string
	SessionTTL                                      time.Duration
}

func (c Config) Validate() error {
	if c.AutonomyEnabled {
		limit := c.AutonomyDailyLimit
		if limit == 0 {
			limit = 20
		}
		if !c.Production || !c.Codex.Enabled {
			return errors.New("autonomy requires production and the configured Codex runner")
		}
		if limit < 1 || limit > 20 || limit >= c.Codex.DailyLimit {
			return errors.New("AUTONOMY_DAILY_LIMIT must be 1..20 and leave owner capacity within CODEX_DAILY_LIMIT")
		}
	}
	if !c.Production && (c.Telegram.Enabled || c.Pluggy.Enabled || c.Codex.Enabled) {
		return errors.New("real integrations require production mode")
	}
	if err := c.Telegram.Validate(); err != nil {
		return err
	}
	if err := c.Pluggy.Validate(); err != nil {
		return err
	}
	c.Codex.PublicURL = c.PublicURL
	if err := c.Codex.Validate(); err != nil {
		return err
	}
	if c.Production && c.Demo {
		return errors.New("production cannot enable demonstrations")
	}
	if !c.Production && !c.Demo {
		return errors.New("this version requires the deterministic conversation processor")
	}
	if c.Password == "" {
		return errors.New("APP_PASSWORD is required")
	}
	if !c.Production && c.TelegramSecret == "" {
		return errors.New("login password and webhook secret are required")
	}
	if c.Production && c.PublicURL == "" {
		return errors.New("PUBLIC_BASE_URL must be an HTTPS origin")
	}
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.User != nil || u.Fragment != "" {
			return errors.New("PUBLIC_BASE_URL must be an HTTPS origin")
		}
		if len(c.Password) < 20 || c.Password == "demo-conselho" || strings.HasPrefix(c.Password, "SUBSTITUA_") {
			return errors.New("APP_PASSWORD must be a unique password with at least 20 characters")
		}
		if !c.Production && (len(c.TelegramSecret) < 32 || strings.HasPrefix(c.TelegramSecret, "SUBSTITUA_")) {
			return errors.New("public deployment requires unique password >=20 chars and webhook secret >=32 chars")
		}
	}
	return nil
}
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func equal(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (a *App) session(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("conselho_session")
	if err != nil || len(cookie.Value) != 64 {
		return "", false
	}
	var csrf string
	err = a.store.DB.QueryRow(r.Context(), `SELECT csrf FROM sessions WHERE token_hash=$1 AND expires_at>now()`, digest(cookie.Value)).Scan(&csrf)
	return csrf, err == nil
}
func (a *App) originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if a.config.PublicURL != "" {
		return equal(origin, a.config.PublicURL)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return equal(origin, scheme+"://"+r.Host)
}
func (a *App) authorized(w http.ResponseWriter, r *http.Request, write bool) (string, bool) {
	csrf, ok := a.session(r)
	if !ok {
		fail(w, 401, "Sua sessão terminou. Entre novamente.")
		return "", false
	}
	if write && (!a.originOK(r) || !equal(csrf, r.Header.Get("X-CSRF-Token"))) {
		fail(w, 403, "A sessão não autorizou esta alteração. Recarregue a página.")
		return "", false
	}
	return csrf, true
}
func (a *App) cookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.config.PublicURL != "", MaxAge: maxAge}
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		token := randomToken()
		http.SetCookie(w, a.cookie("conselho_login", token, 600))
		a.render(w, "login.html", map[string]any{"CSRF": token, "Local": a.config.PublicURL == "", "Production": a.config.Production, "ReturnTo": safeReturnTarget(r.URL.Query().Get("return_to"))})
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.mu.Lock()
	attempt := a.attempts[host]
	if time.Since(attempt.since) > 15*time.Minute {
		attempt = loginAttempt{since: time.Now()}
	}
	limited := attempt.count >= 10
	a.mu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "900")
		fail(w, 429, "Muitas tentativas. Aguarde 15 minutos antes de tentar novamente.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		fail(w, 400, "Não foi possível ler o formulário.")
		return
	}
	cookie, err := r.Cookie("conselho_login")
	if err != nil || cookie.Value == "" || !equal(cookie.Value, r.FormValue("csrf")) || !a.originOK(r) {
		fail(w, 403, "O formulário expirou. Volte à página de entrada.")
		return
	}
	if !equal(a.config.Password, r.FormValue("password")) {
		a.mu.Lock()
		attempt.count++
		a.attempts[host] = attempt
		if len(a.attempts) > 1000 {
			for k, v := range a.attempts {
				if time.Since(v.since) > 15*time.Minute {
					delete(a.attempts, k)
				}
			}
		}
		a.mu.Unlock()
		fail(w, 401, "Senha incorreta. Volte à página de entrada e tente novamente.")
		return
	}
	token, csrf := randomToken(), randomToken()
	ttl := a.config.SessionTTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	if _, err = a.store.DB.Exec(r.Context(), `INSERT INTO sessions(token_hash,csrf,expires_at) VALUES($1,$2,$3)`, digest(token), csrf, time.Now().Add(ttl)); err != nil {
		fail(w, 503, "O banco está indisponível. Tente novamente quando a conexão voltar.")
		return
	}
	a.mu.Lock()
	delete(a.attempts, host)
	a.mu.Unlock()
	http.SetCookie(w, a.cookie("conselho_session", token, int(ttl.Seconds())))
	http.SetCookie(w, a.cookie("conselho_login", "", -1))
	w.Header().Set("Location", safeReturnTarget(r.FormValue("return_to")))
	w.WriteHeader(303)
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, true); !ok {
		return
	}
	cookie, _ := r.Cookie("conselho_session")
	if _, err := a.store.DB.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, digest(cookie.Value)); err != nil {
		fail(w, 503, "Não foi possível encerrar a sessão. Tente novamente.")
		return
	}
	http.SetCookie(w, a.cookie("conselho_session", "", -1))
	respond(w, 200, map[string]bool{"logged_out": true})
}

func (a *App) checkBenchmark(w http.ResponseWriter, r *http.Request) bool {
	if a.config.Production || a.config.BenchToken == "" || a.config.PublicURL != "" {
		http.NotFound(w, r)
		return false
	}
	if !equal(r.Header.Get("Authorization"), fmt.Sprint("Bearer ", a.config.BenchToken)) {
		fail(w, 401, "Benchmark não autorizado.")
		return false
	}
	return true
}
func localOrigin(public string) string { return strings.TrimRight(public, "/") }
