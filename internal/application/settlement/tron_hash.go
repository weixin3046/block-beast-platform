package settlement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/game"
)

const tronGridFullNodeURL = "https://api.trongrid.io"

// TronHashResultSource 从 TRON 官方 FullNode HTTP API 的区块哈希提取尾数作为开奖结果。
// 哈希轮次的 sequence 就是创建轮次时锁定的目标区块高度。
type TronHashResultSource struct {
	baseURL string
	apiKey  string
	client  *http.Client
	grpc    *tronGRPCBlockClient
}

// NewTronHashResultSource 创建使用 TRON 官方 TronGrid FullNode API 的哈希结果源。
func NewTronHashResultSource(apiKey string) TronHashResultSource {
	return NewTronHashResultSourceWithGRPC(apiKey, tronGridGRPCFullNodeEndpoint)
}

func NewTronHashResultSourceWithGRPC(apiKey, grpcEndpoint string) TronHashResultSource {
	if strings.HasPrefix(apiKey, "http://") || strings.HasPrefix(apiKey, "https://") {
		return newTronHashResultSourceForEndpoint(apiKey, "")
	}
	return TronHashResultSource{
		baseURL: tronGridFullNodeURL,
		apiKey:  strings.TrimSpace(apiKey),
		client:  &http.Client{Timeout: 10 * time.Second},
		grpc:    newTronGRPCBlockClient(strings.TrimSpace(grpcEndpoint), strings.TrimSpace(apiKey)),
	}
}

func newTronHashResultSourceForEndpoint(endpoint string, apiKey string) TronHashResultSource {
	return TronHashResultSource{baseURL: strings.TrimRight(endpoint, "/"), apiKey: strings.TrimSpace(apiKey), client: &http.Client{Timeout: 10 * time.Second}}
}

// tronExtras 解析 rules.extras 中的 TRON 数据源参数。
type tronExtras struct {
	BlockInterval int64 `json:"block_interval"`
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
	if extras.BlockInterval <= 0 {
		extras.BlockInterval = 5
	}
	if round.Sequence <= 0 {
		return nil, errors.New("tron_hash: target block height is required")
	}
	targetHeight := round.Sequence

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

// CurrentBlockHeight 返回 TRON 最新已生成区块高度，供轮次调度器选择下一个
// interval 整倍数区块。目标高度一旦写入 round.sequence，后续重试不会漂移。
func (source TronHashResultSource) CurrentBlock(ctx context.Context) (int64, time.Time, error) {
	block, err := source.fetchCurrentBlock(ctx)
	if err != nil {
		return 0, time.Time{}, err
	}
	if block.Number() <= 0 || block.Timestamp() <= 0 {
		return 0, time.Time{}, errors.New("tron full node returned invalid block metadata")
	}
	return block.Number(), time.UnixMilli(block.Timestamp()).UTC(), nil
}

// fetchBlockHash 调用兼容的 JSON-RPC eth_getBlockByNumber 获取区块哈希。
// 区块未产出（result 为 null）时返回 ErrBlockNotFound，调用方应等待下轮重试。
func (source TronHashResultSource) fetchBlockHash(ctx context.Context, height int64) (string, error) {
	block, err := source.fetchBlockByHeight(ctx, height)
	if err != nil {
		return "", err
	}
	if block.Hash == "" {
		return "", ErrBlockNotFound
	}
	return block.Hash, nil
}

func (source TronHashResultSource) Close() error {
	if source.grpc == nil {
		return nil
	}
	return source.grpc.Close()
}

func (source TronHashResultSource) fetchCurrentBlock(ctx context.Context) (tronBlock, error) {
	if source.grpc != nil && source.grpc.endpoint != "" {
		block, err := tronGRPCNowBlock(ctx, source.grpc)
		if err == nil {
			return block, nil
		}
	}
	return source.fetchBlock(ctx, "/wallet/getnowblock", map[string]any{})
}

func (source TronHashResultSource) fetchBlockByHeight(ctx context.Context, height int64) (tronBlock, error) {
	if source.grpc != nil && source.grpc.endpoint != "" {
		block, err := tronGRPCBlockByNumber(ctx, source.grpc, height)
		if err == nil {
			return block, nil
		}
	}
	return source.fetchBlock(ctx, "/wallet/getblockbynum", map[string]any{"num": height})
}

type tronBlock struct {
	Hash   string `json:"blockID"`
	Header struct {
		RawData struct {
			Number    int64 `json:"number"`
			Timestamp int64 `json:"timestamp"`
		} `json:"raw_data"`
	} `json:"block_header"`
}

func (block tronBlock) Number() int64    { return block.Header.RawData.Number }
func (block tronBlock) Timestamp() int64 { return block.Header.RawData.Timestamp }

func (source TronHashResultSource) fetchBlock(ctx context.Context, path string, payload map[string]any) (tronBlock, error) {
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return tronBlock{}, errors.New("marshal TRON full node request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, source.baseURL+path, bytes.NewReader(requestBody))
	if err != nil {
		return tronBlock{}, errors.New("create TRON full node request")
	}
	request.Header.Set("Content-Type", "application/json")
	if source.apiKey != "" {
		request.Header.Set("TRON-PRO-API-KEY", source.apiKey)
	}
	response, err := source.client.Do(request)
	if err != nil {
		return tronBlock{}, errors.New("call TRON full node")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return tronBlock{}, fmt.Errorf("TRON full node returned HTTP %d", response.StatusCode)
	}
	var block tronBlock
	if err := json.NewDecoder(response.Body).Decode(&block); err != nil {
		return tronBlock{}, errors.New("decode TRON full node response")
	}
	return block, nil
}
