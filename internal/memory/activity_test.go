package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestOfficeActivityProjectsOnlyOwnedKnownEvents(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	receipt, err := s.Ingest(ctx, Input{Channel: "dashboard", Key: "private", Text: "PRIVATE conversation text"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 26, 13, 0, 0, 0, time.UTC)
	officeTestEvent(t, s, UserID, receipt.ID, "agent.started", "A02", started, map[string]any{"state": "private state", "task_title_safe": "PRIVATE title", "channel": "private channel", "body": "PRIVATE body"})
	officeTestEvent(t, s, UserID, receipt.ID, "agent.started", "A99", started, nil)
	officeTestEvent(t, s, UserID, receipt.ID, "private.unknown", "A01", started, nil)
	const foreign = "00000000-0000-4000-8000-000000000002"
	officeTestEvent(t, s, foreign, receipt.ID, "agent.completed", "A01", started.Add(time.Second), nil)
	var foreignID int64
	if err = s.DB.QueryRow(ctx, `INSERT INTO conversation_events(user_id,channel,external_key,direction,body) VALUES($1,'telegram','other','incoming','PRIVATE foreign') RETURNING id`, foreign).Scan(&foreignID); err != nil {
		t.Fatal(err)
	}
	officeTestEvent(t, s, UserID, foreignID, "agent.started", "A03", started, nil)
	page, err := s.OfficeHistory(ctx, "A02", 0, 20)
	if err != nil || len(page.Runs) != 1 {
		t.Fatalf("history: %+v %v", page, err)
	}
	run := page.Runs[0]
	if run.SourceID != receipt.ID || run.Channel != "dashboard" || run.Title != "Organizar conversa" || run.Status != "incomplete" || run.EventCount != 2 || len(run.AgentIDs) != 1 || run.AgentIDs[0] != "A02" || run.StartedAt == nil || !run.StartedAt.Equal(started) || run.CompletedAt != nil || run.DurationMS != nil {
		t.Fatalf("unsafe or invented run: %+v", run)
	}
	replay, err := s.OfficeReplay(ctx, receipt.ID)
	if err != nil || len(replay.Events) != 2 {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	event := replay.Events[1]
	if event.Type != "agent.started" || event.AgentID != "A02" || event.State != "running" || event.Title != "Organização iniciada" || event.Channel != "dashboard" || event.SourceID != receipt.ID {
		t.Fatalf("payload was not projected: %+v", event)
	}
	for _, agent := range []string{"A01", "A03", "A23"} {
		page, err = s.OfficeHistory(ctx, agent, 0, 20)
		if err != nil || len(page.Runs) != 0 {
			t.Fatalf("invented %s activity: %+v %v", agent, page, err)
		}
	}
	for _, id := range []int64{foreignID, 99999} {
		if _, err = s.OfficeReplay(ctx, id); !errors.Is(err, ErrActivityNotFound) {
			t.Fatalf("source %d must be absent: %v", id, err)
		}
	}
}

func TestOfficeActivityUsesTerminalEvidenceAndQueueFailure(t *testing.T) {
	for _, tc := range []struct {
		name, jobStatus, terminal, want string
		started                         bool
		wantDuration                    bool
	}{
		{"pending", "pending", "", "pending", false, false},
		{"start without terminal", "pending", "", "incomplete", true, false},
		{"queue complete without terminal", "complete", "", "incomplete", false, false},
		{"queue failed", "failed", "", "failed", false, false},
		{"completed", "complete", "agent.completed", "complete", true, true},
		{"failed terminal", "pending", "agent.failed", "failed", true, true},
		{"queue failure overrides stale completion", "failed", "agent.completed", "failed", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			receipt, err := s.Ingest(ctx, Input{Channel: "telegram", Key: "state", Text: "Privado"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec(ctx, `UPDATE jobs SET status=$2 WHERE event_id=$1`, receipt.ID, tc.jobStatus); err != nil {
				t.Fatal(err)
			}
			start := time.Date(2026, 10, 26, 14, 0, 0, 0, time.UTC)
			if tc.started {
				officeTestEvent(t, s, UserID, receipt.ID, "agent.started", "A02", start, nil)
			}
			if tc.terminal != "" {
				officeTestEvent(t, s, UserID, receipt.ID, tc.terminal, "A02", start.Add(1500*time.Millisecond), nil)
			}
			replay, err := s.OfficeReplay(ctx, receipt.ID)
			if err != nil || replay.Run.Status != tc.want {
				t.Fatalf("status: %+v %v", replay.Run, err)
			}
			if tc.wantDuration {
				if replay.Run.DurationMS == nil || *replay.Run.DurationMS != 1500 || replay.Run.CompletedAt == nil || !replay.Run.CompletedAt.Equal(start.Add(1500*time.Millisecond)) {
					t.Fatalf("recorded timing: %+v", replay.Run)
				}
			} else if replay.Run.DurationMS != nil || replay.Run.CompletedAt != nil {
				t.Fatalf("invented terminal timing: %+v", replay.Run)
			}
		})
	}
}

func TestOfficeHistoryStableDescendingCursorAndLimits(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := 0; i < 43; i++ {
		if _, err := s.Ingest(ctx, Input{Channel: "dashboard", Key: fmt.Sprint(i), Text: "Privado"}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.OfficeHistory(ctx, "", 0, 0)
	if err != nil || len(page.Runs) != 20 || page.Runs[0].SourceID != 43 || page.Runs[19].SourceID != 24 || !page.HasMore || page.NextBefore == nil || *page.NextBefore != 24 {
		t.Fatalf("default page: %+v %v", page, err)
	}
	// A new run cannot move an exclusive ID cursor or duplicate a prior result.
	if _, err = s.Ingest(ctx, Input{Channel: "telegram", Key: "new", Text: "Privado"}); err != nil {
		t.Fatal(err)
	}
	page, err = s.OfficeHistory(ctx, "", *page.NextBefore, 40)
	if err != nil || len(page.Runs) != 23 || page.Runs[0].SourceID != 23 || page.Runs[22].SourceID != 1 || page.HasMore || page.NextBefore != nil {
		t.Fatalf("exclusive cursor: %+v %v", page, err)
	}
	for _, tc := range []struct {
		agent  string
		before int64
		limit  int
	}{{"A24", 0, 20}, {"A2", 0, 20}, {"", -1, 20}, {"", 0, -1}, {"", 0, 41}} {
		if _, err = s.OfficeHistory(ctx, tc.agent, tc.before, tc.limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid query accepted: %+v %v", tc, err)
		}
	}
	if _, err = s.OfficeReplay(ctx, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid source accepted: %v", err)
	}
}

func TestOfficeReplayTruncatesWithoutInventingTerminalEvents(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	receipt, err := s.Ingest(ctx, Input{Channel: "dashboard", Key: "long", Text: "Privado"})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	officeTestEvent(t, s, UserID, receipt.ID, "agent.started", "A02", stamp, nil)
	for i := 0; i < 200; i++ {
		officeTestEvent(t, s, UserID, receipt.ID, "memory.episode_saved", "", stamp.Add(time.Duration(i+1)*time.Millisecond), nil)
	}
	officeTestEvent(t, s, UserID, receipt.ID, "agent.completed", "A02", stamp.Add(time.Second), nil)
	replay, err := s.OfficeReplay(ctx, receipt.ID)
	if err != nil || len(replay.Events) != 200 || !replay.Truncated || replay.ThroughSequence != replay.Events[199].Sequence || replay.Run.EventCount != 203 || replay.Run.Status != "complete" {
		t.Fatalf("bounded replay: %+v %v", replay, err)
	}
	for i, event := range replay.Events {
		if event.Type == "agent.completed" || (i > 0 && event.Sequence <= replay.Events[i-1].Sequence) {
			t.Fatalf("invented terminal or bad sequence: %+v", event)
		}
	}
}

func officeTestEvent(t *testing.T, s *Store, user string, source int64, kind, agent string, stamp time.Time, extra map[string]any) {
	t.Helper()
	payload := map[string]any{"source_event_id": source}
	if agent != "" {
		payload["agent_id"] = agent
	}
	for key, value := range extra {
		payload[key] = value
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(context.Background(), `INSERT INTO stream_events(user_id,event_type,payload,created_at) VALUES($1,$2,$3,$4)`, user, kind, body, stamp); err != nil {
		t.Fatal(err)
	}
}
