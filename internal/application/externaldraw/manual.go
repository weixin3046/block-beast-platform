package externaldraw

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrManualConflict = errors.New("manual draw result conflicts with existing state")
var ErrManualNotFound = errors.New("manual draw round not found")

type ManualInput struct {
	Game   string `json:"game"`
	Issue  string `json:"issue"`
	Result []int  `json:"result"`
	Reason string `json:"reason"`
}

type ManualResult struct {
	Game             string `json:"game"`
	Issue            string `json:"issue"`
	Result           []int  `json:"result"`
	Status           string `json:"status"`
	AlreadyConfirmed bool   `json:"already_confirmed"`
}

func validateManual(in ManualInput) (string, int64, []string, error) {
	game, ok := sourceGame(in.Game)
	issue, err := strconv.ParseInt(in.Issue, 10, 64)
	if !ok || err != nil || issue <= 0 || strconv.FormatInt(issue, 10) != in.Issue || strings.TrimSpace(in.Reason) == "" || len([]rune(in.Reason)) > 1000 {
		return "", 0, nil, ErrInvalidEvent
	}
	max, count := 8, 7
	if game == "lh" {
		max, count = 2, 1
	}
	if game == "race" {
		max, count = 6, 1
	}
	if len(in.Result) == 0 || len(in.Result) > count {
		return "", 0, nil, ErrInvalidEvent
	}
	seen := map[int]bool{}
	result := make([]string, 0, len(in.Result))
	for _, n := range in.Result {
		if n < 1 || n > max || seen[n] {
			return "", 0, nil, ErrInvalidEvent
		}
		seen[n] = true
		result = append(result, strconv.Itoa(n))
	}
	return game, issue, result, nil
}

// 补录只确认缺失结果，不直接改钱包；Worker 继续通过原有幂等事务结算。
func (s *Service) ConfirmManual(ctx context.Context, actor string, in ManualInput) (ManualResult, error) {
	game, issue, result, err := validateManual(in)
	out := ManualResult{Game: in.Game, Issue: in.Issue, Result: in.Result, Status: "confirmed"}
	if err != nil {
		return out, err
	}
	if _, err = uuid.Parse(actor); err != nil || s.pool == nil {
		return out, ErrInvalidEvent
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	// 锁定已存在的采集记录，防止与实时消息并发覆盖。
	var status string
	var saved json.RawMessage
	err = tx.QueryRow(ctx, `SELECT status,outcome FROM external_draw_rounds WHERE source=$1 AND game=$2 AND external_round=$3 FOR UPDATE`, source, game, issue).Scan(&status, &saved)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrManualNotFound
	}
	if err != nil {
		return out, err
	}
	if status == "confirmed" && sameDrawResult(saved, result) {
		out.AlreadyConfirmed = true
		return out, tx.Commit(ctx)
	}
	if status != "pending" || (len(saved) > 0 && string(saved) != "null") {
		return out, ErrManualConflict
	}
	var roundStatus string
	var resultAt time.Time
	err = tx.QueryRow(ctx, `SELECT r.status,r.result_at FROM rounds r JOIN game_types g ON g.id=r.game_type_id WHERE r.sequence=$1 AND g.rules->>'source'='lulu_ws' AND g.rules->'extras'->>'external_game'=$2 FOR UPDATE OF r`, issue, game).Scan(&roundStatus, &resultAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrManualNotFound
	}
	if err != nil {
		return out, err
	}
	if (roundStatus != "closed" && roundStatus != "open") || resultAt.After(time.Now()) {
		return out, ErrManualConflict
	}
	encoded, _ := json.Marshal(result)
	_, err = tx.Exec(ctx, `UPDATE external_draw_rounds SET outcome=$4,status='confirmed',conflict_outcome=NULL,result_received_at=now(),updated_at=now() WHERE source=$1 AND game=$2 AND external_round=$3`, source, game, issue, encoded)
	if err != nil {
		return out, err
	}
	payload, _ := json.Marshal(map[string]any{"game": game, "round": issue, "incoming_outcome": result, "previous_status": status, "reason": strings.TrimSpace(in.Reason), "result_source": "admin_manual"})
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES($1,$2,'lulu_draw.result_manual_confirmed','external_draw_round',$3,$4)`, uuid.NewString(), actor, source+":"+game+":"+strconv.FormatInt(issue, 10), payload)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
