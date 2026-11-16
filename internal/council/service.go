package council

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/memory"
)

type Provider interface {
	Status(context.Context) (codex.Status, error)
	Run(context.Context, codex.Request) (codex.Result, error)
}
type Config struct {
	Enabled                           bool
	InvestmentsEnabled                bool
	RunnerURL, RunnerToken, PublicURL string
	DailyLimit                        int
}

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.DailyLimit < 1 || c.DailyLimit > 30 {
		return errors.New("CODEX_DAILY_LIMIT must be between 1 and 30")
	}
	if _, err := codex.NewClient(c.RunnerURL, c.RunnerToken); err != nil {
		return errors.New("private Codex runner configuration is invalid")
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("Codex requires the private dashboard HTTPS origin")
	}
	return nil
}

type Service struct {
	db           *pgxpool.Pool
	provider     Provider
	cfg          Config
	mu           sync.Mutex
	blockedUntil time.Time
	blockCode    string
}

func New(db *pgxpool.Pool, provider Provider, cfg Config) *Service {
	if cfg.DailyLimit == 0 {
		cfg.DailyLimit = 30
	}
	return &Service{db: db, provider: provider, cfg: cfg}
}
func (s *Service) Enabled() bool { return s.cfg.Enabled }
func (s *Service) Status(ctx context.Context) codex.Status {
	result := codex.Status{State: "disabled", Model: codex.DefaultModel}
	if !s.cfg.Enabled || s.provider == nil {
		return result
	}
	s.mu.Lock()
	if time.Now().Before(s.blockedUntil) {
		result.State = s.blockCode
		s.mu.Unlock()
		return result
	}
	s.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	provider, err := s.provider.Status(bounded)
	if err != nil {
		result.State = "unavailable"
		return result
	}
	switch provider.State {
	case "available", "busy", "not_authenticated", "rate_limited", "policy_violation", "auth_storage_failed":
		result.State = provider.State
	default:
		result.State = "unavailable"
	}
	if result.State != "available" || s.db == nil {
		return result
	}
	var attempts int
	if err = s.db.QueryRow(bounded, `SELECT count(*) FROM council_attempts WHERE quota_day=(clock_timestamp() AT TIME ZONE 'America/Sao_Paulo')::date`).Scan(&attempts); err != nil {
		result.State = "unavailable"
	} else if attempts >= s.cfg.DailyLimit {
		result.State = "rate_limited"
	}
	return result
}
func (s *Service) rememberFailure(code string) {
	delay := time.Duration(0)
	switch code {
	case "not_authenticated":
		delay = time.Minute
	case "rate_limited":
		delay = time.Hour
	case "policy_violation", "auth_storage_failed":
		delay = 24 * 365 * time.Hour
	}
	if delay > 0 {
		s.mu.Lock()
		s.blockCode = code
		s.blockedUntil = time.Now().Add(delay)
		s.mu.Unlock()
	}
}
func validInput(in memory.Input) bool {
	return (in.Channel == "telegram" || in.Channel == "dashboard") && len(in.Key) > 0 && len(in.Key) <= 128 && in.Text != "" && utf8.ValidString(in.Text) && !strings.ContainsRune(in.Text, 0) && utf8.RuneCountInString(in.Text) <= 2000
}
func (s *Service) Ingest(ctx context.Context, in memory.Input) (memory.Receipt, error) {
	in.Text = strings.TrimSpace(in.Text)
	if !validInput(in) {
		return memory.Receipt{}, memory.ErrInvalid
	}
	// Idempotent receipt retrieval remains available while the provider is down.
	var existing memory.Receipt
	var original string
	err := s.db.QueryRow(ctx, `SELECT e.id,e.body,j.status FROM conversation_events e JOIN council_jobs j ON j.event_id=e.id WHERE e.user_id=$1 AND e.channel=$2 AND e.external_key=$3 AND e.direction='incoming'`, memory.UserID, in.Channel, in.Key).Scan(&existing.ID, &original, &existing.Status)
	if err == nil {
		if original != in.Text {
			return existing, memory.ErrConflict
		}
		existing.Duplicate = true
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return existing, err
	}
	state := s.Status(ctx).State
	if state != "available" && state != "busy" {
		return existing, &codex.Error{Kind: state}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return existing, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(20261027)`); err != nil {
		return existing, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO conversation_events(user_id,channel,external_key,direction,body) VALUES($1,$2,$3,'incoming',$4) ON CONFLICT DO NOTHING RETURNING id`, memory.UserID, in.Channel, in.Key, in.Text).Scan(&existing.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT e.id,e.body,coalesce(j.status,'pending') FROM conversation_events e LEFT JOIN council_jobs j ON j.event_id=e.id WHERE e.user_id=$1 AND e.channel=$2 AND e.external_key=$3 AND e.direction='incoming'`, memory.UserID, in.Channel, in.Key).Scan(&existing.ID, &original, &existing.Status)
		if err != nil {
			return existing, err
		}
		if original != in.Text {
			return existing, memory.ErrConflict
		}
		existing.Duplicate = true
		return existing, nil
	}
	if err != nil {
		return existing, err
	}
	if err = s.EnqueueTx(ctx, tx, existing.ID, in.Text); err != nil {
		return existing, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO conversation_threads(user_id,channel,last_input_event_id,state) VALUES($1,$2,$3,'pending') ON CONFLICT(user_id,channel) DO UPDATE SET last_input_event_id=EXCLUDED.last_input_event_id,state='pending',updated_at=clock_timestamp()`, memory.UserID, in.Channel, existing.ID)
	if err == nil {
		err = emit(ctx, tx, "conversation.received", map[string]any{"source_event_id": existing.ID, "channel": in.Channel, "state": "pending"})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	existing.Status = "pending"
	return existing, err
}
func (s *Service) EnqueueTx(ctx context.Context, tx pgx.Tx, eventID int64, text string) error {
	if !s.cfg.Enabled {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO council_jobs(event_id,case_id,agent_id) SELECT id,$2,$3 FROM conversation_events WHERE id=$1 AND user_id=$4 AND direction='incoming' ON CONFLICT(event_id) DO NOTHING`, eventID, memory.ID(), selectProfile(text).ID, memory.UserID)
	return err
}
func (s *Service) TelegramReply(ctx context.Context, tx pgx.Tx, eventID int64) (string, bool, error) {
	if !s.cfg.Enabled {
		return "A IA está pausada. Sua mensagem permanece no histórico do dashboard.\n" + s.cfg.PublicURL + "/dashboard#conversation", true, nil
	}
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM council_jobs WHERE event_id=$1`, eventID).Scan(&status)
	if err != nil {
		return "", false, err
	}
	if status == "pending" || status == "running" {
		return "", false, nil
	}
	link := s.cfg.PublicURL + "/dashboard#conversation"
	if status == "complete" {
		return "Sua análise está pronta. Leia o resultado e as fontes no dashboard autenticado.\n" + link, true, nil
	}
	if status == "stale" {
		return "Os dados mudaram durante a análise. Envie uma nova pergunta para usar as informações atuais.\n" + link, true, nil
	}
	return "A mensagem foi preservada, mas a análise não foi concluída. Consulte o estado no dashboard.\n" + link, true, nil
}
