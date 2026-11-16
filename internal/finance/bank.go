package finance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type BankTransaction struct {
	ID                   string `json:"id"`
	Date                 string `json:"date"`
	Description          string `json:"description"`
	Category             string `json:"category"`
	CategoryName         string `json:"category_name"`
	Account              string `json:"account"`
	Kind                 string `json:"kind"`
	Status               string `json:"status"`
	AmountCents          int64  `json:"amount_cents"`
	Currency             string `json:"currency"`
	Source               string `json:"source"`
	ClassificationSource string `json:"classification_source"`
	ReviewRequired       bool   `json:"review_required"`
	Version              int    `json:"version"`
	Recurring            bool   `json:"recurring"`
	AmountKnown          bool   `json:"amount_known"`
}

type ClassificationInput struct {
	TransactionID string `json:"transaction_id"`
	Category      string `json:"category"`
	Kind          string `json:"kind"`
	ApplyRule     bool   `json:"apply_rule"`
	Version       int    `json:"version"`
}

func exactCents(value string) (int64, bool) {
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, false
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, false
	}
	n := r.Num().Int64()
	return n, n >= -maxCents && n <= maxCents
}

var bankAccents = strings.NewReplacer("á", "a", "à", "a", "ã", "a", "â", "a", "é", "e", "ê", "e", "í", "i", "ó", "o", "ô", "o", "õ", "o", "ú", "u", "ç", "c")

func normalizeBank(value string) string {
	return strings.Join(strings.Fields(bankAccents.Replace(strings.ToLower(value))), " ")
}

func bankKind(category, direction, operation string) string {
	c := normalizeBank(category)
	if operation == "PAGAMENTO_FATURA" || strings.Contains(c, "credit card payment") || strings.Contains(c, "pagamento de fatura") {
		return "card_bill_payment"
	}
	if strings.Contains(c, "same person") || strings.Contains(c, "mesma titularidade") || strings.Contains(c, "contas proprias") {
		return "transfer_internal"
	}
	if strings.Contains(c, "refund") || strings.Contains(c, "estorno") || operation == "ESTORNO" {
		return "refund"
	}
	if strings.Contains(c, "dividend") || strings.Contains(c, "proceeds") || strings.Contains(c, "rendimento") {
		return "income"
	}
	if oneOf(c, "fixed income", "mutual funds", "variable income", "automatic investment", "margin", "pension", "investments", "renda fixa", "fundos de investimento", "renda variavel", "investimento automatico", "margem", "previdencia", "investimentos") {
		return "investment"
	}
	if strings.Contains(c, "transfer") || strings.Contains(c, "loan") || strings.Contains(c, "emprestimo") {
		return "unknown"
	}
	if c != "" && direction == "DEBIT" {
		return "expense"
	}
	if direction == "CREDIT" && oneOf(c, "salary", "income", "retirement", "entrepreneurial activities", "government aid", "non-recurring income", "salario", "receita", "aposentadoria", "renda", "cashback") {
		return "income"
	}
	return "unknown"
}

var bankLabels = map[string]string{"groceries": "Supermercado", "restaurants": "Restaurantes", "eating out": "Restaurantes", "food delivery": "Delivery", "gas stations": "Combustível", "salary": "Salário", "credit card payment": "Pagamento de fatura", "fixed income": "Renda fixa", "mutual funds": "Fundos de investimento", "variable income": "Renda variável", "automatic investment": "Aplicação automática", "margin": "Margem de garantia", "pension": "Previdência", "income": "Receitas", "health": "Saúde", "education": "Educação", "online courses": "Cursos", "internet": "Internet", "mobile": "Telefone", "video streaming": "Streaming de vídeo", "music streaming": "Streaming de música", "shopping": "Compras", "online shopping": "Compras online", "clothing": "Vestuário", "travel": "Viagens", "rent": "Aluguel", "water": "Água", "electricity": "Energia", "refund": "Estorno"}

func categoryLabel(value string) string {
	if label := bankLabels[normalizeBank(value)]; label != "" {
		return label
	}
	if value == "" {
		return "Sem categoria"
	}
	return value
}

func bankCategoryID(label string) string {
	sum := sha256.Sum256([]byte(normalizeBank(label)))
	return "bank-" + hex.EncodeToString(sum[:8])
}

