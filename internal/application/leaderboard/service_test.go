package leaderboard

import (
	"context"
	"errors"
	"testing"
)

func TestListValidatesPeriodAndCurrencyBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}
	if _, err := service.List(context.Background(), "month", "USDT", 10); !errors.Is(err, ErrInvalidPeriod) {
		t.Fatalf("period error = %v", err)
	}
	if _, err := service.List(context.Background(), "today", "", 10); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("currency error = %v", err)
	}
}

func TestValidateRulesRejectsOverlaps(t *testing.T) {
	err := validateRules(RewardRuleSet{PeriodType: "daily", Currency: "USDT", Rules: []RewardRule{{RankFrom: 1, RankTo: 2, RewardCurrency: "POINTS", RewardMinor: 1}, {RankFrom: 2, RankTo: 3, RewardCurrency: "USDT", RewardMinor: 1}}})
	if !errors.Is(err, ErrInvalidRewardRules) {
		t.Fatalf("rules error = %v", err)
	}
}
