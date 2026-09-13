package betting

import "time"

var shanghaiTimeZone = time.FixedZone("Asia/Shanghai", 8*60*60)

// luluXDYBettingClosedAt returns whether 星海逃杀 is in its daily maintenance
// window. The start is inclusive and the end is exclusive.
func luluXDYBettingClosedAt(now time.Time) bool {
	local := now.In(shanghaiTimeZone)
	return local.Hour() == 20
}
