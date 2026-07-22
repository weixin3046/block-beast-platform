package settlement

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/block-beast/platform/internal/domain/game"
)

// ResultSource 为结算提供开奖结果。本地开发使用确定性的哈希实现，
// 生产环境应替换为 TRON 区块哈希或 K 线等外部可验证来源。
type ResultSource interface {
	Outcome(ctx context.Context, round game.Round, rules game.Rules) ([]string, error)
}

// HashResultSource 以轮次身份（game_type + sequence + round_id）的 SHA-256
// 从规则结果池中确定性地抽取结果：同一轮次重复开奖结果必然一致，
// 因而天然满足结算幂等要求。
type HashResultSource struct{}

func NewHashResultSource() HashResultSource {
	return HashResultSource{}
}

func (source HashResultSource) Outcome(_ context.Context, round game.Round, rules game.Rules) ([]string, error) {
	if err := rules.Validate(); err != nil {
		return nil, err
	}
	count := rules.DrawCount()
	pool := append([]string(nil), rules.Outcomes...)
	seed := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", round.GameType, round.Sequence, round.RoundID)))
	drawn := make([]string, 0, count)
	for index := 0; index < count; index++ {
		// 每个抽签位置使用不同的哈希窗口，避免重复结果偏向固定偏移。
		offset := (index * 8) % (sha256.Size - 8)
		roll := binary.BigEndian.Uint64(seed[offset : offset+8])
		pick := roll % uint64(len(pool))
		drawn = append(drawn, pool[pick])
		pool = append(pool[:pick], pool[pick+1:]...)
	}
	return drawn, nil
}
