package autonomy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"openchris/conselho/internal/memory"
	"time"
)

var ErrArchiveCursor = errors.New("invalid archive cursor")

type ArchivePage struct {
	Runs []RunSummary
	Next string
}
type archiveCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func (s *Service) Archive(ctx context.Context, cursor string) (ArchivePage, error) {
	out := ArchivePage{Runs: []RunSummary{}}
	var c archiveCursor
	if cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		var id pgtype.UUID
		if e != nil || json.Unmarshal(raw, &c) != nil || c.At.IsZero() || id.Scan(c.ID) != nil {
			return out, ErrArchiveCursor
		}
	}
	rows, err := s.db.Query(ctx, `SELECT `+runColumns+` FROM autonomy_runs WHERE user_id=$1 AND ($2::timestamptz IS NULL OR (created_at,id)<($2::timestamptz,$3::uuid)) ORDER BY created_at DESC,id DESC LIMIT 21`, memory.UserID, nullableArchiveTime(c.At), nullableArchiveID(c.ID))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			return out, e
		}
		out.Runs = append(out.Runs, r)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Runs) > 20 {
		out.Runs = out.Runs[:20]
		last := out.Runs[19]
		raw, _ := json.Marshal(archiveCursor{At: last.CreatedAt, ID: last.ID})
		out.Next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}
func nullableArchiveTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
func nullableArchiveID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
