package rebate

import (
	"context"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type RecordQuery struct {
	SourceUserID, BeneficiaryUserID              int64
	Currency, Status, GameType, RoomID, ViewerID string
	From, To                                     *time.Time
	Limit, Offset                                int
}
type Record struct {
	ID                string    `json:"id"`
	BetID             string    `json:"bet_id"`
	SourceUserID      int64     `json:"source_user_id"`
	BeneficiaryUserID int64     `json:"beneficiary_user_id"`
	RoundSequence     int64     `json:"round_sequence"`
	GameType          string    `json:"game_type"`
	RoomID            string    `json:"game_room_id"`
	RoomName          string    `json:"game_room_name"`
	Currency          string    `json:"currency"`
	Base              string    `json:"base"`
	Level             int       `json:"agent_level"`
	Differential      int       `json:"differential_per_mille"`
	Amount            string    `json:"amount"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
}
type Summary struct {
	Currency       string `json:"currency"`
	SourceBets     int64  `json:"source_bets"`
	PaidCount      int64  `json:"paid_count"`
	ReversedCount  int64  `json:"reversed_count"`
	PaidAmount     string `json:"paid_amount"`
	ReversedAmount string `json:"reversed_amount"`
}
type Records struct {
	Items   []Record  `json:"items"`
	Total   int64     `json:"total"`
	Summary []Summary `json:"summary"`
}

const recordsFrom = ` FROM rebate_allocations a JOIN commission_entries c ON c.id=a.commission_id JOIN bets b ON b.id=a.bet_id JOIN users src ON src.id=b.user_id JOIN users dst ON dst.id=a.beneficiary_user_id JOIN rounds r ON r.id=b.round_id JOIN game_types g ON g.id=r.game_type_id JOIN game_rooms gr ON gr.id=b.game_room_id JOIN currencies cur ON cur.code=c.currency
 WHERE NOT b.is_simulated AND NOT src.is_virtual AND NOT dst.is_virtual
 AND ($1::bigint=0 OR src.public_id=$1) AND ($2::bigint=0 OR dst.public_id=$2)
 AND ($3='' OR c.currency=$3) AND ($4='' OR c.status=$4) AND ($5='' OR g.code=$5) AND ($6='' OR gr.id::text=$6)
 AND ($7::timestamptz IS NULL OR a.created_at >= $7) AND ($8::timestamptz IS NULL OR a.created_at < $8)
 AND ($9='' OR dst.id::text=$9)`

func (s *Service) ListRecords(ctx context.Context, q RecordQuery) (Records, error) {
	out := Records{Items: []Record{}, Summary: []Summary{}}
	if q.SourceUserID < 0 || q.BeneficiaryUserID < 0 || q.Offset < 0 || (q.Status != "" && q.Status != "paid" && q.Status != "reversed") || (q.From != nil && q.To != nil && !q.From.Before(*q.To)) {
		return out, ErrInvalid
	}
	if q.Limit < 1 || q.Limit > 100 {
		q.Limit = 50
	}
	args := []any{q.SourceUserID, q.BeneficiaryUserID, strings.ToUpper(q.Currency), q.Status, q.GameType, q.RoomID, q.From, q.To, q.ViewerID}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if e = tx.QueryRow(ctx, `SELECT count(*)`+recordsFrom, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := tx.Query(ctx, `SELECT c.id::text,b.id::text,src.public_id,dst.public_id,r.sequence,g.code,gr.id::text,gr.name,c.currency,round(a.base_minor::numeric/power(10::numeric,cur.decimals),cur.decimals)::text,a.agent_level,a.differential_per_mille,round(c.amount_minor::numeric/power(10::numeric,cur.decimals),cur.decimals)::text,c.status,a.created_at`+recordsFrom+` ORDER BY a.created_at DESC,c.id DESC LIMIT $10 OFFSET $11`, append(args, q.Limit, q.Offset)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var r Record
		if e = rows.Scan(&r.ID, &r.BetID, &r.SourceUserID, &r.BeneficiaryUserID, &r.RoundSequence, &r.GameType, &r.RoomID, &r.RoomName, &r.Currency, &r.Base, &r.Level, &r.Differential, &r.Amount, &r.Status, &r.CreatedAt); e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = tx.Query(ctx, `SELECT c.currency,count(DISTINCT b.id),count(*) FILTER(WHERE c.status='paid'),count(*) FILTER(WHERE c.status='reversed'),round(COALESCE(sum(c.amount_minor) FILTER(WHERE c.status='paid'),0)/power(10::numeric,cur.decimals),cur.decimals)::text,round(COALESCE(sum(c.amount_minor) FILTER(WHERE c.status='reversed'),0)/power(10::numeric,cur.decimals),cur.decimals)::text`+recordsFrom+` GROUP BY c.currency,cur.decimals ORDER BY c.currency`, args...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var r Summary
		if e = rows.Scan(&r.Currency, &r.SourceBets, &r.PaidCount, &r.ReversedCount, &r.PaidAmount, &r.ReversedAmount); e != nil {
			rows.Close()
			return out, e
		}
		out.Summary = append(out.Summary, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit(ctx)
}
