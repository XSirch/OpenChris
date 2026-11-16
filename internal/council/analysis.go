package council

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/memory"
)

var ErrNotFound = errors.New("analysis not found")

type Analysis struct {
	SourceEventID int64         `json:"source_event_id"`
	AgentID       string        `json:"agent_id"`
	Status        string        `json:"status"`
	ErrorCode     string        `json:"error_code,omitempty"`
	AsOf          *time.Time    `json:"as_of"`
	Result        *codex.Result `json:"result"`
	Sources       []source      `json:"sources"`
}

func (s *Service) Analysis(ctx context.Context, eventID int64) (Analysis, error) {
	out := Analysis{SourceEventID: eventID, Sources: []source{}}
	var raw, snapshot []byte
	err := s.db.QueryRow(ctx, `SELECT j.agent_id,j.status,coalesce(j.error_code,''),r.result,m.snapshot FROM council_jobs j JOIN conversation_events e ON e.id=j.event_id LEFT JOIN council_results r ON r.source_event_id=j.event_id LEFT JOIN council_manifests m ON m.id=j.context_manifest_id WHERE j.event_id=$1 AND e.user_id=$2`, eventID, memory.UserID).Scan(&out.AgentID, &out.Status, &out.ErrorCode, &raw, &snapshot)
	if err != nil {
		return out, ErrNotFound
	}
	if len(raw) > 0 {
		out.Result = &codex.Result{}
		if err = json.Unmarshal(raw, out.Result); err != nil {
			return out, err
		}
	}
	if len(snapshot) > 0 {
		var data contextData
		if err = json.Unmarshal(snapshot, &data); err != nil {
			return out, err
		}
		out.AsOf = &data.AsOf
		out.Sources = data.Sources
	}
	return out, nil
}
