package memory

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/migrations"
)

const UserID = "00000000-0000-4000-8000-000000000001"

var ErrConflict = errors.New("idempotency key reused with different content")
var ErrInvalid = errors.New("invalid synthetic message")

type Store struct{ DB *pgxpool.Pool }
type Input struct {
	Channel string `json:"channel"`
	Key     string `json:"idempotency_key"`
	Text    string `json:"text"`
}
type Receipt struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Duplicate bool   `json:"duplicate"`
}
type Message struct {
	ID        int64     `json:"id"`
	Channel   string    `json:"channel"`
	Direction string    `json:"direction"`
	Text      string    `json:"text"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	AgentID   string    `json:"agent_id,omitempty"`
	ErrorCode string    `json:"error_code,omitempty"`
	Analysis  bool      `json:"analysis"`
	SourceID  int64     `json:"source_event_id,omitempty"`
}
type Fact struct {
	ID        string    `json:"id"`
	Predicate string    `json:"predicate"`
	Value     string    `json:"value"`
	Version   int       `json:"version"`
	Channel   string    `json:"channel"`
	SourceID  int64     `json:"source_event_id"`
	CreatedAt time.Time `json:"created_at"`
}
type Episode struct {
	ID        int64     `json:"id"`
	SourceID  int64     `json:"source_event_id"`
	Channel   string    `json:"channel"`
	Text      string    `json:"text"`
	Outcome   string    `json:"outcome"`
	CreatedAt time.Time `json:"created_at"`
}
type ActiveJob struct {
	SourceID       int64      `json:"source_event_id"`
	AgentID        string     `json:"agent_id"`
	Status         string     `json:"status"`
	ErrorCode      string     `json:"error_code,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
}
type Snapshot struct {
	ActiveJobs []ActiveJob    `json:"active_jobs"`
	Facts      []Fact         `json:"facts"`
	Episodes   []Episode      `json:"episodes"`
	Messages   []Message      `json:"messages"`
	Pending    int            `json:"pending"`
	Failed     int            `json:"failed"`
	Cursor     int64          `json:"cursor"`
	Counts     map[string]int `json:"counts"`
}
type Event struct {
	Sequence  int64           `json:"sequence"`
	Type      string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 4
	cfg.MinConns = 1
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	s := &Store{DB: db}
	if err = db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(20261026)`)
	if err == nil {
		var entries []fs.DirEntry
		entries, err = migrations.Files.ReadDir(".")
		for _, entry := range entries {
			if err != nil {
				break
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || strings.HasSuffix(entry.Name(), ".down.sql") {
				continue
			}
			var sql []byte
			sql, err = migrations.Files.ReadFile(entry.Name())
			if err == nil {
				_, err = tx.Exec(ctx, string(sql))
			}
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() { s.DB.Close() }
func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (s *Store) Ingest(ctx context.Context, in Input) (Receipt, error) {
	in.Text = strings.TrimSpace(in.Text)
	if (in.Channel != "telegram" && in.Channel != "dashboard") || len(in.Key) == 0 || len(in.Key) > 128 || !utf8.ValidString(in.Text) || strings.ContainsRune(in.Text, 0) || utf8.RuneCountInString(in.Text) > 2000 || in.Text == "" {
		return Receipt{}, ErrInvalid
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Receipt{}, err
	}
	defer tx.Rollback(ctx)
	var id int64
	// Ingestion and processing share the writer lock. Identity values then
	// become a safe committed stream cursor, including cross-channel writes.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(20261027)`); err != nil {
		return Receipt{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO conversation_events(user_id,channel,external_key,direction,body) VALUES($1,$2,$3,'incoming',$4) ON CONFLICT DO NOTHING RETURNING id`, UserID, in.Channel, in.Key, in.Text).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		var original, status string
		err = tx.QueryRow(ctx, `SELECT e.id,e.body,coalesce(j.status,'pending') FROM conversation_events e LEFT JOIN jobs j ON j.event_id=e.id WHERE e.user_id=$1 AND e.channel=$2 AND e.external_key=$3 AND e.direction='incoming'`, UserID, in.Channel, in.Key).Scan(&id, &original, &status)
		if err != nil {
			return Receipt{}, err
		}
		if original != in.Text {
			return Receipt{}, ErrConflict
		}
		return Receipt{ID: id, Status: status, Duplicate: true}, nil
	}
	if err != nil {
		return Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO jobs(event_id) VALUES($1)`, id); err != nil {
		return Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO conversation_threads(user_id,channel,last_input_event_id,state) VALUES($1,$2,$3,'pending') ON CONFLICT(user_id,channel) DO UPDATE SET last_input_event_id=EXCLUDED.last_input_event_id,state='pending',updated_at=clock_timestamp()`, UserID, in.Channel, id); err != nil {
		return Receipt{}, err
	}
	if err = emit(ctx, tx, "conversation.received", map[string]any{"source_event_id": id, "channel": in.Channel, "state": "pending"}); err != nil {
		return Receipt{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Receipt{}, err
	}
	return Receipt{ID: id, Status: "pending"}, nil
}

func emit(ctx context.Context, tx pgx.Tx, kind string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO stream_events(user_id,event_type,payload) VALUES($1,$2,$3)`, UserID, kind, b)
	return err
}

