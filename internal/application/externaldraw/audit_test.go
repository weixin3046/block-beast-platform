package externaldraw

import (
	"context"
	"encoding/json"
	"github.com/block-beast/platform/internal/platform/luludraw"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
)

type auditTx struct {
	pgx.Tx
	args []any
}

func (tx *auditTx) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	tx.args = args
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func TestAuditDrawResultRecordsSourceWithoutRawMessage(t *testing.T) {
	for _, tc := range []struct{ kind, field, transport string }{{"3004", "failedRoomId", "websocket"}, {"3005", "killedRooms", "websocket"}, {"2007", "result.list[].fail", "websocket"}, {"luluall_history", "items", "luluall_http"}} {
		tx := &auditTx{}
		event := luludraw.Event{Game: "xdy", Kind: tc.kind, ResultField: tc.field, Result: []string{"5"}}
		if err := auditDrawResult(context.Background(), tx, "lulu_draw.result_conflict", event, 8891, json.RawMessage(`["5","7","8"]`)); err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		if err := json.Unmarshal(tx.args[3].([]byte), &p); err != nil {
			t.Fatal(err)
		}
		if p["transport"] != tc.transport || p["event_type"] != tc.kind || p["result_field"] != tc.field || len(p) != 7 {
			t.Fatalf("unexpected audit metadata: %v", p)
		}
		if len(p["saved_outcome"].([]any)) != 3 || len(p["incoming_outcome"].([]any)) != 1 {
			t.Fatal("missing result evidence")
		}
	}
}
