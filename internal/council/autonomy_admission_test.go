package council

import (
	"context"
	"openchris/conselho/internal/memory"
	"testing"
	"time"
)

func TestManualConversationWaitsForAutomaticInferenceThenRuns(t *testing.T) {
	service, store, provider := fixture(t, 30)
	receipt := ingest(t, service, "dashboard", "manual-while-automatic", "Atlas, mostre as fontes disponíveis.")
	run, task := memory.ID(), memory.ID()
	ctx := context.Background()
	if _, err := store.DB.Exec(ctx, `INSERT INTO autonomy_runs(id,user_id,status,reason,topic,fingerprint,created_at) VALUES($1,$2,'running','scheduled','fixed_income','test',clock_timestamp())`, run, memory.UserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.DB.Exec(context.Background(), `DELETE FROM autonomy_tasks WHERE run_id=$1`, run)
		store.DB.Exec(context.Background(), `DELETE FROM autonomy_runs WHERE id=$1`, run)
	})
	if _, err := store.DB.Exec(ctx, `INSERT INTO autonomy_tasks(id,run_id,stage,agent_id,status) VALUES($1,$2,0,'A14','running')`, task, run); err != nil {
		t.Fatal(err)
	}
	processed, err := service.ProcessOne(ctx)
	if err != nil || processed || provider.calls.Load() != 0 {
		t.Fatalf("overlapping inference: processed=%v calls=%d err=%v", processed, provider.calls.Load(), err)
	}
	var status string
	if err = store.DB.QueryRow(ctx, `SELECT status FROM council_jobs WHERE event_id=$1`, receipt.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("manual state %s %v", status, err)
	}
	if _, err = store.DB.Exec(ctx, `UPDATE autonomy_tasks SET status='complete' WHERE id=$1`, task); err != nil {
		t.Fatal(err)
	}
	process(t, service)
	if provider.calls.Load() != 1 {
		t.Fatal("manual work did not resume")
	}
}

func TestManualAdmissionSeesAutomaticCommitAfterWaitingForLock(t *testing.T) {
	service, store, _ := fixture(t, 30)
	ctx := context.Background()
	ingest(t, service, "dashboard", "manual-waiting-lock", "Atlas, consulte as fontes.")
	run, task := memory.ID(), memory.ID()
	tx, err := store.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(20261027)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan job, 1)
	fail := make(chan error, 1)
	go func() {
		item, _, err := service.claim(ctx, "available")
		if err != nil {
			fail <- err
			return
		}
		done <- item
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err = store.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=20261027 AND NOT granted)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("claim never waited for global admission")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO autonomy_runs(id,user_id,status,reason,topic,fingerprint,created_at) VALUES($1,$2,'running','scheduled','fixed_income','test',clock_timestamp())`, run, memory.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO autonomy_tasks(id,run_id,stage,agent_id,status) VALUES($1,$2,0,'A14','running')`, task, run); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.DB.Exec(context.Background(), `DELETE FROM autonomy_tasks WHERE run_id=$1`, run)
		store.DB.Exec(context.Background(), `DELETE FROM autonomy_runs WHERE id=$1`, run)
	})
	select {
	case item := <-done:
		if item.EventID != 0 {
			t.Fatal("manual inference admitted using pre-lock snapshot")
		}
	case err := <-fail:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("claim did not release admission")
	}
}

func TestExpiredAutomaticLeaseCannotBlockManualWorkWithSupervisorStopped(t *testing.T) {
	service, store, provider := fixture(t, 30)
	ctx := context.Background()
	ingest(t, service, "dashboard", "manual-after-automatic-crash", "Atlas, consulte as fontes.")
	run, task := memory.ID(), memory.ID()
	if _, err := store.DB.Exec(ctx, `INSERT INTO autonomy_runs(id,user_id,status,reason,topic,fingerprint,created_at) VALUES($1,$2,'running','scheduled','fixed_income','test',clock_timestamp())`, run, memory.UserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.DB.Exec(context.Background(), `DELETE FROM autonomy_tasks WHERE run_id=$1`, run)
		store.DB.Exec(context.Background(), `DELETE FROM autonomy_runs WHERE id=$1`, run)
	})
	if _, err := store.DB.Exec(ctx, `INSERT INTO autonomy_tasks(id,run_id,stage,agent_id,status,lease_token,lease_expires_at) VALUES($1,$2,0,'A14','running',$3,clock_timestamp()-interval '1 minute')`, task, run, memory.ID()); err != nil {
		t.Fatal(err)
	}
	process(t, service)
	var status, runStatus string
	if err := store.DB.QueryRow(ctx, `SELECT t.status,r.status FROM autonomy_tasks t JOIN autonomy_runs r ON r.id=t.run_id WHERE t.id=$1`, task).Scan(&status, &runStatus); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 1 || status != "failed_unknown" || runStatus != "partial" {
		t.Fatalf("calls%d task%s run%s", provider.calls.Load(), status, runStatus)
	}
}
