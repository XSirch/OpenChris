package pluggy

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/memory"
)

// A session lock covers both provider reads and the atomic commit across app
// replicas. PostgreSQL releases it if the process or connection disappears.
// Use this same connection for sync SQL so a one-connection pool still works.
func (s *Service) acquireSyncLease(ctx context.Context, itemID string) (*pgxpool.Conn, func(), error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	key := "openchris:pluggy:sync:" + memory.UserID + ":" + strings.ToLower(itemID)
	var locked bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1::text,2026102804))`, key).Scan(&locked)
	if err != nil {
		// A canceled query might have acquired its lock before cancellation. Never
		// put an uncertain session back into the pool.
		discardLeaseConnection(conn)
		return nil, nil, err
	}
	if !locked {
		conn.Release()
		return nil, nil, ErrBusy
	}
	release := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1::text,2026102804))`, key).Scan(&unlocked); err != nil || !unlocked {
			discardLeaseConnection(conn)
			return
		}
		conn.Release()
	}
	return conn, release, nil
}

func discardLeaseConnection(conn *pgxpool.Conn) {
	cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Hijacking prevents a pool borrower from inheriting a session advisory lock.
	raw := conn.Hijack()
	_ = raw.Close(cleanup)
}
