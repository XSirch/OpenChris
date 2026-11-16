package council

import (
	"context"
	"encoding/json"
	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
	"strings"
	"testing"
)

func TestAutomaticPromptRequiresPersonalCashAndVerifiedProducts(t *testing.T) {
	for _, stage := range []int{1, 2, 4} {
		prompt := automaticPrompt(stage)
		for _, required := range []string{"personal_finance", "can_recommend", "produto", "valor"} {
			if !strings.Contains(prompt, required) {
				t.Errorf("stage %d omits %s", stage, required)
			}
		}
	}
}

func TestPersonalCashContextIsAggregatedAndChangeInvalidatesAnalysis(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "aggregate", true: "changed"}[change], func(t *testing.T) {
			s, store, p, itemID := investmentFixture(t, 1)
			ctx := context.Background()
			_, err := store.DB.Exec(ctx, `INSERT INTO pluggy_accounts(user_id,external_id,item_id,name,account_type,subtype,balance,currency,observed_at,content_hash,payload) VALUES($1,'private-account',$2,'Private account','BANK','CHECKING_ACCOUNT',1000,'BRL',clock_timestamp(),'fixture','{}');`, memory.UserID, itemID)
			if err != nil {
				t.Fatal(err)
			}
			receipt := ingest(t, s, "dashboard", "personal-cash", "Analise meu caixa e gastos")
			p.run = func(_ context.Context, in codex.Request) (codex.Result, error) {
				var data struct {
					Personal *finance.PersonalFinance `json:"personal_finance"`
				}
				if err := json.Unmarshal(in.Context, &data); err != nil {
					t.Fatal(err)
				}
				if data.Personal == nil || len(data.Personal.Cash) != 1 || data.Personal.Cash[0].AmountCents != 100000 {
					t.Fatalf("cash missing from context: %+v", data.Personal)
				}
				if strings.Contains(string(in.Context), "private-account") || strings.Contains(string(in.Context), "Private account") {
					t.Fatal("account identifier leaked")
				}
				if change {
					if _, err := store.DB.Exec(ctx, `UPDATE pluggy_accounts SET balance=800 WHERE external_id='private-account'`); err != nil {
						t.Fatal(err)
					}
				}
				return answer(in), nil
			}
			process(t, s)
			analysis, err := s.Analysis(ctx, receipt.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "complete"
			if change {
				want = "stale"
			}
			if analysis.Status != want {
				t.Fatalf("status=%s want=%s", analysis.Status, want)
			}
		})
	}
}
