package pluggy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/memory"
)

// Portfolio is a read-only projection, independent of manually entered wealth.
// ActiveCount is the provider's ACTIVE status, not the snapshot-presence flag.
type Portfolio struct {
	State             string               `json:"state"`
	SourceID          string               `json:"source_id"`
	Revision          string               `json:"revision"`
	SourceCount       int                  `json:"source_count"`
	ActiveCount       int                  `json:"active_count"`
	WithdrawnCount    int                  `json:"withdrawn_count"`
	UnknownCount      int                  `json:"unknown_count"`
	MissingValueCount int                  `json:"missing_value_count"`
	WithheldCount     int                  `json:"withheld_count"`
	Omitted           int                  `json:"omitted"`
	Partial           bool                 `json:"partial"`
	Stale             bool                 `json:"stale"`
	LastSyncedAt      *time.Time           `json:"last_synced_at"`
	OldestSyncedAt    *time.Time           `json:"oldest_synced_at"`
	Totals            []InvestmentTotal    `json:"totals"`
	Groups            []InvestmentGroup    `json:"groups"`
	Positions         []InvestmentPosition `json:"positions"`
}

type InvestmentTotal struct {
	Currency    string  `json:"currency"`
	Balance     *string `json:"balance"`
	BalanceText string  `json:"balance_text"`
	Count       int     `json:"count"`
	ValuedCount int     `json:"valued_count"`
}

type InvestmentGroup struct {
	Type string `json:"type"`
	InvestmentTotal
}

type InvestmentPosition struct {
	Name            string     `json:"name"`
	Type            string     `json:"type"`
	Status          string     `json:"status"`
	Currency        string     `json:"currency"`
	Balance         *string    `json:"balance"`
	BalanceText     string     `json:"balance_text"`
	Issuer          string     `json:"issuer,omitempty"`
	Maturity        string     `json:"maturity,omitempty"`
	SourceUpdatedAt *time.Time `json:"source_updated_at"`
	ObservedAt      time.Time  `json:"observed_at"`
}

func EmptyPortfolio(state string) Portfolio {
	return Portfolio{State: state, Totals: []InvestmentTotal{}, Groups: []InvestmentGroup{}, Positions: []InvestmentPosition{}}
}

func (s *Service) Portfolio(ctx context.Context) (Portfolio, error) {
	if s == nil || !s.cfg.Enabled {
		return EmptyPortfolio("disabled"), nil
	}
	if err := s.storage(); err != nil {
		return EmptyPortfolio("unavailable"), err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return EmptyPortfolio("unavailable"), err
	}
	defer tx.Rollback(ctx)
	p, err := ReadPortfolio(ctx, tx, time.Now().UTC())
	if err != nil {
		return EmptyPortfolio("unavailable"), err
	}
	return p, tx.Commit(ctx)
}