func (s *Store) Snapshot(ctx context.Context, query string) (Snapshot, error) {
	snap := Snapshot{ActiveJobs: []ActiveJob{}, Facts: []Fact{}, Episodes: []Episode{}, Messages: []Message{}, Counts: map[string]int{"telegram": 0, "dashboard": 0}}
	if len(query) > 200 {
		return snap, ErrInvalid
	}
	// One database snapshot keeps counts, active versions and SSE cursor coherent.
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return snap, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id::text,predicate,value,version,channel,source_event_id,valid_from FROM memory_facts WHERE user_id=$1 AND status='active' ORDER BY predicate`, UserID)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var f Fact
		if err = rows.Scan(&f.ID, &f.Predicate, &f.Value, &f.Version, &f.Channel, &f.SourceID, &f.CreatedAt); err != nil {
			rows.Close()
			return snap, err
		}
		snap.Facts = append(snap.Facts, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return snap, err
	}
	rows, err = tx.Query(ctx, `SELECT id,source_event_id,channel,body,outcome,created_at FROM episodes WHERE user_id=$1 AND ($2='' OR to_tsvector('portuguese',body) @@ websearch_to_tsquery('portuguese',$2)) ORDER BY id DESC LIMIT 100`, UserID, query)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var e Episode
		if err = rows.Scan(&e.ID, &e.SourceID, &e.Channel, &e.Text, &e.Outcome, &e.CreatedAt); err != nil {
			rows.Close()
			return snap, err
		}
		snap.Episodes = append(snap.Episodes, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return snap, err
	}
	rows, err = tx.Query(ctx, `SELECT e.id,e.channel,e.direction,e.body,coalesce(aj.status,j.status,'complete'),e.created_at,coalesce(aj.agent_id,''),coalesce(aj.error_code,''),(e.direction='outgoing' AND cr.source_event_id IS NOT NULL),coalesce(cr.source_event_id,0) FROM conversation_events e LEFT JOIN jobs j ON j.event_id=e.id LEFT JOIN conversation_events src ON e.direction='outgoing' AND src.user_id=e.user_id AND src.channel=e.channel AND src.external_key=e.external_key AND src.direction='incoming' LEFT JOIN council_jobs aj ON aj.event_id=coalesce(src.id,e.id) LEFT JOIN council_results cr ON cr.source_event_id=src.id WHERE e.user_id=$1 ORDER BY e.id DESC LIMIT 60`, UserID)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var m Message
		if err = rows.Scan(&m.ID, &m.Channel, &m.Direction, &m.Text, &m.Status, &m.CreatedAt, &m.AgentID, &m.ErrorCode, &m.Analysis, &m.SourceID); err != nil {
			rows.Close()
			return snap, err
		}
		snap.Messages = append(snap.Messages, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return snap, err
	}
	rows, err = tx.Query(ctx, `SELECT DISTINCT ON(j.agent_id) j.event_id,j.agent_id,j.status,coalesce(j.error_code,''),j.created_at,a.started_at,j.lease_expires_at
 FROM council_jobs j JOIN conversation_events e ON e.id=j.event_id
 LEFT JOIN council_attempts a ON a.run_id=j.run_id
 WHERE e.user_id=$1 AND j.status IN ('pending','running')
 ORDER BY j.agent_id,(j.status='running') DESC,j.event_id`, UserID)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var job ActiveJob
		if err = rows.Scan(&job.SourceID, &job.AgentID, &job.Status, &job.ErrorCode, &job.CreatedAt, &job.StartedAt, &job.LeaseExpiresAt); err != nil {
			rows.Close()
			return snap, err
		}
		snap.ActiveJobs = append(snap.ActiveJobs, job)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return snap, err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status IN ('pending','running')),count(*) FILTER(WHERE status IN ('failed','failed_unknown','blocked','stale')) FROM (SELECT status FROM jobs UNION ALL SELECT status FROM council_jobs) all_jobs`).Scan(&snap.Pending, &snap.Failed); err != nil {
		return snap, err
	}
	if err = tx.QueryRow(ctx, `SELECT coalesce(max(sequence),0) FROM stream_events WHERE user_id=$1`, UserID).Scan(&snap.Cursor); err != nil {
		return snap, err
	}
	rows, err = tx.Query(ctx, `SELECT channel,count(*) FROM conversation_events WHERE user_id=$1 AND direction='incoming' GROUP BY channel`, UserID)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var channel string
		var n int
		if err = rows.Scan(&channel, &n); err != nil {
			rows.Close()
			return snap, err
		}
		snap.Counts[channel] = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return snap, err
	}
	return snap, tx.Commit(ctx)
}

func (s *Store) Events(ctx context.Context, cursor int64) ([]Event, error) {
	events := []Event{}
	rows, err := s.DB.Query(ctx, `SELECT sequence,event_type,payload,created_at FROM stream_events WHERE user_id=$1 AND sequence>$2 ORDER BY sequence LIMIT 100`, UserID, cursor)
	if err != nil {
		return events, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		if err = rows.Scan(&e.Sequence, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			return events, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
