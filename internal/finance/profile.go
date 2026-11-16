package finance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type Queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Goal struct {
	Name        string `json:"name"`
	TargetCents int64  `json:"target_cents"`
	TargetDate  string `json:"target_date"`
}

type Profile struct {
	Version                int        `json:"version"`
	Confirmed              bool       `json:"confirmed"`
	Goals                  []Goal     `json:"goals"`
	ReserveCurrentCents    *int64     `json:"reserve_current_cents"`
	ReserveTargetCents     *int64     `json:"reserve_target_cents"`
	Commitments30DaysCents *int64     `json:"commitments_30_days_cents"`
	MinimumCheckingCents   *int64     `json:"minimum_checking_cents"`
	ProtectedCheckingCents *int64     `json:"protected_checking_cents"`
	RiskProfile            string     `json:"risk_profile"`
	Experience             string     `json:"experience"`
	LiquidityDays          *int       `json:"liquidity_days"`
	Restrictions           string     `json:"restrictions"`
	UpdatedAt              *time.Time `json:"updated_at"`
	ConfirmedAt            *time.Time `json:"confirmed_at"`
}

type ActionItem struct {
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	URL    string `json:"url"`
}

func ReadProfile(ctx context.Context, db Queryer, userID string) (Profile, error) {
	p := Profile{Goals: []Goal{}}
	if !uuidPattern.MatchString(userID) {
		return p, invalid("user_id")
	}
	var raw []byte
	var version int
	var updated time.Time
	var confirmed *time.Time
	err := db.QueryRow(ctx, `SELECT data,version,updated_at,confirmed_at FROM finance_profile WHERE user_id=$1`, userID).Scan(&raw, &version, &updated, &confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	p.Version = version
	p.UpdatedAt = &updated
	p.ConfirmedAt = confirmed
	return p, nil
}

func validateProfile(p *Profile) error {
	if p.Version < 0 || len(p.Goals) > 20 {
		return invalid("profile")
	}
	if p.Goals == nil {
		p.Goals = []Goal{}
	}
	for i := range p.Goals {
		g := &p.Goals[i]
		g.Name = strings.TrimSpace(g.Name)
		date, err := time.Parse("2006-01-02", g.TargetDate)
		if !validPersonalText(g.Name, 1, 100) || g.TargetCents <= 0 || g.TargetCents > maxCents || err != nil || date.Year() < 1900 || date.Year() > 2100 {
			return invalid("goals")
		}
	}
	for _, v := range []*int64{p.ReserveCurrentCents, p.ReserveTargetCents, p.Commitments30DaysCents, p.MinimumCheckingCents, p.ProtectedCheckingCents} {
		if v != nil && (*v < 0 || *v > maxCents) {
			return invalid("amount_cents")
		}
	}
	if !oneOf(p.RiskProfile, "", "conservative", "moderate", "aggressive") {
		return invalid("risk_profile")
	}
	if !oneOf(p.Experience, "", "beginner", "intermediate", "experienced") {
		return invalid("experience")
	}
	if p.LiquidityDays != nil && (*p.LiquidityDays < 0 || *p.LiquidityDays > 3650) {
		return invalid("liquidity_days")
	}
	p.Restrictions = strings.TrimSpace(p.Restrictions)
	if !validPersonalText(p.Restrictions, 0, 1000) {
		return invalid("restrictions")
	}
	if p.Confirmed && len(profileIssues(*p)) > 0 {
		return invalid("profile_incomplete")
	}
	return nil
}

func validPersonalText(v string, min, max int) bool {
	return utf8.ValidString(v) && !strings.ContainsRune(v, 0) && utf8.RuneCountInString(v) >= min && utf8.RuneCountInString(v) <= max
}
func oneOf(v string, values ...string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Store) SaveProfile(ctx context.Context, userID string, p Profile) (Profile, error) {
	if !uuidPattern.MatchString(userID) {
		return Profile{}, invalid("user_id")
	}
	if err := validateProfile(&p); err != nil {
		return Profile{}, err
	}
	p.UpdatedAt = nil
	p.ConfirmedAt = nil
	raw, err := json.Marshal(p)
	if err != nil {
		return Profile{}, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback(ctx)
	var version int
	if p.Version == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO finance_profile(user_id,version,data,confirmed_at) VALUES($1,1,$2,CASE WHEN $3 THEN clock_timestamp() END) ON CONFLICT DO NOTHING RETURNING version`, userID, raw, p.Confirmed).Scan(&version)
	} else {
		err = tx.QueryRow(ctx, `UPDATE finance_profile SET version=version+1,data=$2,confirmed_at=CASE WHEN $4 THEN clock_timestamp() END,updated_at=clock_timestamp() WHERE user_id=$1 AND version=$3 RETURNING version`, userID, raw, p.Version, p.Confirmed).Scan(&version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrConflict
	}
	if err != nil {
		return Profile{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO finance_personal_audit(user_id,entity,entity_id,version,payload) VALUES($1,'profile',($1::uuid)::text,$2,$3)`, userID, version, raw); err != nil {
		return Profile{}, err
	}
	result, err := ReadProfile(ctx, tx, userID)
	if err != nil {
		return Profile{}, err
	}
	return result, tx.Commit(ctx)
}

func profileIssues(p Profile) []ActionItem {
	issues := []ActionItem{}
	add := func(code, title, detail string) {
		issues = append(issues, ActionItem{code, title, detail, "/finance/profile#" + code})
	}
	if len(p.Goals) == 0 {
		add("goals", "Defina seus objetivos", "Informe o valor e o prazo de pelo menos um objetivo.")
	}
	for _, v := range []struct {
		key, title, detail string
		value              *int64
	}{
		{"reserve_current_cents", "Informe sua reserva atual", "Considere a reserva que já está aplicada, sem contar o saldo protegido da conta duas vezes.", p.ReserveCurrentCents},
		{"reserve_target_cents", "Defina a reserva desejada", "Informe quanto deseja manter para emergências.", p.ReserveTargetCents},
		{"commitments_30_days_cents", "Informe os compromissos dos próximos 30 dias", "Inclua faturas e contas a pagar. Confirme zero se não houver compromissos.", p.Commitments30DaysCents},
		{"minimum_checking_cents", "Defina o mínimo na conta corrente", "Informe quanto precisa manter disponível para o dia a dia.", p.MinimumCheckingCents},
		{"protected_checking_cents", "Informe os valores protegidos na conta", "Separe o saldo destinado a reservas ou objetivos que não pode ser usado neste aporte.", p.ProtectedCheckingCents},
	} {
		if v.value == nil {
			add(v.key, v.title, v.detail)
		}
	}
	if p.RiskProfile == "" {
		add("risk_profile", "Confirme seu perfil de risco", "Escolha o nível de risco que aceita para as sugestões.")
	}
	if p.Experience == "" {
		add("experience", "Informe sua experiência", "Isso ajuda a explicar os produtos e limitar estratégias complexas.")
	}
	if p.LiquidityDays == nil {
		add("liquidity_days", "Informe quando pode precisar do dinheiro", "O prazo de resgate precisa atender à sua necessidade de liquidez.")
	}
	if !p.Confirmed {
		add("confirmed", "Confirme seus dados financeiros", "Revise os campos e confirme as informações para personalizar as recomendações.")
	}
	return issues
}

func availableCash(p Profile, cash int64) int64 {
	for _, v := range []*int64{p.Commitments30DaysCents, p.MinimumCheckingCents, p.ProtectedCheckingCents} {
		if v == nil {
			return 0
		}
		cash -= *v
	}
	if cash < 0 {
		return 0
	}
	return cash
}
