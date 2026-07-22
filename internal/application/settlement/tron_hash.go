package settlement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/block-beast/platform/internal/domain/game"
)

// TronHashResultSource 从 TRON 区块哈希提取尾数作为开奖结果。
// 目标区块高度 = base_block_height + sequence × block_interval，由轮次唯一确定，天然幂等。
type TronHashResultSource struct {
	baseURL string
	client  *http.Client
}

// NewTronHashResultSource 创建 TRON 哈希结果源。baseURL 为 QuickNode JSON-RPC 端点（已内嵌 token）。
func NewTronHashResultSource(baseURL string) TronHashResultSource {
	return TronHashResultSource{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// tronExtras 解析 rules.extras 中的 TRON 数据源参数。
type tronExtras struct {
	BaseBlockHeight int64 `json:"base_block_height"`
	BlockInterval   int64 `json:"block_interval"`
}

// jsonRPCRequest 是 JSON-RPC 2.0 请求结构。
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

// jsonRPCResponse 是 JSON-RPC 2.0 响应结构。
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// blockResult 是 eth_getBlockByNumber 返回的区块结果（仅取需要的字段）。
type blockResult struct {
	Hash string `json:"hash"`
}

var ErrBlockNotFound = errors.New("target block not yet produced")

// Outcome 实现 ResultSource 接口：根据轮次序号定位目标区块，取哈希尾数映射为 outcome。
func (source TronHashResultSource) Outcome(ctx context.Context, round game.Round, rules game.Rules) ([]string, error) {
	var extras tronExtras
	if len(rules.Extras) > 0 {
		if err := json.Unmarshal(rules.Extras, &extras); err != nil {
			return nil, fmt.Errorf("parse tron extras: %w", err)
		}
	}
	if extras.BaseBlockHeight <= 0 {
		return nil, errors.New("tron_hash: base_block_height is required in extras")
	}
	if extras.BlockInterval <= 0 {
		extras.BlockInterval = 5
	}
	targetHeight := extras.BaseBlockHeight + round.Sequence*extras.BlockInterval

	hash, err := source.fetchBlockHash(ctx, targetHeight)
	if err != nil {
		return nil, err
	}

	digit, err := lastDigit(hash)
	if err != nil {
		return nil, fmt.Errorf("extract digit from block hash: %w", err)
	}

	shape := detectShape(rules.Outcomes, rules.DodgeMode)
	return mapOutcome(digit, shape), nil
}

// fetchBlockHash 调用 QuickNode JSON-RPC eth_getBlockByNumber 获取区块哈希。
// 区块未产出（result 为 null）时返回 ErrBlockNotFound，调用方应等待下轮重试。
func (source TronHashResultSource) fetchBlockHash(ctx context.Context, height int64) (string, error) {
	heightHex := "0x" + strconv.FormatInt(height, 16)
	requestBody, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "eth_getBlockByNumber",
		Params:  []any{heightHex, false},
	})
	if err != nil {
		return "", fmt.Errorf("marshal jsonrpc request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, source.baseURL, bytes.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("create http request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, err := source.client.Do(httpRequest)
	if err != nil {
		return "", fmt.Errorf("call tron rpc: %w", err)
	}
	defer httpResponse.Body.Close()

	body, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	var rpcResponse jsonRPCResponse
	if err := json.Unmarshal(body, &rpcResponse); err != nil {
		return "", fmt.Errorf("unmarshal jsonrpc response: %w", err)
	}
	if rpcResponse.Error != nil {
		return "", fmt.Errorf("tron rpc error %d: %s", rpcResponse.Error.Code, rpcResponse.Error.Message)
	}

	// result 为 null 表示区块未产出。
	if string(rpcResponse.Result) == "null" {
		return "", ErrBlockNotFound
	}

	var block blockResult
	if err := json.Unmarshal(rpcResponse.Result, &block); err != nil {
		return "", fmt.Errorf("unmarshal block result: %w", err)
	}
	if block.Hash == "" {
		return "", errors.New("block hash is empty")
	}
	return block.Hash, nil
}
