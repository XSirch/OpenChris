package autonomy

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/codex"
)

type Config struct {
	Enabled                    bool
	DailyLimit, AutoDailyLimit int
}
type Provider interface {
	Status(context.Context) (codex.Status, error)
	Run(context.Context, codex.Request) (codex.Result, error)
	Research(context.Context, codex.ResearchRequest) (codex.ResearchResult, error)
}
type Builder interface {
	Build(context.Context, pgx.Tx, ContextInput) (ContextSnapshot, error)
	Fingerprint(context.Context, pgx.Tx, time.Time) (string, error)
}
type ContextInput struct {
	TaskID, RoundID, AgentID string
	Stage                    int
	Topic                    string
	Evidence                 []Evidence
	Prior                    []Report
	Strategies               []json.RawMessage
}
type Evidence struct {
	ID          string     `json:"id"`
	URL         string     `json:"url"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	Kind        string     `json:"kind"`
	PublishedAt *time.Time `json:"published_at"`
	ObservedAt  time.Time  `json:"observed_at"`
}
type Report struct {
	AgentID string       `json:"agent_id"`
	Stage   int          `json:"stage"`
	Result  codex.Result `json:"result"`
}
type ContextSnapshot struct {
	Context     json.RawMessage
	Fingerprint string
}
type Notice struct {
	ID    string `json:"id"`
	RunID string `json:"run_id"`
	Kind  string `json:"kind"`
}
type SourceStatus struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	URL           string     `json:"url"`
	Kind          string     `json:"kind"`
	State         string     `json:"state"`
	ErrorCode     string     `json:"error_code"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
}
type RunSummary struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason"`
	Topic       string     `json:"topic"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at"`
	ErrorCode   string     `json:"error_code"`
}
type TaskSummary struct {
	ID             string          `json:"id"`
	RunID          string          `json:"run_id"`
	Stage          int             `json:"stage"`
	AgentID        string          `json:"agent_id"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	ErrorCode      string          `json:"error_code"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	StartedAt      *time.Time      `json:"started_at"`
	CompletedAt    *time.Time      `json:"completed_at"`
	Result         json.RawMessage `json:"result,omitempty"`
}
type Status struct {
	Enabled        bool           `json:"enabled"`
	Paused         bool           `json:"paused"`
	State          string         `json:"state"`
	HeartbeatAt    *time.Time     `json:"heartbeat_at"`
	NextCollectAt  *time.Time     `json:"next_collect_at"`
	NextRunAt      *time.Time     `json:"next_run_at"`
	DailyLimit     int            `json:"daily_limit"`
	AutoDailyLimit int            `json:"auto_daily_limit"`
	Attempts       int            `json:"attempts"`
	AutoAttempts   int            `json:"auto_attempts"`
	ReservedCalls  int            `json:"reserved_calls"`
	Sources        []SourceStatus `json:"sources"`
	Runs           []RunSummary   `json:"runs"`
	Tasks          []TaskSummary  `json:"tasks"`
}
type RunDetail struct {
	Run        RunSummary        `json:"run"`
	Tasks      []TaskSummary     `json:"tasks"`
	Evidence   []Evidence        `json:"evidence"`
	Strategies []json.RawMessage `json:"strategies"`
}
