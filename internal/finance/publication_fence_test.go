package finance

import (
	"context"
	"openchris/conselho/internal/memory"
	"testing"
	"time"
)

func TestFinancialWritersWaitForAnalysisPublicationFence(t *testing.T) {
	for _, kind := range []string{"category_create", "category_update", "expense", "net_worth"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			categories, err := s.Categories(ctx, memory.UserID)
			if err != nil || len(categories) == 0 {
				t.Fatal(err)
			}
			tx, err := s.DB.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(20261027)`); err != nil {
				t.Fatal(err)
			}
			bounded, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
			defer cancel()
			switch kind {
			case "category_create":
				_, err = s.CreateCategory(bounded, memory.UserID, CategoryInput{Name: "Fence", Color: "#123456"})
			case "category_update":
				_, err = s.UpdateCategory(bounded, memory.UserID, categories[0].ID, CategoryInput{Name: "Updated", Color: "#123456", Version: categories[0].Version})
			case "expense":
				_, err = s.CreateExpense(bounded, memory.UserID, ExpenseInput{Date: "2026-11-05", Description: "Fence test", CategoryID: categories[0].ID, Account: "Synthetic", Status: "confirmed", AmountCents: 100, IdempotencyKey: "fence-expense-1"})
			case "net_worth":
				_, err = s.SaveNetWorth(bounded, memory.UserID, NetWorthInput{Date: "2026-11-05", AssetsCents: 100, LiabilitiesCents: 0, IdempotencyKey: "fence-wealth-1"})
			}
			if err == nil {
				t.Fatal("financial write bypassed publication fence")
			}
		})
	}
}
