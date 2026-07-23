package agent

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidRelation = errors.New("invalid agent relation")
var ErrRelationExists = errors.New("agent relation already exists")

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Bind creates an immutable direct referral relation and a materialized ltree path.
func (service *Service) Bind(ctx context.Context, userID, parentID string) error {
	if userID == "" || parentID == "" || userID == parentID {
		return ErrInvalidRelation
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, parentID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrInvalidRelation
	}
	var parentPath string
	err = tx.QueryRow(ctx, `SELECT COALESCE(path::text,'') FROM agent_relations WHERE user_id=$1`, parentID).Scan(&parentPath)
	if errors.Is(err, pgx.ErrNoRows) {
		parentPath = ""
	} else if err != nil {
		return err
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT parent_user_id::text FROM agent_relations WHERE user_id=$1`, userID).Scan(&existing)
	if err == nil {
		return ErrRelationExists
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	path := parentID
	if parentPath != "" {
		path = parentPath + "." + userID
	}
	_, err = tx.Exec(ctx, `INSERT INTO agent_relations(user_id,parent_user_id,path) VALUES($1,$2,$3::ltree)`, userID, parentID, path)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
