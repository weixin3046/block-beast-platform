package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/block-beast/platform/internal/domain/game"
)

// OkxKlineResultSource 从 OKX 现货 1 分钟 K 线收盘价提取尾数作为开奖结果。
// 目标 K 线分钟 = floor(round.BetClosesAt / 60s)，取该分钟已收盘的 K 线。
type OkxKlineResultSource struct {
	baseURL string
	client  *http.Client
}

// NewOkxKlineResultSource 创建 OKX K 线结果源。baseURL 为 OKX REST API 基础地址。
func NewOkxKlineResultSource(baseURL string) OkxKlineResultSource {
	return OkxKlineResultSource{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// okxExtras 解析 rules.extras 中的 OKX 数据源参数。
type okxExtras struct {
	Symbol string `json:"symbol"` // 如 "BTC-USDT"、"ETH-USDT"
}

// okxCandlesResponse 是 OKX GET /api/v5/market/candles 的响应结构。
type okxCandlesResponse struct {
	Code string     `json:"code"`
	Msg  string     `json:"msg"`
	Data [][]string `json:"data"`
}

var ErrKlineNotReady = errors.New("target kline not yet closed")

// Outcome 实现 ResultSource 接口：按轮次封盘时间定位 1 分钟 K 线，取收盘价尾数映射奇偶。
func (source OkxKlineResultSource) Outcome(ctx context.Context, round game.Round, rules game.Rules) ([]string, error) {
	var extras okxExtras
	if len(rules.Extras) > 0 {
		if err := json.Unmarshal(rules.Extras, &extras); err != nil {
			return nil, fmt.Errorf("parse okx extras: %w", err)
		}
	}
	if extras.Symbol == "" {
		return nil, errors.New("okx_kline: symbol is required in extras")
	}

	// 目标 K 线分钟 = 封盘时间向下取整到分钟。
	targetMinute := round.BetClosesAt.UTC().Truncate(time.Minute)
	closePrice, err := source.fetchKlineClose(ctx, extras.Symbol, targetMinute)
	if err != nil {
		return nil, err
	}

	digit, err := lastDigit(closePrice)
	if err != nil {
		return nil, fmt.Errorf("extract digit from close price %q: %w", closePrice, err)
	}

	shape := detectShape(rules.Outcomes, rules.DodgeMode)
	return mapOutcome(digit, shape), nil
}

// fetchKlineClose 调用 OKX REST API 获取指定分钟的 1m K 线收盘价。
// K 线未收盘或时间不对齐时返回 ErrKlineNotReady，调用方应等待下轮重试。
func (source OkxKlineResultSource) fetchKlineClose(ctx context.Context, symbol string, targetMinute time.Time) (string, error) {
	// OKX after 参数是毫秒时间戳，表示返回该时间之前的数据。
	// 请求 after=targetMinute+60s 来获取 targetMinute 这一分钟的 K 线。
	afterMs := targetMinute.Add(time.Minute).UnixMilli()
	url := fmt.Sprintf("%s/api/v5/market/candles?instId=%s&bar=1m&after=%d&limit=1",
		source.baseURL, symbol, afterMs)

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create http request: %w", err)
	}

	httpResponse, err := source.client.Do(httpRequest)
	if err != nil {
		return "", fmt.Errorf("call okx api: %w", err)
	}
	defer httpResponse.Body.Close()

	body, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	var candles okxCandlesResponse
	if err := json.Unmarshal(body, &candles); err != nil {
		return "", fmt.Errorf("unmarshal okx response: %w", err)
	}
	if candles.Code != "0" {
		return "", fmt.Errorf("okx api error %s: %s", candles.Code, candles.Msg)
	}
	if len(candles.Data) == 0 {
		return "", ErrKlineNotReady
	}

	candle := candles.Data[0]
	if len(candle) < 9 {
		return "", errors.New("okx candle data incomplete")
	}

	// candle[0] = ts（开盘时间，毫秒时间戳字符串）
	// candle[4] = close（收盘价）
	// candle[8] = confirm（"1" 表示已收盘，"0" 表示未收盘）
	candleTs, err := parseOkxTimestamp(candle[0])
	if err != nil {
		return "", fmt.Errorf("parse candle timestamp: %w", err)
	}

	// 校验返回的 K 线时间是否与目标分钟对齐。
	if candleTs != targetMinute.UnixMilli() {
		return "", fmt.Errorf("%w: candle ts %d != target %d", ErrKlineNotReady, candleTs, targetMinute.UnixMilli())
	}

	// 校验 K 线是否已收盘。
	if candle[8] != "1" {
		return "", ErrKlineNotReady
	}

	closePrice := candle[4]
	if closePrice == "" {
		return "", errors.New("close price is empty")
	}
	return closePrice, nil
}

// parseOkxTimestamp 解析 OKX 毫秒时间戳字符串。
func parseOkxTimestamp(ts string) (int64, error) {
	var value int64
	if _, err := fmt.Sscanf(ts, "%d", &value); err != nil {
		return 0, err
	}
	return value, nil
}