// All rows are read before aggregating; provider pagination never truncates a report.
func ReadBankTransactions(ctx context.Context, db Queryer, userID string, at time.Time) ([]BankTransaction, error) {
	out := []BankTransaction{}
	if !uuidPattern.MatchString(userID) {
		return out, invalid("user_id")
	}
	rows, err := db.Query(ctx, `SELECT t.external_id,coalesce(t.effective_at,''),left(coalesce(t.description,''),200),
 coalesce(t.amount::text,''),coalesce(t.currency,''),coalesce(t.provider_status,''),
 left(coalesce(t.payload->>'category',''),60),left(coalesce(t.payload->>'type',''),10),left(coalesce(t.payload->>'operationType',''),64),
 left(coalesce(nullif(a.name,''),'Conta importada'),80),
 coalesce(c.category,r.category,''),coalesce(c.kind,r.kind,''),coalesce(c.version,0),
 CASE WHEN c.transaction_id IS NOT NULL THEN 'user' WHEN r.match_key IS NOT NULL THEN 'rule' ELSE 'pluggy' END
 FROM pluggy_transactions t JOIN pluggy_items i ON i.user_id=t.user_id AND i.item_id=t.item_id
 LEFT JOIN pluggy_accounts a ON a.user_id=t.user_id AND a.external_id=t.account_external_id
 LEFT JOIN finance_classifications c ON c.user_id=t.user_id AND c.transaction_id=t.external_id
 LEFT JOIN finance_classification_rules r ON r.user_id=t.user_id AND r.match_key=coalesce(t.payload->>'type','')||':'||regexp_replace(lower(btrim(coalesce(t.description,''))), '\s+', ' ', 'g')
 WHERE t.user_id=$1 AND t.active AND i.explicit_owner AND i.superseded_by IS NULL
 AND i.status NOT IN ('revoked','expired') AND (i.consent_expires_at IS NULL OR i.consent_expires_at>$2)
 ORDER BY t.effective_at DESC,t.external_id`, userID, at)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var t BankTransaction
		var rawAmount, providerStatus, category, direction, operation, override, kind string
		if err = rows.Scan(&t.ID, &t.Date, &t.Description, &rawAmount, &t.Currency, &providerStatus, &category, &direction, &operation, &t.Account, &override, &kind, &t.Version, &t.ClassificationSource); err != nil {
			return out, err
		}
		if parsed, e := time.Parse(time.RFC3339, t.Date); e == nil {
			t.Date = parsed.In(loc).Format("2006-01-02")
		}
		if _, e := time.Parse("2006-01-02", t.Date); e != nil {
			t.Date = ""
		}
		t.AmountCents, t.AmountKnown = exactCents(rawAmount)
		if t.AmountCents < 0 {
			t.AmountCents = -t.AmountCents
		}
		t.Kind = bankKind(category, direction, operation)
		t.CategoryName = categoryLabel(strings.TrimSpace(category))
		if override != "" {
			t.CategoryName = override
			t.Kind = kind
		}
		if t.Kind == "refund" {
			t.AmountCents = -t.AmountCents
		}
		if providerStatus == "POSTED" {
			t.Status = "confirmed"
		} else {
			t.Status = "pending"
		}
		t.Source = "pluggy"
		t.Category = bankCategoryID(t.CategoryName)
		t.ReviewRequired = t.Kind == "unknown" || !t.AmountKnown || t.Date == "" || !validCurrency(t.Currency)
		out = append(out, t)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	months := map[string]map[string]bool{}
	for _, t := range out {
		if t.Kind != "expense" || t.Status != "confirmed" || len(t.Date) < 7 {
			continue
		}
		key := recurringKey(t)
		if months[key] == nil {
			months[key] = map[string]bool{}
		}
		months[key][t.Date[:7]] = true
	}
	for i := range out {
		out[i].Recurring = len(months[recurringKey(out[i])]) >= 3
	}
	return out, nil
}

func validCurrency(v string) bool {
	if len(v) != 3 {
		return false
	}
	for _, c := range v {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}
func recurringKey(t BankTransaction) string {
	raw, _ := json.Marshal([]any{normalizeBank(t.Description), t.Currency, t.AmountCents})
	return string(raw)
}

func (s *Store) SaveClassification(ctx context.Context, userID string, in ClassificationInput) error {
	in.Category = strings.TrimSpace(in.Category)
	if !uuidPattern.MatchString(userID) || !validPersonalText(in.TransactionID, 1, 240) || !validPersonalText(in.Category, 1, 60) || in.Version < 0 || !oneOf(in.Kind, "expense", "income", "transfer_internal", "investment", "card_bill_payment", "refund", "unknown") {
		return invalid("classification")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var key string
	err = tx.QueryRow(ctx, `SELECT coalesce(payload->>'type','')||':'||regexp_replace(lower(btrim(coalesce(description,''))), '\s+', ' ', 'g') FROM pluggy_transactions WHERE user_id=$1 AND external_id=$2 FOR SHARE`, userID, in.TransactionID).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if in.ApplyRule && !validPersonalText(key, 1, 240) {
		return invalid("rule")
	}
	var version int
	if in.Version == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO finance_classifications(user_id,transaction_id,category,kind,version) VALUES($1,$2,$3,$4,1) ON CONFLICT DO NOTHING RETURNING version`, userID, in.TransactionID, in.Category, in.Kind).Scan(&version)
	} else {
		err = tx.QueryRow(ctx, `UPDATE finance_classifications SET category=$3,kind=$4,version=version+1,updated_at=clock_timestamp() WHERE user_id=$1 AND transaction_id=$2 AND version=$5 RETURNING version`, userID, in.TransactionID, in.Category, in.Kind, in.Version).Scan(&version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if in.ApplyRule {
		_, err = tx.Exec(ctx, `INSERT INTO finance_classification_rules(user_id,match_key,category,kind) VALUES($1,$2,$3,$4) ON CONFLICT(user_id,match_key) DO UPDATE SET category=EXCLUDED.category,kind=EXCLUDED.kind,version=finance_classification_rules.version+1,updated_at=clock_timestamp()`, userID, key, in.Category, in.Kind)
		if err != nil {
			return err
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO finance_personal_audit(user_id,entity,entity_id,version,payload) VALUES($1,'classification',$2,$3,$4)`, userID, in.TransactionID, version, raw); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type CurrencyTotal struct {
	Currency    string `json:"currency"`
	AmountCents int64  `json:"amount_cents"`
	Count       int    `json:"count"`
}
type CategoryTotal struct {
	Category     string `json:"category"`
	CategoryName string `json:"category_name"`
	CurrencyTotal
}

func currencyTotals(totals map[string]CurrencyTotal) []CurrencyTotal {
	result := make([]CurrencyTotal, 0, len(totals))
	for _, v := range totals {
		result = append(result, v)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Currency < result[j].Currency })
	return result
}
