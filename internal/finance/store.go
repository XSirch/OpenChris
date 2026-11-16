package finance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrValidation = errors.New("invalid financial input")
	ErrConflict   = errors.New("financial data conflict")
	ErrNotFound   = errors.New("financial record not found")
	uuidPattern   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	colorPattern  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	keyPattern    = regexp.MustCompile(`^[!-~]{8,128}$`)
)

const maxCents int64 = 1000000000000

type Store struct{ DB *pgxpool.Pool }

type Category struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Color   string `json:"color"`
	Version int    `json:"version"`
}

type CategoryInput struct {
	Name    string `json:"name"`
	Color   string `json:"color"`
	Version int    `json:"version"`
}

type Transaction struct {
	ID          string `json:"id"`
	Date        string `json:"date"`
	Description string `json:"description"`
	CategoryID  string `json:"category_id"`
	Account     string `json:"account"`
	Status      string `json:"status"`
	AmountCents int64  `json:"amount_cents"`
	Recurring   bool   `json:"recurring"`
}

type ExpenseInput struct {
	Date           string `json:"date"`
	Description    string `json:"description"`
	CategoryID     string `json:"category_id"`
	Account        string `json:"account"`
	Status         string `json:"status"`
	AmountCents    int64  `json:"amount_cents"`
	Recurring      bool   `json:"recurring"`
	IdempotencyKey string `json:"idempotency_key"`
}

type NetWorthInput struct {
	AssetsCents      int64  `json:"assets_cents"`
	LiabilitiesCents int64  `json:"liabilities_cents"`
	Date             string `json:"date"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type NetWorth struct {
	NetWorthInput
	ID        string    `json:"id"`
	NetCents  int64     `json:"net_cents"`
	UpdatedAt time.Time `json:"updated_at"`
}

func New(db *pgxpool.Pool) *Store { return &Store{DB: db} }

func (s *Store) Categories(ctx context.Context, userID string) ([]Category, error) {
	items := []Category{}
	if !uuidPattern.MatchString(userID) {
		return items, invalid("user_id")
	}
	rows, err := s.DB.Query(ctx, `SELECT id::text,name,color,version FROM finance_categories WHERE user_id=$1 ORDER BY lower(name),id`, userID)
	if err != nil {
		return items, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Category
		if err := rows.Scan(&item.ID, &item.Name, &item.Color, &item.Version); err != nil {
			return items, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Transactions(ctx context.Context, userID string) ([]Transaction, error) {
	items := []Transaction{}
	if !uuidPattern.MatchString(userID) {
		return items, invalid("user_id")
	}
	rows, err := s.DB.Query(ctx, `SELECT id::text,transaction_date::text,description,category_id::text,account,status,amount_cents,recurring FROM finance_transactions WHERE user_id=$1 ORDER BY transaction_date DESC,created_at DESC,id`, userID)
	if err != nil {
		return items, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Transaction
		if err := scanTransaction(rows, &item); err != nil {
			return items, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateCategory(ctx context.Context, userID string, in CategoryInput) (Category, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !uuidPattern.MatchString(userID) || !validText(in.Name, 60) || !colorPattern.MatchString(in.Color) {
		return Category{}, invalid("category")
	}
	tx, err := s.beginWrite(ctx)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback(ctx)
	item := Category{ID: newID(), Name: in.Name, Color: strings.ToUpper(in.Color), Version: 1}
	_, err = tx.Exec(ctx, `INSERT INTO finance_categories(id,user_id,name,color) VALUES($1,$2,$3,$4)`, item.ID, userID, item.Name, item.Color)
	if err != nil {
		return Category{}, databaseError(err)
	}
	if err := audit(ctx, tx, userID, "category", item.ID, 1, "created", item); err != nil {
		return Category{}, err
	}
	return item, tx.Commit(ctx)
}

func (s *Store) UpdateCategory(ctx context.Context, userID, id string, in CategoryInput) (Category, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !uuidPattern.MatchString(userID) || !uuidPattern.MatchString(id) || !validText(in.Name, 60) || !colorPattern.MatchString(in.Color) || in.Version < 1 || in.Version >= 2147483647 {
		return Category{}, invalid("category")
	}
	tx, err := s.beginWrite(ctx)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback(ctx)
	var item Category
	err = tx.QueryRow(ctx, `UPDATE finance_categories SET name=$4,color=$5,version=version+1,updated_at=clock_timestamp() WHERE user_id=$1 AND id=$2 AND version=$3 RETURNING id::text,name,color,version`, userID, id, in.Version, in.Name, strings.ToUpper(in.Color)).Scan(&item.ID, &item.Name, &item.Color, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_categories WHERE user_id=$1 AND id=$2)`, userID, id).Scan(&exists); err != nil {
			return Category{}, err
		}
		if exists {
			return Category{}, ErrConflict
		}
		return Category{}, ErrNotFound
	}
	if err != nil {
		return Category{}, databaseError(err)
	}
	if err := audit(ctx, tx, userID, "category", item.ID, item.Version, "updated", item); err != nil {
		return Category{}, err
	}
	return item, tx.Commit(ctx)
}

