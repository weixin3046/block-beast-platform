package chain

import "time"

// WithdrawalInput 是一次提现申请；UserID 由服务端从访问令牌注入，不信任请求体。
type WithdrawalInput struct {
	UserID             string `json:"-"`
	ClientRequestID    string `json:"client_request_id"`
	DestinationAddress string `json:"destination_address"`
	DestinationMemo    string `json:"destination_memo,omitempty"`
	ChainCode          string `json:"chain_code"`
	Currency           string `json:"currency"`
	AmountMinor        int64  `json:"amount_minor"`
}

type Withdrawal struct {
	WithdrawalID       string    `json:"withdrawal_id"`
	UserID             string    `json:"-"`
	ClientRequestID    string    `json:"client_request_id"`
	DestinationAddress string    `json:"destination_address"`
	DestinationMemo    string    `json:"destination_memo,omitempty"`
	ChainCode          string    `json:"chain_code"`
	Currency           string    `json:"currency"`
	AmountMinor        int64     `json:"amount_minor"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
	ProviderOrderID    string    `json:"provider_order_id,omitempty"`
	TxHash             string    `json:"tx_hash,omitempty"`
	FailureReason      string    `json:"failure_reason,omitempty"`
}

type WithdrawalStatusInput struct {
	ProviderOrderID string `json:"provider_order_id"`
	TxHash          string `json:"tx_hash"`
	Status          string `json:"status"`
	FailureReason   string `json:"failure_reason"`
	Fee             string `json:"fee"`
}
