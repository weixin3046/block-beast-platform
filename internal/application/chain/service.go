package chain

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUnknownDepositAddress = errors.New("deposit address is not registered")
var ErrInvalidAmount = errors.New("amount must be positive")
var ErrMissingFields = errors.New("missing required fields")
var ErrWithdrawalNotFound = errors.New("withdrawal not found")
var ErrDepositAddressNotFound = errors.New("deposit address not found")
var ErrWithdrawalState = errors.New("withdrawal cannot transition from its current status")
var ErrUnsupportedAsset = errors.New("asset is not enabled for withdrawal")

type Service struct {
	pool               *pgxpool.Pool
	addressProvider    DepositAddressProvider
	withdrawalProvider WithdrawalProvider
}

type DepositAddressProvider interface {
	CreateDepositAddress(ctx context.Context, userID, chainCode, tokenCode string) (providerID, address, memo string, err error)
}

type ProviderWithdrawalRequest struct {
	RequestID    string
	ChainCode    string
	ChainTokenID int64
	Address      string
	Memo         string
	AmountMinor  int64
	Decimals     int
}

type WithdrawalProvider interface {
	CreateProviderWithdrawal(ctx context.Context, input ProviderWithdrawalRequest) (providerOrderID, status string, err error)
	GetProviderWithdrawal(ctx context.Context, appOrderNo string) (status, txHash, failureReason, fee string, err error)
}

type ReconcileResult struct {
	Checked   int `json:"checked"`
	Confirmed int `json:"confirmed"`
	Failed    int `json:"failed"`
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (service *Service) WithDepositAddressProvider(provider DepositAddressProvider) *Service {
	service.addressProvider = provider
	return service
}

func (service *Service) WithWithdrawalProvider(provider WithdrawalProvider) *Service {
	service.withdrawalProvider = provider
	return service
}
