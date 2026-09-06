package task

import (
	"errors"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/application/credit"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool          *pgxpool.Pool
	creditService *credit.Service
	now           func() time.Time
}

var chinaTimeZone = time.FixedZone("China Standard Time", 8*60*60)

var ErrTaskConfigNotFound = errors.New("task config not found")
var ErrTaskNotCompleted = errors.New("task has not reached its claim threshold today")
var ErrTaskAlreadyClaimed = errors.New("task reward has already been claimed today")

func NewService(pool *pgxpool.Pool, creditService *credit.Service) *Service {
	return &Service{pool: pool, creditService: creditService, now: time.Now}
}

type BetTask struct {
	Title                string       `json:"title"`
	PeriodType           string       `json:"period_type"`
	SortOrder            int          `json:"sort_order"`
	MaxCompleteCount     int          `json:"max_complete_count"`
	CompleteCount        int          `json:"complete_count"`
	Rewards              []TaskReward `json:"rewards"`
	ID                   string       `json:"id"`
	AccumulationCurrency string       `json:"accumulation_currency"`
	RewardCurrency       string       `json:"reward_currency"`
	ThresholdMinor       int64        `json:"threshold_minor"`
	RewardMinor          int64        `json:"reward_minor"`
	ProgressMinor        int64        `json:"progress_minor"`
	Completed            bool         `json:"completed"`
	Rewarded             bool         `json:"rewarded"`
}

type BetTaskConfig struct {
	Title                string       `json:"title"`
	PeriodType           string       `json:"period_type"`
	SortOrder            int          `json:"sort_order"`
	MaxCompleteCount     int          `json:"max_complete_count"`
	Rewards              []TaskReward `json:"rewards"`
	ID                   string       `json:"id"`
	AccumulationCurrency string       `json:"accumulation_currency"`
	RewardCurrency       string       `json:"reward_currency"`
	ThresholdMinor       int64        `json:"threshold_minor"`
	RewardMinor          int64        `json:"reward_minor"`
	Enabled              bool         `json:"enabled"`
}

type BetTaskClaim struct {
	CompleteCount     int          `json:"complete_count"`
	Rewards           []TaskReward `json:"rewards"`
	TaskID            string       `json:"task_id"`
	BetDate           string       `json:"bet_date"`
	RewardCurrency    string       `json:"reward_currency"`
	RewardMinor       int64        `json:"reward_minor"`
	BalanceAfterMinor int64        `json:"balance_after_minor"`
	ClaimedAt         time.Time    `json:"claimed_at"`
}

func validAccumulationCurrency(v string) bool {
	return strings.TrimSpace(v) != ""
}
func validRewardCurrency(v string) bool {
	return strings.TrimSpace(v) != ""
}

func (service *Service) betDate() string {
	return service.betDateAt(service.now())
}

func (service *Service) betDateAt(value time.Time) string {
	return value.In(chinaTimeZone).Format("2006-01-02")
}
