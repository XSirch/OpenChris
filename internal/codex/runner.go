package codex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type RunnerConfig struct {
	Binary, StateDir, TempDir, Token, Model, Effort string
	Key                                             []byte
	Timeout                                         time.Duration
	prefixArgs                                      []string
}
type Runner struct {
	cfg     RunnerConfig
	cache   authCache
	gate    chan struct{}
	mu      sync.RWMutex
	blocked string
}

func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if cfg.Binary == "" || !filepath.IsAbs(cfg.Binary) || cfg.StateDir == "" || cfg.TempDir == "" || len(cfg.Token) < 32 || len(cfg.Key) != 32 {
		return nil, failure("invalid_configuration")
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.Effort == "" {
		cfg.Effort = "xhigh"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 180 * time.Second
	}
	if cfg.Timeout < time.Second || cfg.Timeout > 180*time.Second {
		return nil, failure("invalid_configuration")
	}
	r := &Runner{cfg: cfg, cache: authCache{path: filepath.Join(cfg.StateDir, "account.enc"), key: cfg.Key}, gate: make(chan struct{}, 1)}
	if _, err := r.cache.load(); err != nil {
		return nil, err
	}
	if release, err := r.acquire(); err == nil {
		release()
	} else if Code(err) != "busy" {
		return nil, err
	}
	return r, nil
}
func (r *Runner) block(code string) {
	r.mu.Lock()
	r.blocked = code
	r.mu.Unlock()
	writeSynced(filepath.Join(r.cfg.StateDir, ".blocked"), []byte(code))
}
func (r *Runner) blockedState() string { r.mu.RLock(); defer r.mu.RUnlock(); return r.blocked }
func (r *Runner) home() (string, error) {
	auth, err := r.cache.load()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(r.cfg.TempDir, "conselho-codex-")
	if err != nil {
		return "", failure("unavailable")
	}
	if err = os.Chmod(dir, 0700); err == nil && len(auth) > 0 {
		err = os.WriteFile(filepath.Join(dir, "auth.json"), auth, 0600)
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", failure("auth_storage_failed")
	}
	return dir, nil
}
func (r *Runner) persist(home string) error {
	raw, err := readLimited(filepath.Join(home, "auth.json"), 65536)
	if os.IsNotExist(err) {
		return nil
	}
	if err == nil {
		err = r.cache.save(raw)
	}
	if err != nil {
		r.block("auth_storage_failed")
		return failure("auth_storage_failed")
	}
	return nil
}
func (r *Runner) Status(ctx context.Context) Status {
	out := Status{State: r.blockedState(), Model: r.cfg.Model}
	if out.State != "" {
		return out
	}
	release, lockErr := r.acquire()
	if lockErr != nil {
		out.State = Code(lockErr)
		return out
	}
	defer release()
	if blocked := r.blockedState(); blocked != "" {
		out.State = blocked
		return out
	}
	home, err := r.home()
	if err != nil {
		out.State = Code(err)
		return out
	}
	defer os.RemoveAll(home)
	if _, err = os.Stat(filepath.Join(home, "auth.json")); err != nil {
		out.State = "not_authenticated"
		return out
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = r.beginOperation(); err != nil {
		out.State = Code(err)
		return out
	}
	var output limitedBuffer
	cmd := r.command(bounded, home, "-c", `cli_auth_credentials_store="file"`, "login", "status")
	cmd.Stdout = &output
	cmd.Stderr = &output
	if cmd.Run() != nil || !output.contains("Logged in using ChatGPT") {
		out.State = "not_authenticated"
	} else {
		out.State = "available"
	}
	if err = r.finishOperation(home); err != nil {
		out.State = Code(err)
	}
	return out
}
func (r *Runner) Login(ctx context.Context, in io.Reader, out io.Writer) error {
	release, err := r.acquire()
	if err != nil {
		return err
	}
	defer release()
	if blocked := r.blockedState(); blocked != "" {
		return failure(blocked)
	}
	home, err := r.home()
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	cmd := r.command(ctx, home, "-c", `cli_auth_credentials_store="file"`, "login", "--device-auth")
	if err = r.beginOperation(); err != nil {
		return err
	}
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = out
	runErr := cmd.Run()
	if err = r.finishOperation(home); err != nil {
		return err
	}
	if runErr != nil {
		return failure("not_authenticated")
	}
	return nil
}
func (r *Runner) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if req.Method == "GET" && req.URL.Path == "/healthz" {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	if !authorized(req.Header.Get("Authorization"), r.cfg.Token) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(Error{Kind: "unauthorized"})
		return
	}
	if req.Method == "GET" && req.URL.Path == "/v1/status" {
		json.NewEncoder(w).Encode(r.Status(req.Context()))
		return
	}
	if req.Method != "POST" || (req.URL.Path != "/v1/run" && req.URL.Path != "/v1/research") {
		w.WriteHeader(404)
		return
	}
	limit := MaxContext + 1024
	if req.URL.Path == "/v1/research" {
		limit = 1024
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, req.Body, int64(limit)))
	if err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(Error{Kind: "invalid_request"})
		return
	}
	var result any
	if req.URL.Path == "/v1/research" {
		var input ResearchRequest
		if strictJSON(raw, &input) != nil {
			err = failure("invalid_request")
		} else {
			result, err = r.Research(req.Context(), input)
		}
	} else {
		var input Request
		if strictJSON(raw, &input) != nil {
			err = failure("invalid_request")
		} else {
			result, err = r.Run(req.Context(), input)
		}
	}
	if err != nil {
		code := Code(err)
		status := 503
		if code == "busy" {
			status = 429
		}
		if code == "invalid_request" {
			status = 400
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(Error{Kind: code, Transient: code == "busy"})
		return
	}
	json.NewEncoder(w).Encode(result)
}
