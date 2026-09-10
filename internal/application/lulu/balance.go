package lulu

import (
	"context"
	"errors"
	"time"
)

var ErrBalanceUnavailable = errors.New("lulu balance unavailable")
var ErrBalanceTimeout = errors.New("lulu balance timeout")

type AccountBalance struct {
	ReceiverUID string    `json:"receiver_uid"`
	ItemID      int64     `json:"item_id"`
	Balance     string    `json:"balance"`
	QueriedAt   time.Time `json:"queried_at"`
}
type BalanceReader interface {
	Balance(context.Context) (string, error)
}
type BalanceFactory func(string, string, string, string) (BalanceReader, error)

func (s *Service) WithBalanceFactory(f BalanceFactory) *Service { s.balanceFactory = f; return s }
func (s *Service) AccountBalance(ctx context.Context, actor string) (AccountBalance, error) {
	var out AccountBalance
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = admin(ctx, tx, actor); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	cfg, err := s.runtimeConfig(ctx, true)
	if err != nil {
		return out, err
	}
	if cfg.Token == "" || cfg.ProtocolKey == "" || cfg.APIURL == "" || !ValidUID(cfg.ReceiverUID) {
		return out, ErrConfigInvalid
	}
	if s.balanceFactory == nil {
		return out, ErrBalanceUnavailable
	}
	reader, err := s.balanceFactory(cfg.APIURL, cfg.ReceiverUID, cfg.Token, cfg.ProtocolKey)
	if err != nil {
		return out, ErrBalanceUnavailable
	}
	queryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	value, err := reader.Balance(queryCtx)
	if err != nil {
		if queryCtx.Err() != nil {
			return out, ErrBalanceTimeout
		}
		return out, err
	}
	return AccountBalance{cfg.ReceiverUID, 102201, value, time.Now().UTC()}, nil
}
