package finance

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/memory"
)

const otherUser = "00000000-0000-4000-8000-000000000002"

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("FINANCE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("FINANCE_TEST_DATABASE_URL must name the isolated conselho_finance_test database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "conselho_finance_test" {
		t.Fatal("refusing finance setup outside conselho_finance_test")
	}
	mem, err := memory.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mem.Close)
	if _, err := mem.DB.Exec(context.Background(), `TRUNCATE finance_audit,finance_transactions,finance_net_worth,finance_categories RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	// Startup must be repeatable, and the seeded categories must not depend on GET.
	second, err := memory.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
	return New(mem.DB)
}

// Removing the version predicate or replacing category IDs on rename must fail.
func TestCategoryRenamePreservesExpenseAndRejectsLostUpdate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	categories, err := s.Categories(ctx, memory.UserID)
	if err != nil || len(categories) != 6 {
		t.Fatalf("default categories: %+v %v", categories, err)
	}
	before, err := s.Categories(ctx, memory.UserID)
	if err != nil || before[0].ID != categories[0].ID {
		t.Fatalf("stable categories: %+v %v", before, err)
	}
	custom, err := s.CreateCategory(ctx, memory.UserID, CategoryInput{Name: "  Educação  ", Color: "#8B5CF6"})
	if err != nil || custom.Name != "Educação" || custom.Version != 1 {
		t.Fatalf("create category: %+v %v", custom, err)
	}
	if _, err := s.CreateCategory(ctx, memory.UserID, CategoryInput{Name: "educação", Color: "#123456"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("case-insensitive duplicate: %v", err)
	}
	expense, err := s.CreateExpense(ctx, memory.UserID, ExpenseInput{Date: "2026-10-27", Description: "Curso", CategoryID: custom.ID, Account: "Conta pessoal", Status: "confirmed", AmountCents: 15090, IdempotencyKey: "category-link-1"})
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := s.UpdateCategory(ctx, memory.UserID, custom.ID, CategoryInput{Name: "Formação", Color: "#112233", Version: 1})
	if err != nil || renamed.ID != custom.ID || renamed.Version != 2 {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	if _, err := s.UpdateCategory(ctx, memory.UserID, custom.ID, CategoryInput{Name: "Stale", Color: "#112233", Version: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if _, err := s.UpdateCategory(ctx, otherUser, custom.ID, CategoryInput{Name: "Foreign", Color: "#112233", Version: 2}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign category update: %v", err)
	}
	rows, err := s.Transactions(ctx, memory.UserID)
	if err != nil || len(rows) != 1 || rows[0].ID != expense.ID || rows[0].CategoryID != custom.ID {
		t.Fatalf("rename lost expense linkage: %+v %v", rows, err)
	}
	var versions int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM finance_audit WHERE user_id=$1 AND entity_id=$2`, memory.UserID, custom.ID).Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("category audit: %d %v", versions, err)
	}
}

// Removing same-user category enforcement or the unique request key must fail.
func TestExpenseIsolationConcurrentIdempotencyAndDurability(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	categories, err := s.Categories(ctx, memory.UserID)
	if err != nil {
		t.Fatal(err)
	}
	in := ExpenseInput{Date: "2026-10-27", Description: "  Café e pão  ", CategoryID: categories[0].ID, Account: " Conta corrente ", Status: "pending", AmountCents: 1599, Recurring: true, IdempotencyKey: "expense-once-1"}
	var wg sync.WaitGroup
	results := make(chan Transaction, 5)
	errorsOut := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, err := s.CreateExpense(ctx, memory.UserID, in)
			results <- row
			errorsOut <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	for row := range results {
		if id == "" {
			id = row.ID
		}
		if row.ID != id || row.Description != "Café e pão" || row.Account != "Conta corrente" || row.AmountCents != 1599 || !row.Recurring {
			t.Fatalf("idempotent normalized expense: %+v", row)
		}
	}
	in.AmountCents++
	if _, err := s.CreateExpense(ctx, memory.UserID, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("different content with same key: %v", err)
	}
	in.IdempotencyKey = "foreign-category-1"
	if _, err := s.CreateExpense(ctx, otherUser, in); !errors.Is(err, ErrValidation) {
		t.Fatalf("foreign category accepted: %v", err)
	}
	foreignRows, err := s.Transactions(ctx, otherUser)
	if err != nil || len(foreignRows) != 0 {
		t.Fatalf("foreign transactions: %+v %v", foreignRows, err)
	}
	foreignCategories, err := s.Categories(ctx, otherUser)
	if err != nil || len(foreignCategories) != 0 {
		t.Fatalf("foreign categories: %+v %v", foreignCategories, err)
	}
	reopened, err := memory.Open(ctx, os.Getenv("FINANCE_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err := New(reopened.DB).Transactions(ctx, memory.UserID)
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("durable expense: %+v %v", rows, err)
	}
	var audit, journal, jobs, stream int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM finance_audit WHERE entity_type='expense'),(SELECT count(*) FROM conversation_events),(SELECT count(*) FROM jobs),(SELECT count(*) FROM stream_events)`).Scan(&audit, &journal, &jobs, &stream); err != nil || audit != 1 || journal != 0 || jobs != 0 || stream != 0 {
		t.Fatalf("duplicate audit or conversation effects: %d %d %d %d %v", audit, journal, jobs, stream, err)
	}
}

// Treating missing wealth as zero, allowing duplicate snapshots, or sorting only
// by insert time must fail: a backdated record cannot overwrite the newer date.
func TestNetWorthMissingNegativeBalanceLatestDateAndIdempotency(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	missing, err := s.LatestNetWorth(ctx, memory.UserID)
	if err != nil || missing != nil {
		t.Fatalf("missing is not zero: %+v %v", missing, err)
	}
	in := NetWorthInput{AssetsCents: 10050, LiabilitiesCents: 20080, Date: "2026-10-27", IdempotencyKey: "wealth-once-1"}
	first, err := s.SaveNetWorth(ctx, memory.UserID, in)
	if err != nil || first.NetCents != -10030 || first.UpdatedAt.IsZero() {
		t.Fatalf("net assets less liabilities: %+v %v", first, err)
	}
	again, err := s.SaveNetWorth(ctx, memory.UserID, in)
	if err != nil || again.ID != first.ID || !again.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("wealth idempotency: %+v %v", again, err)
	}
	in.AssetsCents++
	if _, err := s.SaveNetWorth(ctx, memory.UserID, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("wealth key conflict: %v", err)
	}
	if _, err := s.SaveNetWorth(ctx, memory.UserID, NetWorthInput{AssetsCents: 0, LiabilitiesCents: 0, Date: "2026-09-01", IdempotencyKey: "backdated-zero-1"}); err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestNetWorth(ctx, memory.UserID)
	if err != nil || latest == nil || latest.ID != first.ID {
		t.Fatalf("latest observation: %+v %v", latest, err)
	}
	foreign, err := s.LatestNetWorth(ctx, otherUser)
	if err != nil || foreign != nil {
		t.Fatalf("foreign wealth: %+v %v", foreign, err)
	}
	var snapshots, auditRows int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM finance_net_worth WHERE user_id=$1),(SELECT count(*) FROM finance_audit WHERE user_id=$1 AND entity_type='net_worth' AND version=1 AND source='manual')`, memory.UserID).Scan(&snapshots, &auditRows); err != nil || snapshots != 2 || auditRows != 2 {
		t.Fatalf("wealth snapshots or audit duplicated: %d %d %v", snapshots, auditRows, err)
	}
}

