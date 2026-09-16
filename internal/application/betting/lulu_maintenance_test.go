package betting

import (
	"slices"
	"testing"
	"time"
)

func TestLuluXDYRestrictedPlaysDuringShanghaiPrimeTime(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	for _, tc := range []struct {
		name       string
		now        time.Time
		restricted []string
	}{
		{name: "before window", now: time.Date(2026, 9, 13, 19, 59, 59, 0, shanghai), restricted: nil},
		{name: "window opens", now: time.Date(2026, 9, 13, 20, 0, 0, 0, shanghai), restricted: []string{"direct", "up_down", "left_right"}},
		{name: "during window", now: time.Date(2026, 9, 13, 20, 30, 0, 0, shanghai), restricted: []string{"direct", "up_down", "left_right"}},
		{name: "window closes", now: time.Date(2026, 9, 13, 21, 0, 0, 0, shanghai), restricted: nil},
		{name: "utc input", now: time.Date(2026, 9, 13, 12, 30, 0, 0, time.UTC), restricted: []string{"direct", "up_down", "left_right"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := luluXDYRestrictedPlays(tc.now)
			if !slices.Equal(got, tc.restricted) {
				t.Fatalf("luluXDYRestrictedPlays(%s) = %v, want %v", tc.now, got, tc.restricted)
			}
		})
	}
}

func TestLuluPrimeTimeWindow(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	if luluPrimeTime(time.Date(2026, 9, 13, 20, 0, 0, 0, shanghai)) != true {
		t.Fatal("20:00 must be inside prime time")
	}
	if luluPrimeTime(time.Date(2026, 9, 13, 20, 59, 59, 0, shanghai)) != true {
		t.Fatal("20:59:59 must be inside prime time")
	}
	if luluPrimeTime(time.Date(2026, 9, 13, 21, 0, 0, 0, shanghai)) != false {
		t.Fatal("21:00 must be outside prime time")
	}
}
