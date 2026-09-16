package betting

import "time"

var shanghaiTimeZone = time.FixedZone("Asia/Shanghai", 8*60*60)

// luluPrimeTime returns whether 星海逃杀 is inside the fixed Beijing
// 20:00-21:00 prime time window. The start is inclusive, the end exclusive.
func luluPrimeTime(now time.Time) bool {
	local := now.In(shanghaiTimeZone)
	return local.Hour() == 20
}

// luluXDYRestrictedPlays returns the plays that may not be bet during prime
// time. Prime time only allows 单双 (odd_even), 躲避 (dodge) and 直选 (direct);
// 上下 and 左右 are blocked. Other game types are never restricted.
func luluXDYRestrictedPlays(now time.Time) []string {
	if !luluPrimeTime(now) {
		return nil
	}
	return []string{"up_down", "left_right"}
}
