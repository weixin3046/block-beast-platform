package game

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) CloseDue(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	rows, err := repository.pool.Query(ctx, `
		UPDATE rounds
		SET status = 'closed', version = version + 1
		WHERE id IN (
			SELECT id
			FROM rounds
			WHERE status = 'open' AND bet_closes_at <= $1
			ORDER BY bet_closes_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roundIDs := make([]string, 0)
	for rows.Next() {
		var roundID string
		if err := rows.Scan(&roundID); err != nil {
			return nil, err
		}
		roundIDs = append(roundIDs, roundID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return roundIDs, nil
}
