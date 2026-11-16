package memory

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrActivityNotFound = errors.New("office activity not found")

// OfficeRun contains operational metadata only. Conversation bodies and stream
// payloads are deliberately absent from the history and replay contracts.
type OfficeRun struct {
	SourceID    int64      `json:"source_event_id"`
	Channel     string     `json:"channel"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	DurationMS  *int64     `json:"duration_ms"`
	Status      string     `json:"status"`
	AgentIDs    []string   `json:"agent_ids"`
	EventCount  int        `json:"event_count"`
	Title       string     `json:"title"`
}

type OfficeHistory struct {
	Runs       []OfficeRun `json:"runs"`
	NextBefore *int64      `json:"next_before"`
	HasMore    bool        `json:"has_more"`
}

type OfficeEvent struct {
	Sequence  int64     `json:"sequence"`
	Type      string    `json:"event_type"`
	AgentID   string    `json:"agent_id,omitempty"`
	State     string    `json:"state"`
	SourceID  int64     `json:"source_event_id"`
	Channel   string    `json:"channel"`
	CreatedAt time.Time `json:"created_at"`
	Title     string    `json:"title"`
}

type OfficeReplay struct {
	Run             OfficeRun     `json:"run"`
	Events          []OfficeEvent `json:"events"`
	Truncated       bool          `json:"truncated"`
	ThroughSequence int64         `json:"through_sequence"`
}

func ValidOfficeAgent(agent string) bool {
	return len(agent) == 3 && agent[0] == 'A' && ((agent[1] == '0' && agent[2] >= '1' && agent[2] <= '9') ||
		(agent[1] == '1' && agent[2] >= '0' && agent[2] <= '9') || (agent[1] == '2' && agent[2] >= '0' && agent[2] <= '3'))
}

// Every query uses the same allowlist. Agent activity requires a recorded,
// valid agent ID; a conversation receipt never implies an Atlas execution.
const officeEventPredicate = `se.user_id=e.user_id AND se.payload->>'source_event_id'=e.id::text
 AND (se.event_type IN ('conversation.received','memory.episode_saved')
 OR (se.event_type IN ('agent.started','agent.completed','agent.failed')
 AND se.payload->>'agent_id' ~ '^A(0[1-9]|1[0-9]|2[0-3])$'))`

const officeRunsQuery = `SELECT e.id,e.channel,e.created_at,coalesce(cj.status,j.status,''),x.started_at,x.terminal_at,
 coalesce(x.terminal_type,''),x.agent_ids,x.event_count,x.has_progress
 FROM conversation_events e LEFT JOIN jobs j ON j.event_id=e.id LEFT JOIN council_jobs cj ON cj.event_id=e.id
 CROSS JOIN LATERAL (
 SELECT count(*)::integer AS event_count,
 min(se.created_at) FILTER (WHERE se.event_type='agent.started') AS started_at,
 (array_agg(se.created_at ORDER BY se.sequence DESC) FILTER (WHERE se.event_type IN ('agent.completed','agent.failed')))[1] AS terminal_at,
 (array_agg(se.event_type ORDER BY se.sequence DESC) FILTER (WHERE se.event_type IN ('agent.completed','agent.failed')))[1] AS terminal_type,
 coalesce(array_agg(DISTINCT se.payload->>'agent_id' ORDER BY se.payload->>'agent_id')
 FILTER (WHERE se.event_type IN ('agent.started','agent.completed','agent.failed')),ARRAY[]::text[]) AS agent_ids,
 coalesce(bool_or(se.event_type<>'conversation.received'),false) AS has_progress
 FROM stream_events se WHERE ` + officeEventPredicate + `
 ) x WHERE e.user_id=$1 AND e.direction='incoming' AND ($2='' OR $2=ANY(x.agent_ids))
 AND ($3::bigint=0 OR e.id<$3) AND ($4::bigint=0 OR e.id=$4)
 ORDER BY e.id DESC LIMIT $5`

type officeQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func officeRuns(ctx context.Context, db officeQuerier, agent string, before, source int64, limit int) ([]OfficeRun, error) {
	runs := []OfficeRun{}
	rows, err := db.Query(ctx, officeRunsQuery, UserID, agent, before, source, limit)
	if err != nil {
		return runs, err
	}
	defer rows.Close()
	for rows.Next() {
		var run OfficeRun
		var jobStatus, terminal string
		var hasProgress bool
		if err = rows.Scan(&run.SourceID, &run.Channel, &run.CreatedAt, &jobStatus, &run.StartedAt, &run.CompletedAt, &terminal, &run.AgentIDs, &run.EventCount, &hasProgress); err != nil {
			return runs, err
		}
		run.Title = "Organizar conversa"
		switch {
		case jobStatus == "failed" || jobStatus == "failed_unknown" || jobStatus == "blocked" || jobStatus == "stale" || terminal == "agent.failed":
			run.Status = "failed"
		case terminal == "agent.completed":
			run.Status = "complete"
		case jobStatus == "pending" && !hasProgress:
			run.Status = "pending"
		default:
			run.Status = "incomplete"
		}
		if run.StartedAt != nil && run.CompletedAt != nil && !run.CompletedAt.Before(*run.StartedAt) {
			duration := run.CompletedAt.Sub(*run.StartedAt).Milliseconds()
			run.DurationMS = &duration
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) OfficeHistory(ctx context.Context, agent string, before int64, limit int) (OfficeHistory, error) {
	page := OfficeHistory{Runs: []OfficeRun{}}
	if limit == 0 {
		limit = 20
	}
	if (agent != "" && !ValidOfficeAgent(agent)) || before < 0 || limit < 1 || limit > 40 {
		return page, ErrInvalid
	}
	runs, err := officeRuns(ctx, s.DB, agent, before, 0, limit+1)
	if err != nil {
		return page, err
	}
	if len(runs) > limit {
		page.HasMore = true
		runs = runs[:limit]
		cursor := runs[len(runs)-1].SourceID
		page.NextBefore = &cursor
	}
	page.Runs = runs
	return page, nil
}

func (s *Store) OfficeReplay(ctx context.Context, source int64) (OfficeReplay, error) {
	replay := OfficeReplay{Events: []OfficeEvent{}}
	if source <= 0 {
		return replay, ErrInvalid
	}
	// Metadata and event bounds must describe one committed database snapshot.
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return replay, err
	}
	defer tx.Rollback(ctx)
	runs, err := officeRuns(ctx, tx, "", 0, source, 1)
	if err != nil {
		return replay, err
	}
	if len(runs) == 0 {
		return replay, ErrActivityNotFound
	}
	replay.Run = runs[0]
	rows, err := tx.Query(ctx, `SELECT se.sequence,se.event_type,
 coalesce(CASE WHEN se.event_type IN ('agent.started','agent.completed','agent.failed') THEN se.payload->>'agent_id' END,''),se.created_at
 FROM conversation_events e JOIN stream_events se ON `+officeEventPredicate+`
 WHERE e.user_id=$1 AND e.direction='incoming' AND e.id=$2 ORDER BY se.sequence LIMIT 201`, UserID, source)
	if err != nil {
		return replay, err
	}
	for rows.Next() {
		var event OfficeEvent
		if err = rows.Scan(&event.Sequence, &event.Type, &event.AgentID, &event.CreatedAt); err != nil {
			rows.Close()
			return replay, err
		}
		event.SourceID, event.Channel = source, replay.Run.Channel
		switch event.Type {
		case "conversation.received":
			event.State, event.Title = "pending", "Conversa recebida"
		case "agent.started":
			event.State, event.Title = "running", "Organização iniciada"
		case "memory.episode_saved":
			event.State, event.Title = "complete", "Memória registrada"
		case "agent.completed":
			event.State, event.Title = "idle", "Organização concluída"
		case "agent.failed":
			event.State, event.Title = "error", "Organização interrompida"
		}
		replay.Events = append(replay.Events, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return replay, err
	}
	if len(replay.Events) > 200 {
		replay.Events = replay.Events[:200]
		replay.Truncated = true
	}
	if len(replay.Events) > 0 {
		replay.ThroughSequence = replay.Events[len(replay.Events)-1].Sequence
	}
	return replay, tx.Commit(ctx)
}
