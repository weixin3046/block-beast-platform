package credit

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var spinCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)
var ErrInvalidSpinConfig = errors.New("invalid spin configuration")

type SpinConfig struct {
	ID           string      `json:"id"`
	Code         string      `json:"code"`
	Title        string      `json:"title"`
	Enabled      bool        `json:"enabled"`
	CostCurrency string      `json:"cost_currency"`
	CostMinor    int64       `json:"cost_minor"`
	SortOrder    int         `json:"sort_order"`
	Prizes       []SpinPrize `json:"prizes"`
}

func (service *Service) ListSpinConfigs(ctx context.Context, enabledOnly bool) ([]SpinConfig, error) {
	rows, err := service.pool.Query(ctx, `SELECT id::text,code,title,enabled,cost_currency,cost_minor,sort_order FROM spin_configs WHERE NOT $1 OR enabled=true ORDER BY sort_order,created_at`, enabledOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpinConfig{}
	for rows.Next() {
		var v SpinConfig
		if err := rows.Scan(&v.ID, &v.Code, &v.Title, &v.Enabled, &v.CostCurrency, &v.CostMinor, &v.SortOrder); err != nil {
			return nil, err
		}
		prizes, err := service.spinPrizes(ctx, v.ID)
		if err != nil {
			return nil, err
		}
		v.Prizes = prizes
		out = append(out, v)
	}
	return out, rows.Err()
}
func (service *Service) spinPrizes(ctx context.Context, spinID string) ([]SpinPrize, error) {
	rows, err := service.pool.Query(ctx, `SELECT code,label,reward_currency,reward_minor,weight FROM spin_prizes WHERE spin_id=$1 ORDER BY sort_order,id`, spinID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpinPrize{}
	for rows.Next() {
		var v SpinPrize
		if err := rows.Scan(&v.ID, &v.Label, &v.Currency, &v.AmountMinor, &v.Weight); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (service *Service) ReplaceSpinConfigs(ctx context.Context, items []SpinConfig) ([]SpinConfig, error) {
	if len(items) == 0 {
		return nil, ErrInvalidSpinConfig
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	keep := []string{}
	for _, v := range items {
		v.Code = strings.ToLower(strings.TrimSpace(v.Code))
		v.Title = strings.TrimSpace(v.Title)
		v.CostCurrency = strings.ToUpper(strings.TrimSpace(v.CostCurrency))
		if !spinCodePattern.MatchString(v.Code) || v.Title == "" || !validCurrency(v.CostCurrency) || v.CostMinor <= 0 || len(v.Prizes) == 0 {
			return nil, ErrInvalidSpinConfig
		}
		if _, ok := choosePrize(v.Prizes); !ok {
			return nil, ErrInvalidSpinConfig
		}
		if v.ID == "" {
			v.ID = uuid.NewString()
			_, err = tx.Exec(ctx, `INSERT INTO spin_configs(id,code,title,enabled,cost_currency,cost_minor,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7)`, v.ID, v.Code, v.Title, v.Enabled, v.CostCurrency, v.CostMinor, v.SortOrder)
		} else {
			_, err = tx.Exec(ctx, `UPDATE spin_configs SET code=$2,title=$3,enabled=$4,cost_currency=$5,cost_minor=$6,sort_order=$7,updated_at=now() WHERE id=$1`, v.ID, v.Code, v.Title, v.Enabled, v.CostCurrency, v.CostMinor, v.SortOrder)
		}
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM spin_prizes WHERE spin_id=$1`, v.ID); err != nil {
			return nil, err
		}
		for i, p := range v.Prizes {
			p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
			if _, err = tx.Exec(ctx, `INSERT INTO spin_prizes(id,spin_id,code,label,reward_currency,reward_minor,weight,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.NewString(), v.ID, p.ID, p.Label, p.Currency, p.AmountMinor, p.Weight, i); err != nil {
				return nil, err
			}
		}
		keep = append(keep, v.ID)
	}
	if _, err = tx.Exec(ctx, `UPDATE spin_configs SET enabled=false WHERE NOT(id::text=ANY($1))`, keep); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return service.ListSpinConfigs(ctx, false)
}

func findSpinConfig(ctx context.Context, tx pgx.Tx, idOrCode string, enabledOnly bool) (SpinConfig, error) {
	var v SpinConfig
	err := tx.QueryRow(ctx, `SELECT id::text,code,title,enabled,cost_currency,cost_minor,sort_order FROM spin_configs WHERE (id::text=$1 OR code=$1) AND (NOT $2 OR enabled=true) FOR SHARE`, idOrCode, enabledOnly).Scan(&v.ID, &v.Code, &v.Title, &v.Enabled, &v.CostCurrency, &v.CostMinor, &v.SortOrder)
	if err != nil {
		return v, err
	}
	rows, err := tx.Query(ctx, `SELECT code,label,reward_currency,reward_minor,weight FROM spin_prizes WHERE spin_id=$1 ORDER BY sort_order,id`, v.ID)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var p SpinPrize
		if err := rows.Scan(&p.ID, &p.Label, &p.Currency, &p.AmountMinor, &p.Weight); err != nil {
			return v, err
		}
		v.Prizes = append(v.Prizes, p)
	}
	return v, rows.Err()
}
