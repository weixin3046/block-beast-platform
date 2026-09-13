package betting

import (
	"testing"
	"time"
)

func TestLuluXDYBettingClosedDuringShanghaiMaintenanceWindow(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	for _, tc := range []struct {
		name   string
		now    time.Time
		closed bool
	}{
		{name: "before window", now: time.Date(2026, 9, 13, 19, 59, 59, 0, shanghai), closed: false},
		{name: "window opens", now: time.Date(2026, 9, 13, 20, 0, 0, 0, shanghai), closed: true},
		{name: "during window", now: time.Date(2026, 9, 13, 20, 30, 0, 0, shanghai), closed: true},
		{name: "window closes", now: time.Date(2026, 9, 13, 21, 0, 0, 0, shanghai), closed: false},
		{name: "utc input", now: time.Date(2026, 9, 13, 12, 30, 0, 0, time.UTC), closed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := luluXDYBettingClosedAt(tc.now); got != tc.closed {
				t.Fatalf("luluXDYBettingClosedAt(%s) = %t, want %t", tc.now, got, tc.closed)
			}
		})
	}
}
