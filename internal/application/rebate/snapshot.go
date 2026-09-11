package rebate

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// SnapshotTx freezes the complete eligible ancestor chain in the placing
// transaction. A missing/disabled configuration never falls back to legacy rates.
func SnapshotTx(ctx context.Context, tx pgx.Tx, betID string) error {
	var user string
	var room *string
	var simulated bool
	if e := tx.QueryRow(ctx, `SELECT b.user_id::text,b.game_room_id::text,b.is_simulated FROM bets b WHERE b.id=$1`, betID).Scan(&user, &room, &simulated); e != nil {
		return e
	}
	var roomConfigID *string
	var version *int64
	var enabled bool
	var rates []int
	if room != nil && !simulated {
		e := tx.QueryRow(ctx, `SELECT id::text,version,enabled,rates FROM room_rebate_configs WHERE room_id=$1 FOR SHARE`, *room).Scan(&roomConfigID, &version, &enabled, &rates)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
	}
	ancestors := []Ancestor{}
	if enabled {
		// One recursive statement provides a coherent MVCC view of levels and links.
		rows, e := tx.Query(ctx, `WITH RECURSIVE chain AS (
   SELECT u.id,COALESCE(u.agent_level,0) level,u.is_virtual,1 depth,ARRAY[$1::uuid,u.id] visited
   FROM agent_relations ar JOIN users u ON u.id=ar.parent_user_id WHERE ar.user_id=$1 AND u.id<>$1
   UNION ALL
   SELECT u.id,COALESCE(u.agent_level,0),u.is_virtual,c.depth+1,c.visited||u.id FROM chain c JOIN agent_relations ar ON ar.user_id=c.id JOIN users u ON u.id=ar.parent_user_id WHERE NOT u.id=ANY(c.visited)
  ) SELECT id::text,level,is_virtual FROM chain ORDER BY depth`, user)
		if e != nil {
			return e
		}
		for rows.Next() {
			var a Ancestor
			if e = rows.Scan(&a.UserID, &a.Level, &a.IsVirtual); e != nil {
				rows.Close()
				return e
			}
			if a.Level > 0 && a.Level <= len(rates) {
				a.RatePerMille = rates[a.Level-1]
			}
			ancestors = append(ancestors, a)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	raw, e := json.Marshal(ancestors)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO bet_rebate_snapshots(bet_id,room_config_id,config_version,enabled,ancestors) VALUES($1,$2,$3,$4,$5)`, betID, roomConfigID, version, enabled, raw); e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE bets SET rebate_version=2 WHERE id=$1`, betID)
	return e
}

func LoadTx(ctx context.Context, tx pgx.Tx, betID string) ([]Ancestor, error) {
	var raw []byte
	var enabled bool
	if e := tx.QueryRow(ctx, `SELECT enabled,ancestors FROM bet_rebate_snapshots WHERE bet_id=$1`, betID).Scan(&enabled, &raw); e != nil {
		return nil, e
	}
	if !enabled {
		return nil, nil
	}
	var chain []Ancestor
	e := json.Unmarshal(raw, &chain)
	return chain, e
}
