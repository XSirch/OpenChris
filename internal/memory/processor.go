package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var reserve = regexp.MustCompile(`(?i)^(?:minha reserva é de|quero mudar minha reserva para|quero uma reserva de|prefiro uma reserva de) ([0-9]{1,3}) meses[.!]?$`)

// ProcessOne performs only local deterministic work inside a transaction.
// No provider calls or financial capabilities exist in this first slice.
func (s *Store) ProcessOne(ctx context.Context) (processed bool, err error) {
	var eventID int64
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() {
		tx.Rollback(context.Background())
		if err != nil && eventID > 0 && ctx.Err() == nil {
			retry, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			s.DB.Exec(retry, `UPDATE jobs SET attempts=attempts+1,status=CASE WHEN attempts+1>=5 THEN 'failed' ELSE 'pending' END,error_code='processor_failed',next_run_at=clock_timestamp()+interval '1 second'*power(2,least(attempts,6)) WHERE event_id=$1 AND status='pending'`, eventID)
		}
	}()
	// Serialize temporal updates in arrival order; the unique active index is a second guard.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(20261027)`); err != nil {
		return false, err
	}
	var text, channel, key string
	var ready bool
	err = tx.QueryRow(ctx, `SELECT e.id,e.body,e.channel,e.external_key,j.next_run_at<=now() FROM jobs j JOIN conversation_events e ON e.id=j.event_id WHERE j.status='pending' ORDER BY e.id FOR UPDATE OF j SKIP LOCKED LIMIT 1`).Scan(&eventID, &text, &channel, &key, &ready)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Do not let later preferences overtake a predecessor during backoff.
	if !ready {
		return false, nil
	}
	if err = emit(ctx, tx, "agent.started", map[string]any{"agent_id": "A02", "state": "running", "source_event_id": eventID, "task_title_safe": "Organizar conversa sintética"}); err != nil {
		return false, err
	}
	response := "Conversa registrada nesta demonstração. O episódio pode ser pesquisado no dashboard."
	outcome := "episode_saved"
	if match := reserve.FindStringSubmatch(text); match != nil {
		months, _ := strconv.Atoi(match[1])
		if months > 0 {
			var previous string
			var version int
			err = tx.QueryRow(ctx, `SELECT id::text,version FROM memory_facts WHERE user_id=$1 AND predicate='reserve_months' AND status='active'`, UserID).Scan(&previous, &version)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return false, err
			}
			if previous != "" {
				if _, err = tx.Exec(ctx, `UPDATE memory_facts SET status='superseded',valid_to=clock_timestamp() WHERE id=$1`, previous); err != nil {
					return false, err
				}
			}
			var supersedes any
			if previous != "" {
				supersedes = previous
			}
			if _, err = tx.Exec(ctx, `INSERT INTO memory_facts(id,user_id,predicate,value,version,status,source_event_id,channel,supersedes) VALUES($1,$2,'reserve_months',$3,$4,'active',$5,$6,$7)`, ID(), UserID, strconv.Itoa(months), version+1, eventID, channel, supersedes); err != nil {
				return false, err
			}
			response = fmt.Sprintf("Preferência de reserva registrada: %d meses, versão %d. Essa preferência estará disponível nos dois canais.", months, version+1)
			outcome = "preference_versioned"
		}
	} else if strings.Contains(strings.ToLower(text), "reserva") && strings.Contains(text, "?") {
		var value, origin string
		var version int
		err = tx.QueryRow(ctx, `SELECT value,channel,version FROM memory_facts WHERE user_id=$1 AND predicate='reserve_months' AND status='active'`, UserID).Scan(&value, &origin, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			response = "Você ainda não registrou uma preferência de reserva. Para testar, escreva: Minha reserva é de 6 meses."
		} else if err != nil {
			return false, err
		} else {
			response = fmt.Sprintf("Sua preferência ativa de reserva é de %s meses. Origem: %s, versão %d.", value, origin, version)
			outcome = "active_preference_retrieved"
		}
	} else if strings.Contains(strings.ToLower(text), "investir") {
		response = "Intenção registrada. Esta demonstração não confirma saldo nem avalia investimentos."
	}
	// Outgoing content is built only from these approved templates and typed facts.
	// Raw user text never reaches Telegram replies.
	rows, err := tx.Query(ctx, `SELECT id::text,version FROM memory_facts WHERE user_id=$1 AND status='active' ORDER BY predicate`, UserID)
	if err != nil {
		return false, err
	}
	refs := []map[string]any{}
	for rows.Next() {
		var id string
		var v int
		if err = rows.Scan(&id, &v); err != nil {
			rows.Close()
			return false, err
		}
		refs = append(refs, map[string]any{"id": id, "version": v})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	contextJSON, err := json.Marshal(refs)
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO context_manifests(id,user_id,source_event_id,facts) VALUES($1,$2,$3,$4)`, ID(), UserID, eventID, contextJSON); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO episodes(user_id,source_event_id,channel,body,outcome) VALUES($1,$2,$3,$4,$5)`, UserID, eventID, channel, text, outcome); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO conversation_events(user_id,channel,external_key,direction,body,classification) VALUES($1,$2,$3,'outgoing',$4,'aggregated')`, UserID, channel, key, response); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE conversation_threads SET last_output_event_id=(SELECT id FROM conversation_events WHERE user_id=$1 AND channel=$2 AND external_key=$3 AND direction='outgoing'),state=CASE WHEN last_input_event_id=$4 THEN 'complete' ELSE 'pending' END,updated_at=clock_timestamp() WHERE user_id=$1 AND channel=$2`, UserID, channel, key, eventID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE jobs SET status='complete',completed_at=clock_timestamp(),error_code=NULL WHERE event_id=$1`, eventID); err != nil {
		return false, err
	}
	if err = emit(ctx, tx, "memory.episode_saved", map[string]any{"source_event_id": eventID, "state": "complete", "outcome": outcome}); err != nil {
		return false, err
	}
	if err = emit(ctx, tx, "agent.completed", map[string]any{"agent_id": "A02", "state": "idle", "source_event_id": eventID}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Store) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for n := 0; n < 16; n++ {
				ok, err := s.ProcessOne(ctx)
				if err != nil {
					if ctx.Err() == nil {
						onError(err)
					}
					break
				}
				if !ok {
					break
				}
			}
		}
	}
}
