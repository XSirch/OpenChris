package telegram

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ReplyBuilder func(context.Context, string) (string, error)

// The bridge only persists work and reads completed work. It never infers in
// a Telegram transaction or supplies free-form model text for delivery.
type AnalysisBridge interface {
	Enabled() bool
	EnqueueTx(context.Context, pgx.Tx, int64, string) error
	TelegramReply(context.Context, pgx.Tx, int64) (string, bool, error)
}

type Service struct {
	db       *pgxpool.Pool
	cfg      Config
	client   *Client
	botID    int64
	reply    ReplyBuilder
	mu       sync.RWMutex
	setup    bool
	analysis AnalysisBridge
	now      func() time.Time
}

// SetAnalysis is startup wiring and must precede Run/HandleUpdate.
func (s *Service) SetAnalysis(bridge AnalysisBridge) { s.analysis = bridge }

func New(db *pgxpool.Pool, cfg Config, reply ReplyBuilder) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var client *Client
	var err error
	if cfg.Enabled {
		client, err = NewClient(cfg.BotToken)
	}
	if err != nil {
		return nil, err
	}
	return NewWithClient(db, cfg, reply, client)
}

func NewWithClient(db *pgxpool.Pool, cfg Config, reply ReplyBuilder, client *Client) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var id int64
	if cfg.Enabled {
		if client == nil || client.token != cfg.BotToken {
			return nil, ErrInvalid
		}
		id, _ = tokenIdentity(cfg.BotToken)
	}
	s := &Service{db: db, cfg: cfg, client: client, botID: id, reply: reply, now: time.Now}
	if cfg.Enabled && cfg.AutonomyEnabled && db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.registerAutonomyIdentity(ctx); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Service) Setup(ctx context.Context) error {
	if !s.cfg.Enabled {
		return ErrDisabled
	}
	bot, err := s.client.GetMe(ctx)
	if err != nil {
		return err
	}
	if bot.ID != s.botID {
		return errors.New("telegram bot identity mismatch")
	}
	info, err := s.client.GetWebhookInfo(ctx)
	if err != nil {
		return err
	}
	if info.URL != "" && info.URL != s.cfg.PublicURL {
		return errors.New("telegram webhook belongs to another endpoint")
	}
	if err = s.client.SetWebhook(ctx, s.cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.setup = true
	s.mu.Unlock()
	return nil
}