// Committing a write separately from its audit record must fail this test.
func TestAuditFailureRollsBackExpense(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	categories, err := s.Categories(ctx, memory.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `CREATE FUNCTION finance_test_audit_failure() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RAISE EXCEPTION ''finance audit fault''; END'; CREATE TRIGGER finance_test_audit_failure BEFORE INSERT ON finance_audit FOR EACH ROW EXECUTE FUNCTION finance_test_audit_failure()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.DB.Exec(context.Background(), `DROP TRIGGER IF EXISTS finance_test_audit_failure ON finance_audit; DROP FUNCTION IF EXISTS finance_test_audit_failure()`); err != nil {
			t.Error(err)
		}
	})
	_, err = s.CreateExpense(ctx, memory.UserID, ExpenseInput{Date: "2026-10-27", Description: "Uncommitted", CategoryID: categories[0].ID, Account: "Account", Status: "confirmed", AmountCents: 10, IdempotencyKey: "audit-rollback-1"})
	if err == nil {
		t.Fatal("audit fault was not exercised")
	}
	rows, err := s.Transactions(ctx, memory.UserID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("expense survived audit rollback: %+v %v", rows, err)
	}
}

// Validation must reject malformed financial data before attempting any query.
func TestInvalidInputsAreRejectedBeforeDatabaseAccess(t *testing.T) {
	s := New(nil)
	ctx := context.Background()
	valid := ExpenseInput{Date: "2026-10-27", Description: "Expense", CategoryID: otherUser, Account: "Account", Status: "confirmed", AmountCents: 10, IdempotencyKey: "valid-key-1"}
	tests := []struct {
		name string
		edit func(*ExpenseInput)
	}{
		{"negative cents", func(in *ExpenseInput) { in.AmountCents = -1 }},
		{"excessive cents", func(in *ExpenseInput) { in.AmountCents = 1000000000001 }},
		{"invalid date", func(in *ExpenseInput) { in.Date = "2026-02-30" }},
		{"non-ISO date", func(in *ExpenseInput) { in.Date = "2026-2-01" }},
		{"empty description", func(in *ExpenseInput) { in.Description = "  " }},
		{"invalid UTF-8", func(in *ExpenseInput) { in.Description = string([]byte{0xff}) }},
		{"NUL text", func(in *ExpenseInput) { in.Account = "a\x00b" }},
		{"empty account", func(in *ExpenseInput) { in.Account = " " }},
		{"invalid status", func(in *ExpenseInput) { in.Status = "paid" }},
		{"invalid category", func(in *ExpenseInput) { in.CategoryID = "abc" }},
		{"empty key", func(in *ExpenseInput) { in.IdempotencyKey = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := valid
			tt.edit(&in)
			if _, err := s.CreateExpense(ctx, memory.UserID, in); !errors.Is(err, ErrValidation) {
				t.Fatalf("validation: %v", err)
			}
		})
	}
	for _, in := range []CategoryInput{{Name: " ", Color: "#112233"}, {Name: "Valid", Color: "red"}} {
		if _, err := s.CreateCategory(ctx, memory.UserID, in); !errors.Is(err, ErrValidation) {
			t.Fatalf("category validation: %v", err)
		}
	}
	for _, in := range []NetWorthInput{{Date: "2026-02-30", IdempotencyKey: "invalid-date-1"}, {Date: "2026-10-27", AssetsCents: -1, IdempotencyKey: "invalid-cents-1"}, {Date: "2026-10-27", LiabilitiesCents: 1000000000001, IdempotencyKey: "invalid-limit-1"}} {
		if _, err := s.SaveNetWorth(ctx, memory.UserID, in); !errors.Is(err, ErrValidation) {
			t.Fatalf("wealth validation: %v", err)
		}
	}
	if _, err := s.Categories(ctx, ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid user: %v", err)
	}
}
