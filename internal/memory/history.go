package memory

import (
	"context"
	"time"
)

// FactVersion is an authenticated dashboard projection; it is never sent to Telegram.
type FactVersion struct {
	Fact
	Status     string     `json:"status"`
	Authority  string     `json:"authority"`
	SourceText string     `json:"source_text"`
	ValidTo    *time.Time `json:"valid_to"`
}

func (s *Store) FactHistory(ctx context.Context) ([]FactVersion, error) {
	versions := []FactVersion{}
	rows, err := s.DB.Query(ctx, `SELECT f.id::text,f.predicate,f.value,f.version,f.channel,f.source_event_id,f.valid_from,f.status,f.authority,e.body,f.valid_to FROM memory_facts f JOIN conversation_events e ON e.id=f.source_event_id AND e.user_id=f.user_id WHERE f.user_id=$1 ORDER BY f.valid_from DESC,f.version DESC`, UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v FactVersion
		if err = rows.Scan(&v.ID, &v.Predicate, &v.Value, &v.Version, &v.Channel, &v.SourceID, &v.CreatedAt, &v.Status, &v.Authority, &v.SourceText, &v.ValidTo); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}
