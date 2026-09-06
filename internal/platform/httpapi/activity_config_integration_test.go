package httpapi

import (
	"context"
	"errors"
	"github.com/block-beast/platform/internal/application/credit"
	"github.com/block-beast/platform/internal/application/task"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestSingleConfigPersistence(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	spins := credit.NewService(p)
	makeSpin := func() credit.SpinConfig {
		v, e := spins.SaveSpinConfig(ctx, credit.SpinConfig{Title: "test generated", Enabled: true, CostCurrency: "POINTS", CostMinor: 1500, Prizes: []credit.SpinPrize{{Label: "test", Currency: "USDT", AmountMinor: 1500000, Weight: 1}}})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			p2, e := pgxpool.New(ctx, dsn)
			if e == nil {
				defer p2.Close()
				p2.Exec(ctx, "DELETE FROM spin_configs WHERE id=$1", v.ID)
			}
		})
		if _, e = uuid.Parse(v.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = uuid.Parse(v.Prizes[0].ID); e != nil {
			t.Fatal(e)
		}
		return v
	}
	a, b := makeSpin(), makeSpin()
	prizeID := a.Prizes[0].ID
	a.Title = "updated"
	a.Prizes[0].AmountMinor = 2000000
	a, err = spins.SaveSpinConfig(ctx, a)
	if err != nil || a.Prizes[0].ID != prizeID {
		t.Fatal(a, err)
	}
	a.Prizes[0].ID = b.Prizes[0].ID
	if _, err = spins.SaveSpinConfig(ctx, a); err == nil {
		t.Fatal("foreign prize accepted")
	}
	a.ID = uuid.NewString()
	if _, err = spins.SaveSpinConfig(ctx, a); !errors.Is(err, credit.ErrSpinConfigNotFound) {
		t.Fatal(err)
	}
	var enabled bool
	if err = p.QueryRow(ctx, "SELECT enabled FROM spin_configs WHERE id=$1", b.ID).Scan(&enabled); err != nil || !enabled {
		t.Fatal("other spin changed", err)
	}
	tasks := task.NewService(p, spins)
	v := task.BetTaskConfig{AccumulationCurrency: "POINTS", ThresholdMinor: time.Now().UnixNano() / 1000, RewardCurrency: "USDT", RewardMinor: 1500000, Enabled: true}
	x, err := tasks.SaveBetTaskConfig(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Exec(ctx, "DELETE FROM bet_task_configs WHERE id=$1", x.ID)
	v.ThresholdMinor++
	y, err := tasks.SaveBetTaskConfig(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Exec(ctx, "DELETE FROM bet_task_configs WHERE id=$1", y.ID)
	x.RewardMinor = 2000000
	x, err = tasks.SaveBetTaskConfig(ctx, x)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(ctx, "SELECT enabled FROM bet_task_configs WHERE id=$1", y.ID).Scan(&enabled); err != nil || !enabled {
		t.Fatal("other task changed", err)
	}
	x.ID = uuid.NewString()
	if _, err = tasks.SaveBetTaskConfig(ctx, x); !errors.Is(err, task.ErrTaskConfigNotFound) {
		t.Fatal(err)
	}
}
