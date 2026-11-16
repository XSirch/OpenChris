package finance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PersonalFinance struct {
	State                  string          `json:"state"`
	Revision               string          `json:"revision"`
	Month                  string          `json:"month"`
	Profile                Profile         `json:"profile"`
	ProfileComplete        bool            `json:"profile_complete"`
	CanRecommend           bool            `json:"can_recommend"`
	Cash                   []CurrencyTotal `json:"cash"`
	Expenses               []CurrencyTotal `json:"expenses"`
	PreviousExpenses       []CurrencyTotal `json:"previous_expenses"`
	Pending                []CurrencyTotal `json:"pending"`
	Categories             []CategoryTotal `json:"categories"`
	RecurringCount         int             `json:"recurring_count"`
	ReviewCount            int             `json:"review_count"`
	SourceCount            int             `json:"source_count"`
	TransactionCount       int             `json:"transaction_count"`
	AvailableToInvestCents *int64          `json:"available_to_invest_cents"`
	ReserveGapCents        *int64          `json:"reserve_gap_cents"`
	PendingItems           []ActionItem    `json:"pending_items"`
	LastSyncedAt           *time.Time      `json:"last_synced_at"`
}

// ReadPersonalFinance contains only aggregates and confirmed user inputs. The
// caller may send it to the Council without transaction descriptions or IDs.
func ReadPersonalFinance(ctx context.Context, db Queryer, userID string, at time.Time) (PersonalFinance, error) {
	if pool, ok := db.(*pgxpool.Pool); ok {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			return PersonalFinance{}, err
		}
		defer tx.Rollback(ctx)
		result, err := ReadPersonalFinance(ctx, tx, userID, at)
		if err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	local := at.In(loc)
	p := PersonalFinance{State: "awaiting_data", Month: local.Format("2006-01"), Cash: []CurrencyTotal{}, Expenses: []CurrencyTotal{}, PreviousExpenses: []CurrencyTotal{}, Pending: []CurrencyTotal{}, Categories: []CategoryTotal{}, PendingItems: []ActionItem{}}
	profile, err := ReadProfile(ctx, db, userID)
	if err != nil {
		return p, err
	}
	p.Profile = profile
	p.PendingItems = profileIssues(profile)
	for _, goal := range profile.Goals {
		if goal.TargetDate < local.Format("2006-01-02") {
			p.PendingItems = append(p.PendingItems, ActionItem{"goals", "Revise os objetivos vencidos", "Atualize o prazo ou remova o objetivo já concluído.", "/finance/profile#goals"})
			break
		}
	}
	if profile.ConfirmedAt != nil && profile.ConfirmedAt.Before(at.AddDate(0, 0, -30)) {
		p.PendingItems = append(p.PendingItems, ActionItem{"confirmed", "Atualize seus compromissos", "A confirmação tem mais de 30 dias. Revise os compromissos e confirme os dados novamente.", "/finance/profile#confirmed"})
	}
	p.ProfileComplete = len(p.PendingItems) == 0
	if profile.ReserveCurrentCents != nil && profile.ReserveTargetCents != nil {
		gap := *profile.ReserveTargetCents - *profile.ReserveCurrentCents
		if gap < 0 {
			gap = 0
		}
		p.ReserveGapCents = &gap
	}
	rows, err := ReadBankTransactions(ctx, db, userID, at)
	if err != nil {
		return p, err
	}
	p.TransactionCount = len(rows)
	expenses, pending, previous := map[string]CurrencyTotal{}, map[string]CurrencyTotal{}, map[string]CurrencyTotal{}
	categories := map[string]CategoryTotal{}
	previousMonth := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, -1, 0).Format("2006-01")
	material := []any{p.Month}
	cleanProfile := profile
	cleanProfile.Version = 0
	cleanProfile.UpdatedAt = nil
	cleanProfile.ConfirmedAt = nil
	material = append(material, cleanProfile)
	for _, row := range rows {
		material = append(material, []any{row.ID, row.Date, row.Currency, row.AmountCents, row.AmountKnown, row.Kind, row.CategoryName, row.Status})
		if row.ReviewRequired {
			p.ReviewCount++
		}
		if !row.AmountKnown || !validCurrency(row.Currency) || len(row.Date) < 7 || !oneOf(row.Kind, "expense", "refund") {
			continue
		}
		if row.Status == "pending" && row.Date[:7] == p.Month {
			total := pending[row.Currency]
			total.Currency = row.Currency
			total.AmountCents += row.AmountCents
			total.Count++
			pending[row.Currency] = total
			continue
		}
		if row.Status != "confirmed" {
			continue
		}
		if row.Date[:7] == previousMonth {
			total := previous[row.Currency]
			total.Currency = row.Currency
			total.AmountCents += row.AmountCents
			total.Count++
			previous[row.Currency] = total
		}
		if row.Date[:7] != p.Month {
			continue
		}
		total := expenses[row.Currency]
		total.Currency = row.Currency
		total.AmountCents += row.AmountCents
		total.Count++
		expenses[row.Currency] = total
		key := row.Category + "|" + row.Currency
		c := categories[key]
		c.Category = row.Category
		c.CategoryName = row.CategoryName
		c.Currency = row.Currency
		c.AmountCents += row.AmountCents
		c.Count++
		categories[key] = c
		if row.Recurring {
			p.RecurringCount++
		}
	}
	p.Expenses = currencyTotals(expenses)
	p.Pending = currencyTotals(pending)
	p.PreviousExpenses = currencyTotals(previous)
	for _, c := range categories {
		p.Categories = append(p.Categories, c)
	}
	sort.Slice(p.Categories, func(i, j int) bool {
		a, b := p.Categories[i], p.Categories[j]
		if a.Currency != b.Currency {
			return a.Currency < b.Currency
		}
		return a.Category < b.Category
	})
	// Prefer the older provider/local collection time. A cache read cannot renew
	// provider freshness; legacy items without provider dates use local collection.
	accountRows, err := db.Query(ctx, `SELECT a.external_id,a.item_id::text,coalesce(a.balance::text,''),coalesce(a.currency,''),
 i.explicit_owner AND i.superseded_by IS NULL AND i.status NOT IN ('revoked','expired') AND (i.consent_expires_at IS NULL OR i.consent_expires_at>$2),
 i.status IN ('connected','syncing','stale') AND coalesce((i.coverage->>'complete_for_reported_products')='true',false),
 i.last_synced_at IS NOT NULL AND least(i.last_synced_at,i.last_source_sync)>=$2::timestamptz-interval '36 hours',least(i.last_synced_at,i.last_source_sync),
 coalesce(a.account_type,''),coalesce(a.subtype,'')
 FROM pluggy_accounts a JOIN pluggy_items i ON i.user_id=a.user_id AND i.item_id=a.item_id
 WHERE a.user_id=$1 AND a.active AND i.superseded_by IS NULL ORDER BY a.external_id`, userID, at)
	if err != nil {
		return p, err
	}
	cash := map[string]CurrencyTotal{}
	sources := map[string]bool{}
	var incomplete, stale, withheld, unknown int
	for accountRows.Next() {
		var id, itemID, value, currency, accountType, subtype string
		var eligible, complete, fresh bool
		var synced *time.Time
		if err = accountRows.Scan(&id, &itemID, &value, &currency, &eligible, &complete, &fresh, &synced, &accountType, &subtype); err != nil {
			accountRows.Close()
			return p, err
		}
		cents, known := exactCents(value)
		checking := accountType == "BANK" && subtype == "CHECKING_ACCOUNT"
		material = append(material, []any{id, currency, cents, known, accountType, subtype, eligible, complete, fresh})
		if !eligible {
			withheld++
			continue
		}
		sources[itemID] = true
		if synced != nil && (p.LastSyncedAt == nil || synced.After(*p.LastSyncedAt)) {
			p.LastSyncedAt = synced
		}
		if !complete {
			incomplete++
		}
		if !fresh {
			stale++
		}
		if !checking {
			continue
		}
		if !known || !validCurrency(currency) {
			unknown++
			continue
		}
		total := cash[currency]
		total.Currency = currency
		total.AmountCents += cents
		total.Count++
		cash[currency] = total
	}
	err = accountRows.Err()
	accountRows.Close()
	if err != nil {
		return p, err
	}
	p.SourceCount = len(sources)
	p.Cash = currencyTotals(cash)
	add := func(code, title, detail, url string) {
		p.PendingItems = append(p.PendingItems, ActionItem{code, title, detail, url})
	}
	if p.SourceCount == 0 {
		add("bank_data", "Conecte uma conta atualizada", "Autorize uma instituição para consultar saldos e gastos.", "/dashboard#connections-panel")
	}
	if withheld > 0 {
		add("bank_consent", "Revise as autorizações bancárias", "Há contas sem consentimento válido. O saldo disponível ficará incompleto até a regularização.", "/dashboard#connections-panel")
	}
	if incomplete > 0 {
		add("bank_coverage", "Confira a sincronização das contas", "Alguma fonte não confirmou todos os produtos informados.", "/dashboard#connections-panel")
	}
	if stale > 0 {
		add("bank_stale", "Atualize os dados bancários", "Há dados sem atualização há mais de 36 horas.", "/dashboard#connections-panel")
	}
	if unknown > 0 {
		add("bank_balance", "Confira os saldos não informados", "Alguma conta não forneceu saldo ou moeda válidos.", "/dashboard#connections-panel")
	}
	if p.ReviewCount > 0 {
		add("classification", "Revise os lançamentos sem identificação", "Confirme a categoria e a natureza das movimentações sinalizadas.", "/expenses?source=bank&review=1")
	}
	if p.SourceCount > 0 {
		p.State = "available"
	}
	if incomplete+withheld+unknown > 0 {
		p.State = "partial"
	} else if stale > 0 {
		p.State = "stale"
	}
	brl, hasBRL := cash["BRL"]
	if p.SourceCount > 0 && !hasBRL {
		add("checking_balance", "Informe uma conta corrente em reais", "O valor para investir depende do saldo de uma conta corrente BRL.", "/dashboard#connections-panel")
	}
	if p.ProfileComplete && hasBRL && p.State == "available" {
		available := availableCash(profile, brl.AmountCents)
		p.AvailableToInvestCents = &available
		p.CanRecommend = true
	}
	// Classified revisions, source validity and the full ledger are included;
	// ingest timestamps and provider versions have no financial meaning here.
	material = append(material, p.State, p.ProfileComplete)
	encoded, err := json.Marshal(material)
	if err != nil {
		return p, err
	}
	hash := sha256.Sum256(encoded)
	p.Revision = hex.EncodeToString(hash[:])
	return p, nil
}
