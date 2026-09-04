package currency

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalid = errors.New("invalid currency definition")
var ErrConflict = errors.New("currency already exists or version is stale")
var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

type Currency struct {
	ID                   string `json:"id"`
	Code                 string `json:"code"`
	Name                 string `json:"name"`
	Decimals             int    `json:"decimals"`
	Category             string `json:"category"`
	Enabled              bool   `json:"enabled"`
	CreateOnRegistration bool   `json:"create_on_registration"`
	SortOrder            int    `json:"sort_order"`
	Version              int64  `json:"version"`
}
type Update struct {
	Name                 string `json:"name"`
	Enabled              bool   `json:"enabled"`
	CreateOnRegistration bool   `json:"create_on_registration"`
	SortOrder            int    `json:"sort_order"`
	Version              int64  `json:"version"`
}
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

const columns = `id::text,code,name,decimals,category,enabled,create_on_registration,sort_order,version`

func scan(row pgx.Row) (Currency, error) {
	var c Currency
	err := row.Scan(&c.ID, &c.Code, &c.Name, &c.Decimals, &c.Category, &c.Enabled, &c.CreateOnRegistration, &c.SortOrder, &c.Version)
	return c, err
}
func (s *Service) List(ctx context.Context, enabledOnly bool) ([]Currency, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+columns+` FROM currencies WHERE NOT $1 OR enabled ORDER BY sort_order,code`, enabledOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Currency{}
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func validate(c Currency) error {
	if !codePattern.MatchString(c.Code) || strings.TrimSpace(c.Name) == "" || c.Decimals < 0 || c.Decimals > 18 {
		return ErrInvalid
	}
	switch c.Category {
	case "token", "points", "stamina", "custom":
	default:
		return ErrInvalid
	}
	return nil
}
func (s *Service) Create(ctx context.Context, c Currency) (Currency, error) {
	c.Code = strings.ToUpper(strings.TrimSpace(c.Code))
	c.Name = strings.TrimSpace(c.Name)
	if err := validate(c); err != nil {
		return Currency{}, err
	}
	result, err := scan(s.pool.QueryRow(ctx, `INSERT INTO currencies(code,name,decimals,category,enabled,create_on_registration,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+columns, c.Code, c.Name, c.Decimals, c.Category, c.Enabled, c.CreateOnRegistration, c.SortOrder))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Currency{}, ErrConflict
	}
	return result, err
}
func (s *Service) Update(ctx context.Context, code string, u Update) (Currency, error) {
	if strings.TrimSpace(u.Name) == "" || u.Version < 1 {
		return Currency{}, ErrInvalid
	}
	result, err := scan(s.pool.QueryRow(ctx, `UPDATE currencies SET name=$2,enabled=$3,create_on_registration=$4,sort_order=$5,version=version+1,updated_at=now() WHERE code=$1 AND version=$6 RETURNING `+columns, code, strings.TrimSpace(u.Name), u.Enabled, u.CreateOnRegistration, u.SortOrder, u.Version))
	if errors.Is(err, pgx.ErrNoRows) {
		return Currency{}, ErrConflict
	}
	return result, err
}
func (s *Service) Parse(ctx context.Context, code, value string) (int64, error) {
	decimals, err := wallet.ResolveDecimals(ctx, s.pool, code, true)
	if err != nil {
		return 0, err
	}
	return wallet.ParseDisplayAmount(value, decimals)
}