// One SQL statement computes complete aggregates before bounding the details.
// It never loads a complete provider payload or calls the provider.
func ReadPortfolio(ctx context.Context, db queryer, at time.Time) (Portfolio, error) {
	p := EmptyPortfolio("unavailable")
	var raw []byte
	var material string
	err := db.QueryRow(ctx, `WITH sources AS (
	 SELECT i.item_id,i.status,i.explicit_owner,i.superseded_by,i.consent_expires_at,least(i.last_synced_at,i.last_source_sync) AS last_synced_at,
	 coalesce((i.coverage->>'complete_for_reported_products')='true',false) AS complete,
	 (i.explicit_owner AND i.superseded_by IS NULL AND i.status NOT IN ('revoked','expired')
	 AND (i.consent_expires_at IS NULL OR i.consent_expires_at>$2::timestamptz)
	 AND i.last_synced_at IS NOT NULL
	 AND (i.coverage->'source_products' ? 'INVESTMENTS' OR EXISTS(SELECT 1 FROM pluggy_investments p WHERE p.user_id=$1 AND p.item_id=i.item_id AND p.active))) AS eligible
	 FROM pluggy_items i WHERE i.user_id=$1
	), positions AS (
	 SELECT p.external_id,coalesce(nullif(left(p.name,160),''),'Investimento sem nome') AS name,
	 coalesce(nullif(left(p.investment_type,64),''),'N/D') AS type,
	 coalesce(nullif(left(p.payload->>'status',40),''),'UNKNOWN') AS status,
	 CASE WHEN p.currency ~ '^[A-Z]{3}$' THEN p.currency ELSE 'N/D' END AS currency,
	 CASE WHEN p.currency ~ '^[A-Z]{3}$' AND p.balance::text NOT IN ('NaN','Infinity','-Infinity') THEN p.balance ELSE NULL END AS balance,
	 left(coalesce(p.payload->>'issuer',''),120) AS issuer,
	 CASE WHEN p.payload->>'dueDate' ~ '^\d{4}-\d{2}-\d{2}' THEN left(p.payload->>'dueDate',10) ELSE '' END AS maturity,
	 p.source_updated_at,p.observed_at
	 FROM pluggy_investments p JOIN sources s ON s.item_id=p.item_id AND s.eligible
	 WHERE p.user_id=$1 AND p.active
	), totals AS (
	 SELECT currency,trim_scale(sum(balance))::text AS balance,count(*) AS count,count(balance) AS valued_count
	 FROM positions WHERE status='ACTIVE' GROUP BY currency
	), groups AS (
	 SELECT type,currency,trim_scale(sum(balance))::text AS balance,count(*) AS count,count(balance) AS valued_count
	 FROM positions WHERE status='ACTIVE' GROUP BY type,currency
	), details AS (
	 SELECT name,type,status,currency,trim_scale(balance)::text AS balance,issuer,maturity,source_updated_at,observed_at
	 FROM positions ORDER BY (status='ACTIVE') DESC,external_id LIMIT 100
	)
	SELECT jsonb_build_object(
	 'source_count',(SELECT count(*) FROM sources WHERE eligible),
	 'active_count',(SELECT count(*) FROM positions WHERE status='ACTIVE'),
	 'withdrawn_count',(SELECT count(*) FROM positions WHERE status='TOTAL_WITHDRAWAL'),
	 'unknown_count',(SELECT count(*) FROM positions WHERE status NOT IN ('ACTIVE','TOTAL_WITHDRAWAL')),
	 'missing_value_count',(SELECT count(*) FROM positions WHERE status='ACTIVE' AND balance IS NULL),
	 'withheld_count',(SELECT count(*) FROM pluggy_investments p JOIN sources s ON s.item_id=p.item_id WHERE p.user_id=$1 AND p.active AND NOT s.eligible),
	 'omitted',greatest((SELECT count(*) FROM positions)-100,0),
	 'partial',coalesce((SELECT bool_or(NOT complete OR status NOT IN ('connected','syncing','stale')) FROM sources WHERE eligible),false),
	 'stale',coalesce((SELECT bool_or(last_synced_at<$2::timestamptz-interval '36 hours') FROM sources WHERE eligible),false),
	 'last_synced_at',(SELECT max(last_synced_at) FROM sources WHERE eligible),
	 'oldest_synced_at',(SELECT min(last_synced_at) FROM sources WHERE eligible),
	 'totals',coalesce((SELECT jsonb_agg(to_jsonb(t) ORDER BY currency) FROM totals t),'[]'::jsonb),
	 'groups',coalesce((SELECT jsonb_agg(to_jsonb(g) ORDER BY type,currency) FROM groups g),'[]'::jsonb),
	 'positions',coalesce((SELECT jsonb_agg(to_jsonb(d)) FROM details d),'[]'::jsonb)
	),coalesce((SELECT jsonb_agg(jsonb_build_object('id',item_id,'eligible',eligible,'complete',complete,
	 'partial',status NOT IN ('connected','syncing','stale'),'stale',last_synced_at<$2::timestamptz-interval '36 hours') ORDER BY item_id)::text FROM sources),'[]')||'|'||
	 coalesce((SELECT jsonb_agg(jsonb_build_object('id',external_id,'item_id',item_id,'active',active,
	 'balance',trim_scale(balance)::text,'currency',currency,'type',investment_type,'name',name,
	 'status',payload->>'status','issuer',payload->>'issuer','dueDate',payload->>'dueDate') ORDER BY external_id)::text
	 FROM pluggy_investments WHERE user_id=$1),'[]')`, memory.UserID, at).Scan(&raw, &material)
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(raw, &p); err != nil {
		return EmptyPortfolio("unavailable"), err
	}
	p.Partial = p.Partial || p.MissingValueCount > 0 || p.UnknownCount > 0 || p.WithheldCount > 0
	switch {
	case p.SourceCount == 0 && p.WithheldCount > 0:
		p.State = "unavailable"
	case p.SourceCount == 0:
		p.State = "awaiting_authorization"
	case p.Partial:
		p.State = "partial"
	case p.Stale:
		p.State = "stale"
	default:
		p.State = "available"
	}
	for i := range p.Totals {
		p.Totals[i].BalanceText = investmentMoney(p.Totals[i].Balance, p.Totals[i].Currency)
	}
	for i := range p.Groups {
		p.Groups[i].BalanceText = investmentMoney(p.Groups[i].Balance, p.Groups[i].Currency)
	}
	for i := range p.Positions {
		p.Positions[i].BalanceText = investmentMoney(p.Positions[i].Balance, p.Positions[i].Currency)
	}
	// State includes time-derived staleness and consent eligibility. The material
	// covers every position, including those outside the bounded detail list.
	sum := sha256.Sum256([]byte(material + "|" + p.State))
	p.Revision = hex.EncodeToString(sum[:])
	p.SourceID = "pluggy:investments:" + p.Revision[:16]
	return p, nil
}

// Round only for presentation. The JSON balance and SQL aggregates stay exact.
func investmentMoney(value *string, currency string) string {
	if value == nil || currency == "N/D" {
		return "N/D"
	}
	r, ok := new(big.Rat).SetString(*value)
	if !ok {
		return "N/D"
	}
	negative := r.Sign() < 0
	r.Abs(r)
	r.Mul(r, big.NewRat(100, 1))
	whole, rem := new(big.Int), new(big.Int)
	whole.QuoRem(r.Num(), r.Denom(), rem)
	if rem.Mul(rem, big.NewInt(2)).Cmp(r.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	digits := whole.String()
	for len(digits) < 3 {
		digits = "0" + digits
	}
	integer, fraction := digits[:len(digits)-2], digits[len(digits)-2:]
	var parts []string
	for len(integer) > 3 {
		parts = append([]string{integer[len(integer)-3:]}, parts...)
		integer = integer[:len(integer)-3]
	}
	parts = append([]string{integer}, parts...)
	prefix := currency
	if currency == "BRL" {
		prefix = "R$"
	} else if currency == "USD" {
		prefix = "US$"
	} else if currency == "EUR" {
		prefix = "€"
	}
	sign := ""
	if negative && whole.Sign() != 0 {
		sign = "-"
	}
	return prefix + " " + sign + strings.Join(parts, ".") + "," + fraction
}
