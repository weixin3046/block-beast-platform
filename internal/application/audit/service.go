package audit

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Entry 是一条不可变审计记录；ActorUserID 为空表示匿名或系统操作，写入 NULL。
type Entry struct {
	ActorUserID string
	Action      string
	TargetType  string
	TargetID    string
	Payload     any
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Record 将审计记录追加到 audit_logs。审计表只增不改，构成完整操作轨迹。
func (service *Service) Record(ctx context.Context, entry Entry) error {
	payload, err := json.Marshal(entry.Payload)
	if err != nil {
		return err
	}
	var actorUserID *string
	if entry.ActorUserID != "" {
		actorUserID = &entry.ActorUserID
	}
	_, err = service.pool.Exec(ctx, `
		INSERT INTO audit_logs (id, actor_user_id, action, target_type, target_id, payload)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.NewString(), actorUserID, entry.Action, entry.TargetType, entry.TargetID, payload)
	return err
}
