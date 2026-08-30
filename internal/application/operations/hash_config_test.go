package operations

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestValidateHashConfigUpdateRequiresFixedRoomsAndValidLimits(t *testing.T) {
	roomIDs := []string{
		"94000000-0000-4000-8000-000000000001",
		"95000000-0000-4000-8000-000000000001",
		"96000000-0000-4000-8000-000000000001",
		"97000000-0000-4000-8000-000000000001",
		"98000000-0000-4000-8000-000000000001",
		"98500000-0000-4000-8000-000000000001",
	}
	input := HashConfigUpdate{ExpectedVersion: 1, Rooms: make([]HashRoomConfig, 0, 6)}
	for _, id := range roomIDs {
		input.Rooms = append(input.Rooms, HashRoomConfig{
			ID: id, Name: "房间", CurrencyConfigs: []HashCurrencyConfig{{
				Currency: "CUSTOM", GuessMultiplier: 1, GuessDivisor: 1,
				DodgeMultiplier: 1, DodgeDivisor: 1, RoadMultiplier: 1, RoadDivisor: 1,
				GuessMaxStakeMinor: 100, DodgeMaxStakeMinor: 100, RoadMaxStakeMinor: 100,
				MinStakeMinor: 1,
			}},
		})
	}
	if err := validateHashConfigUpdate(input); err != nil {
		t.Fatalf("valid fixed config: %v", err)
	}

	invalidRoom := input
	invalidRoom.Rooms = append([]HashRoomConfig(nil), input.Rooms...)
	invalidRoom.Rooms[0].ID = "00000000-0000-4000-8000-000000000001"
	if err := validateHashConfigUpdate(invalidRoom); !errors.Is(err, ErrInvalidHashConfig) {
		t.Fatalf("unknown room error = %v", err)
	}

	invalidLimit := input
	invalidLimit.Rooms = append([]HashRoomConfig(nil), input.Rooms...)
	invalidLimit.Rooms[0].CurrencyConfigs = append([]HashCurrencyConfig(nil), input.Rooms[0].CurrencyConfigs...)
	invalidLimit.Rooms[0].CurrencyConfigs[0].MinStakeMinor = 101
	if err := validateHashConfigUpdate(invalidLimit); !errors.Is(err, ErrInvalidHashConfig) {
		t.Fatalf("min above max error = %v", err)
	}
}

func TestSeededHashConfigHasSixFixedIntervals(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	config, err := NewService(pool).GetHashConfig(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Rooms) != 6 {
		t.Fatalf("rooms = %d, want 6", len(config.Rooms))
	}
	want := []string{"hash_9", "hash_13", "hash_17", "hash_19", "hash_23", "hash_29"}
	for _, room := range config.Rooms {
		got := make([]string, 0, len(room.GameTypes))
		for _, gameType := range room.GameTypes {
			got = append(got, gameType.Code)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("room %s game types = %v, want %v", room.Code, got, want)
		}
		if room.Code == "hash_rate_1940" {
			for _, currency := range room.CurrencyConfigs {
				if currency.Currency == "USDT" && (currency.MinStakeMinor != 100_000 || currency.GuessMaxStakeMinor != 50_000_000) {
					t.Fatalf("1.94 USDT limits = min %d max %d", currency.MinStakeMinor, currency.GuessMaxStakeMinor)
				}
			}
		}
	}
}