func (s *Store) CreateExpense(ctx context.Context, userID string, in ExpenseInput) (Transaction, error) {
	in.Description = strings.TrimSpace(in.Description)
	in.Account = strings.TrimSpace(in.Account)
	in.CategoryID = strings.ToLower(in.CategoryID)
	if !uuidPattern.MatchString(userID) || !validDate(in.Date) || !validText(in.Description, 200) || !validText(in.Account, 80) || !uuidPattern.MatchString(in.CategoryID) || (in.Status != "confirmed" && in.Status != "pending") || !validCents(in.AmountCents) || !keyPattern.MatchString(in.IdempotencyKey) {
		return Transaction{}, invalid("expense")
	}
	hash := requestHash(in)
	tx, err := s.beginWrite(ctx)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)
	var categoryExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_categories WHERE user_id=$1 AND id=$2)`, userID, in.CategoryID).Scan(&categoryExists); err != nil {
		return Transaction{}, err
	}
	if !categoryExists {
		return Transaction{}, invalid("category_id")
	}
	var item Transaction
	err = scanTransaction(tx.QueryRow(ctx, `INSERT INTO finance_transactions(id,user_id,transaction_date,description,category_id,account,status,amount_cents,recurring,idempotency_key,request_hash) VALUES($1,$2,$3::date,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(user_id,idempotency_key) DO NOTHING RETURNING id::text,transaction_date::text,description,category_id::text,account,status,amount_cents,recurring`, newID(), userID, in.Date, in.Description, in.CategoryID, in.Account, in.Status, in.AmountCents, in.Recurring, in.IdempotencyKey, hash), &item)
	if errors.Is(err, pgx.ErrNoRows) {
		var originalHash string
		err = tx.QueryRow(ctx, `SELECT id::text,transaction_date::text,description,category_id::text,account,status,amount_cents,recurring,request_hash FROM finance_transactions WHERE user_id=$1 AND idempotency_key=$2`, userID, in.IdempotencyKey).Scan(&item.ID, &item.Date, &item.Description, &item.CategoryID, &item.Account, &item.Status, &item.AmountCents, &item.Recurring, &originalHash)
		if err != nil {
			return Transaction{}, err
		}
		if hash != originalHash {
			return Transaction{}, ErrConflict
		}
		return item, tx.Commit(ctx)
	}
	if err != nil {
		return Transaction{}, databaseError(err)
	}
	if err := audit(ctx, tx, userID, "expense", item.ID, 1, "created", item); err != nil {
		return Transaction{}, err
	}
	return item, tx.Commit(ctx)
}

func (s *Store) SaveNetWorth(ctx context.Context, userID string, in NetWorthInput) (NetWorth, error) {
	if !uuidPattern.MatchString(userID) || !validDate(in.Date) || !validCents(in.AssetsCents) || !validCents(in.LiabilitiesCents) || !keyPattern.MatchString(in.IdempotencyKey) {
		return NetWorth{}, invalid("net_worth")
	}
	hash := requestHash(in)
	tx, err := s.beginWrite(ctx)
	if err != nil {
		return NetWorth{}, err
	}
	defer tx.Rollback(ctx)
	var item NetWorth
	err = scanNetWorth(tx.QueryRow(ctx, `INSERT INTO finance_net_worth(id,user_id,observation_date,assets_cents,liabilities_cents,idempotency_key,request_hash) VALUES($1,$2,$3::date,$4,$5,$6,$7) ON CONFLICT(user_id,idempotency_key) DO NOTHING RETURNING id::text,observation_date::text,assets_cents,liabilities_cents,net_cents,idempotency_key,updated_at`, newID(), userID, in.Date, in.AssetsCents, in.LiabilitiesCents, in.IdempotencyKey, hash), &item)
	if errors.Is(err, pgx.ErrNoRows) {
		var originalHash string
		err = tx.QueryRow(ctx, `SELECT id::text,observation_date::text,assets_cents,liabilities_cents,net_cents,idempotency_key,updated_at,request_hash FROM finance_net_worth WHERE user_id=$1 AND idempotency_key=$2`, userID, in.IdempotencyKey).Scan(&item.ID, &item.Date, &item.AssetsCents, &item.LiabilitiesCents, &item.NetCents, &item.IdempotencyKey, &item.UpdatedAt, &originalHash)
		if err != nil {
			return NetWorth{}, err
		}
		if originalHash != hash {
			return NetWorth{}, ErrConflict
		}
		return item, tx.Commit(ctx)
	}
	if err != nil {
		return NetWorth{}, databaseError(err)
	}
	if err := audit(ctx, tx, userID, "net_worth", item.ID, 1, "created", item); err != nil {
		return NetWorth{}, err
	}
	return item, tx.Commit(ctx)
}

func (s *Store) LatestNetWorth(ctx context.Context, userID string) (*NetWorth, error) {
	if !uuidPattern.MatchString(userID) {
		return nil, invalid("user_id")
	}
	var item NetWorth
	err := scanNetWorth(s.DB.QueryRow(ctx, `SELECT id::text,observation_date::text,assets_cents,liabilities_cents,net_cents,idempotency_key,updated_at FROM finance_net_worth WHERE user_id=$1 ORDER BY observation_date DESC,updated_at DESC,id LIMIT 1`, userID), &item)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

type scanner interface{ Scan(...any) error }

func scanTransaction(row scanner, item *Transaction) error {
	return row.Scan(&item.ID, &item.Date, &item.Description, &item.CategoryID, &item.Account, &item.Status, &item.AmountCents, &item.Recurring)
}

func scanNetWorth(row scanner, item *NetWorth) error {
	return row.Scan(&item.ID, &item.Date, &item.AssetsCents, &item.LiabilitiesCents, &item.NetCents, &item.IdempotencyKey, &item.UpdatedAt)
}

func audit(ctx context.Context, tx pgx.Tx, userID, entityType, entityID string, version int, action string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO finance_audit(user_id,entity_type,entity_id,version,action,payload) VALUES($1,$2,$3,$4,$5,$6)`, userID, entityType, entityID, version, action, data)
	return err
}

func invalid(field string) error { return fmt.Errorf("%w: %s", ErrValidation, field) }

func validText(value string, limit int) bool {
	return value != "" && utf8.ValidString(value) && !strings.ContainsRune(value, 0) && utf8.RuneCountInString(value) <= limit
}

func validDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Year() >= 1900 && parsed.Year() <= 9999 && parsed.Format("2006-01-02") == value
}

func validCents(value int64) bool { return value >= 0 && value <= maxCents }

func requestHash(input any) string {
	data, _ := json.Marshal(input)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func databaseError(err error) error {
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) && postgres.Code == "23505" {
		return ErrConflict
	}
	return err
}

func newID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	data[6] = (data[6] & 15) | 64
	data[8] = (data[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[:4], data[4:6], data[6:8], data[8:10], data[10:])
}

// Domain writes and analysis publication share one transaction-scoped fence.
func (s *Store) beginWrite(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(20261027)`); err != nil {
		tx.Rollback(context.Background())
		return nil, err
	}
	return tx, nil
}
