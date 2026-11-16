package pluggy

import (
	"context"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/memory"
)

// Wealth is the BRL subtotal of authorized, identifiable positions.
type Wealth struct {
	Net          string    `json:"net"`
	Cash         string    `json:"cash"`
	Investments  string    `json:"investments"`
	Debt         string    `json:"debt"`
	BankAsOf     time.Time `json:"bank_as_of,omitzero"`
	LastImported time.Time `json:"last_imported,omitzero"`
	Issues       []string  `json:"issues"`
}

type wealthLoan struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Currency string `json:"currency"`
	Balance  string `json:"balance"`
}

func (s *Service) readWealthLoans(ctx context.Context, itemID string, item Object) []wealthLoan {
	products, _ := item["products"].([]any)
	authorized := false
	for _, product := range products {
		authorized = authorized || product == "LOANS"
	}
	if !authorized {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	rows, err := s.client.Loans(ctx, itemID)
	if err != nil {
		return nil
	}
	loans := make([]wealthLoan, 0, len(rows))
	for _, row := range rows {
		id := textField(row, "id")
		if !uuidPattern.MatchString(id) || (row["itemId"] != nil && textField(row, "itemId") != itemID) {
			return nil
		}
		key := "id:" + id
		if ipoc := strings.TrimSpace(textField(row, "ipocCode")); ipoc != "" && len(ipoc) <= 120 {
			key = "ipoc:" + ipoc
		}
		payments, _ := row["payments"].(map[string]any)
		amount, _ := decimal(payments, "contractOutstandingBalance")
		balance, _ := amount.(string)
		loans = append(loans, wealthLoan{contentHash([]byte(key)), textField(row, "kind"), textField(row, "currencyCode"), balance})
	}
	return loans
}

type wealthItem struct {
	id       string
	status   string
	imported *time.Time
	source   *time.Time
	eligible bool
	complete bool
	invests  bool
	cards    bool
	loans    []wealthLoan
}

type wealthPosition struct {
	item, kind, subtype, currency, balance, transfer, holder, auto, custodian, number, asset string
	investment                                                                               bool
}

func (s *Service) Wealth(ctx context.Context) (Wealth, error) {
	if err := s.storage(); err != nil {
		return Wealth{}, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Wealth{}, err
	}
	defer tx.Rollback(ctx)
	wealth, err := readWealth(ctx, tx, time.Now().UTC())
	if err != nil {
		return Wealth{}, err
	}
	return wealth, tx.Commit(ctx)
}

func readWealth(ctx context.Context, db queryer, at time.Time) (Wealth, error) {
	rows, err := db.Query(ctx, `SELECT item_id::text,status,last_synced_at,last_source_sync,
   last_synced_at IS NOT NULL AND status NOT IN ('expired','revoked','user_action_required') AND
   (consent_expires_at IS NULL OR consent_expires_at>$2),
   coalesce(coverage->>'complete_for_reported_products'='true',false),
   coalesce(coverage->'source_products' ? 'INVESTMENTS',false),
   coalesce(coverage->'source_products' ? 'CREDIT_CARDS',false),wealth_loans
 FROM pluggy_items WHERE user_id=$1 AND explicit_owner AND superseded_by IS NULL
 ORDER BY least(last_synced_at,last_source_sync) DESC NULLS LAST,item_id`, memory.UserID, at)
	if err != nil {
		return Wealth{}, err
	}
	defer rows.Close()
	var items []wealthItem
	eligible := []string{}
	for rows.Next() {
		var i wealthItem
		if err := rows.Scan(&i.id, &i.status, &i.imported, &i.source, &i.eligible, &i.complete, &i.invests, &i.cards, &i.loans); err != nil {
			return Wealth{}, err
		}
		items = append(items, i)
		if i.eligible {
			eligible = append(eligible, i.id)
		}
	}
	if err := rows.Err(); err != nil {
		return Wealth{}, err
	}
	positions, err := db.Query(ctx, `SELECT item_id::text,coalesce(account_type,''),coalesce(subtype,''),coalesce(currency,''),coalesce(balance::text,''),
   coalesce(payload#>>'{bankData,transferNumber}',''),coalesce(payload#>>'{creditData,holderType}',''),
   coalesce(payload#>>'{bankData,automaticallyInvestedBalance}',''),'','','',false,source_updated_at,external_id
 FROM pluggy_accounts WHERE user_id=$1 AND item_id=ANY($2::text[]::uuid[]) AND active
 UNION ALL
 SELECT item_id::text,coalesce(payload->>'status',''),'',coalesce(currency,''),coalesce(balance::text,''),
   '','','',coalesce(payload#>>'{institution,number}',''),coalesce(payload->>'number',''),
   coalesce(nullif(payload->>'isin',''),payload->>'code',''),true,source_updated_at,external_id
 FROM pluggy_investments WHERE user_id=$1 AND item_id=ANY($2::text[]::uuid[]) AND active
 AND coalesce(payload->>'status','')<>'TOTAL_WITHDRAWAL'
 ORDER BY source_updated_at DESC NULLS LAST,external_id`, memory.UserID, eligible)
	if err != nil {
		return Wealth{}, err
	}
	defer positions.Close()
	byItem := map[string][]wealthPosition{}
	for positions.Next() {
		var p wealthPosition
		if err := positions.Scan(&p.item, &p.kind, &p.subtype, &p.currency, &p.balance, &p.transfer, &p.holder, &p.auto, &p.custodian, &p.number, &p.asset, &p.investment, nil, nil); err != nil {
			return Wealth{}, err
		}
		byItem[p.item] = append(byItem[p.item], p)
	}
	if err := positions.Err(); err != nil {
		return Wealth{}, err
	}
	return calculateWealth(items, byItem, at), nil
}

var bankTransferNumber = regexp.MustCompile(`^[0-9]{3}/[0-9]+/[0-9]+[0-9/-]*$`)

func wealthAmount(value string) (*big.Rat, bool) {
	if len(value) > 180 || !decimalPattern.MatchString(value) || strings.ContainsAny(value, "eE") {
		return nil, false
	}
	return new(big.Rat).SetString(value)
}

func wealthMoney(value *big.Rat) string {
	if value == nil {
		return ""
	}
	number := value.RatString()
	return investmentMoney(&number, "BRL")
}

func calculateWealth(items []wealthItem, positions map[string][]wealthPosition, at time.Time) Wealth {
	var result Wealth
	var cash, investments, debt *big.Rat
	add := func(total **big.Rat, value *big.Rat) {
		if *total == nil {
			*total = new(big.Rat)
		}
		(*total).Add(*total, value)
	}
	var blocked, incomplete, old, undated, missing, foreign, overlap, unknownDebt bool
	accounts, securities, loanKeys := map[string]bool{}, map[string]string{}, map[string]bool{}
	directAssets := map[string]bool{}
	directWithoutAsset := false
	for _, list := range positions {
		for _, p := range list {
			if p.investment && p.custodian == "" && p.kind == "ACTIVE" && p.currency == "BRL" {
				if p.asset == "" {
					directWithoutAsset = true
				} else {
					directAssets[p.asset] = true
				}
			}
		}
	}
	for _, i := range items {
		if !i.eligible {
			blocked = true
			continue
		}
		imported := *i.imported
		if result.LastImported.Before(imported) {
			result.LastImported = imported
		}
		asOf := imported
		if i.source == nil {
			undated = true
		} else {
			if result.BankAsOf.IsZero() || i.source.Before(result.BankAsOf) {
				result.BankAsOf = *i.source
			}
			if i.source.Before(asOf) {
				asOf = *i.source
			}
		}
		old = old || at.Sub(asOf) > 36*time.Hour || asOf.After(at)
		hasInvestment, hasCard, hasOverdraft, additionalCard := false, false, false, false
		for _, p := range positions[i.id] {
			hasInvestment = hasInvestment || p.investment
			if p.kind == "CREDIT" && p.holder == "ADDITIONAL" {
				additionalCard = true
				continue
			}
			hasCard = hasCard || p.kind == "CREDIT"
			if p.investment && p.kind != "ACTIVE" {
				missing = true
				continue
			}
			if p.currency != "BRL" {
				if len(p.currency) == 3 {
					foreign = true
				} else {
					missing = true
				}
				continue
			}
			value, ok := wealthAmount(p.balance)
			if !ok {
				missing = true
				continue
			}
			switch {
			case p.investment:
				// shortcut: consolidated positions can overlap direct custody without a shared position ID; include them when the asset is distinct.
				if p.custodian != "" && (directWithoutAsset || directAssets[p.asset] || (p.asset == "" && len(directAssets) > 0)) {
					overlap = true
					continue
				}
				if p.custodian != "" && p.number != "" && p.asset != "" {
					key := p.custodian + ":" + p.number + ":" + p.asset
					if item := securities[key]; item != "" && item != i.id {
						overlap = true
						continue
					}
					securities[key] = i.id
				}
				add(&investments, value)
			case p.kind == "BANK":
				if bankTransferNumber.MatchString(p.transfer) {
					key := p.transfer + ":" + p.subtype
					if accounts[key] {
						continue
					}
					accounts[key] = true
				}
				if auto, valid := wealthAmount(p.auto); valid && auto.Sign() > 0 && value.Cmp(auto) != 0 {
					// An automatic investment can also be present in the investments product.
					overlap = true
				}
				if value.Sign() < 0 {
					hasOverdraft = true
					add(&debt, value.Neg(value))
				} else {
					add(&cash, value)
				}
			case p.kind == "CREDIT":
				add(&debt, value)
			default:
				missing = true
			}
		}
		incomplete = incomplete || !i.complete || (i.status != "connected" && i.status != "syncing") || (!i.invests && !hasInvestment) || (!i.cards && !hasCard)
		unknownDebt = unknownDebt || i.loans == nil || (additionalCard && !hasCard)
		for _, loan := range i.loans {
			value, ok := wealthAmount(loan.Balance)
			if !ok || value.Sign() < 0 {
				unknownDebt = true
				continue
			}
			if loan.Currency != "BRL" {
				foreign = foreign || len(loan.Currency) == 3
				unknownDebt = unknownDebt || len(loan.Currency) != 3
				continue
			}
			if (loan.Kind == "INVOICE_FINANCING" && hasCard) || (loan.Kind == "UNARRANGED_ACCOUNT_OVERDRAFT" && hasOverdraft) {
				overlap = overlap || value.Sign() != 0
				continue
			}
			switch loan.Kind {
			case "LOAN", "FINANCING", "INVOICE_FINANCING", "UNARRANGED_ACCOUNT_OVERDRAFT":
				if !loanKeys[loan.Key] {
					loanKeys[loan.Key] = true
					add(&debt, value)
				}
			default:
				unknownDebt = true
			}
		}
	}
	net := new(big.Rat)
	for _, amount := range []*big.Rat{cash, investments} {
		if amount != nil {
			net.Add(net, amount)
		}
	}
	if debt != nil {
		net.Sub(net, debt)
	}
	if cash != nil || investments != nil || debt != nil {
		result.Net = wealthMoney(net)
	}
	result.Cash, result.Investments, result.Debt = wealthMoney(cash), wealthMoney(investments), wealthMoney(debt)
	for _, issue := range []struct {
		applies bool
		text    string
	}{
		{len(items) == 0, "Conecte uma instituição para calcular."},
		{len(items) > 0 && result.Net == "", "Nenhum saldo em reais pôde ser confirmado para o cálculo."},
		{blocked, "Há conexões sem dados válidos. Confira a autorização e a sincronização."},
		{old, "Há dados bancários desatualizados."},
		{undated, "Algumas instituições não informaram a data da atualização."},
		{incomplete, "A cobertura de contas, cartões ou investimentos está incompleta."},
		{missing, "Há posições sem saldo, moeda ou situação confirmada."},
		{unknownDebt, "Parte das dívidas não teve cobertura ou saldo confirmado."},
		{foreign, "Valores em outras moedas ficam fora deste subtotal em reais."},
		{overlap, "Valores com possível sobreposição ficaram fora para evitar duplicidade."},
	} {
		if issue.applies {
			result.Issues = append(result.Issues, issue.text)
		}
	}
	return result
}
