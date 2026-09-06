package credit

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidSpinConfig = errors.New("invalid spin configuration")
var ErrSpinConfigNotFound = errors.New("spin configuration not found")

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
	rows, err := service.pool.Query(ctx, `SELECT id::text,code,title,enabled,cost_currency,cost_minor,sort_order FROM spin_configs WHERE NOT deleted AND (NOT $1 OR enabled=true) ORDER BY sort_order,created_at`, enabledOnly)
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
	rows, err := service.pool.Query(ctx, `SELECT id::text,label,reward_currency,reward_minor,weight,disabled FROM spin_prizes WHERE spin_id=$1 ORDER BY sort_order,id`, spinID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpinPrize{}
	for rows.Next() {
		var v SpinPrize
		if err := rows.Scan(&v.ID, &v.Label, &v.Currency, &v.AmountMinor, &v.Weight, &v.Disabled); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (service *Service) ReplaceSpinConfigs(ctx context.Context, items []SpinConfig) ([]SpinConfig, error) {
	return service.saveSpinConfigs(ctx, items, true)
}

func (service *Service) SaveSpinConfig(ctx context.Context, item SpinConfig) (SpinConfig, error) {
	items, err := service.saveSpinConfigs(ctx, []SpinConfig{item}, false)
	if err != nil {
		return SpinConfig{}, err
	}
	return items[0], nil
}

func (service *Service) saveSpinConfigs(ctx context.Context, items []SpinConfig, replace bool) ([]SpinConfig, error) {
	if len(items) == 0 {
		return nil, ErrInvalidSpinConfig
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	keep := []string{}
	saved := []SpinConfig{}
	for _, v := range items {
		v.Prizes = append([]SpinPrize(nil), v.Prizes...)
		v.Title = strings.TrimSpace(v.Title)
		v.CostCurrency = strings.ToUpper(strings.TrimSpace(v.CostCurrency))
		if v.Title == "" || !validCurrency(v.CostCurrency) || v.CostMinor <= 0 || len(v.Prizes) == 0 {
			return nil, ErrInvalidSpinConfig
		}
		creating := v.ID == ""
		if v.ID == "" {
			v.ID = uuid.NewString()
			v.Code = "spin-" + strings.ReplaceAll(v.ID, "-", "")
			_, err = tx.Exec(ctx, `INSERT INTO spin_configs(id,code,title,enabled,cost_currency,cost_minor,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7)`, v.ID, v.Code, v.Title, v.Enabled, v.CostCurrency, v.CostMinor, v.SortOrder)
		} else {
			err = tx.QueryRow(ctx, `UPDATE spin_configs SET title=$2,enabled=$3,cost_currency=$4,cost_minor=$5,sort_order=$6,updated_at=now() WHERE id=$1 AND NOT deleted RETURNING code`, v.ID, v.Title, v.Enabled, v.CostCurrency, v.CostMinor, v.SortOrder).Scan(&v.Code)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrSpinConfigNotFound
			}
		}
		if err != nil {
			return nil, err
		}
		prizeIDs := []string{}
		seen := map[string]bool{}
		for i, p := range v.Prizes {
			p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
			if p.Label == "" || !validCurrency(p.Currency) || p.AmountMinor <= 0 || p.Weight < 0 {
				return nil, ErrInvalidSpinConfig
			}
			if p.ID == "" {
				p.ID = uuid.NewString()
				_, err = tx.Exec(ctx, `INSERT INTO spin_prizes(id,spin_id,code,label,reward_currency,reward_minor,weight,sort_order,disabled) VALUES($1::uuid,$2,$1::text,$3,$4,$5,$6,$7,$8)`, p.ID, v.ID, p.Label, p.Currency, p.AmountMinor, p.Weight, i, p.Disabled)
			} else {
				if creating || seen[p.ID] {
					return nil, ErrInvalidSpinConfig
				}
				var id string
				err = tx.QueryRow(ctx, `UPDATE spin_prizes SET label=$3,reward_currency=$4,reward_minor=$5,weight=$6,sort_order=$7,disabled=$8 WHERE id=$1 AND spin_id=$2 RETURNING id::text`, p.ID, v.ID, p.Label, p.Currency, p.AmountMinor, p.Weight, i, p.Disabled).Scan(&id)
				if errors.Is(err, pgx.ErrNoRows) {
					return nil, ErrInvalidSpinConfig
				}
			}
			if err != nil {
				return nil, err
			}
			seen[p.ID] = true
			prizeIDs = append(prizeIDs, p.ID)
			v.Prizes[i] = p
		}
		if _, ok := choosePrize(v.Prizes); v.Enabled && !ok {
			return nil, ErrInvalidSpinConfig
		}
		if _, err = tx.Exec(ctx, `DELETE FROM spin_prizes WHERE spin_id=$1 AND NOT(id::text=ANY($2))`, v.ID, prizeIDs); err != nil {
			return nil, err
		}
		keep = append(keep, v.ID)
		saved = append(saved, v)
	}
	if replace {
		if _, err = tx.Exec(ctx, `UPDATE spin_configs SET enabled=false WHERE NOT(id::text=ANY($1))`, keep); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	if replace {
		return service.ListSpinConfigs(ctx, false)
	}
	return saved, nil
}

func findSpinConfig(ctx context.Context, tx pgx.Tx, idOrCode string, enabledOnly bool) (SpinConfig, error) {
	var v SpinConfig
	err := tx.QueryRow(ctx, `SELECT id::text,code,title,enabled,cost_currency,cost_minor,sort_order FROM spin_configs WHERE (id::text=$1 OR code=$1) AND NOT deleted AND (NOT $2 OR enabled=true) FOR SHARE`, idOrCode, enabledOnly).Scan(&v.ID, &v.Code, &v.Title, &v.Enabled, &v.CostCurrency, &v.CostMinor, &v.SortOrder)
	if err != nil {
		return v, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,label,reward_currency,reward_minor,weight,disabled FROM spin_prizes WHERE spin_id=$1 ORDER BY sort_order,id`, v.ID)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var p SpinPrize
		if err := rows.Scan(&p.ID, &p.Label, &p.Currency, &p.AmountMinor, &p.Weight, &p.Disabled); err != nil {
			return v, err
		}
		v.Prizes = append(v.Prizes, p)
	}
	return v, rows.Err()
}
