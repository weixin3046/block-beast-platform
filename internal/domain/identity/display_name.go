package identity

import "strings"

var restrictedDisplayNameTerms = []string{
	"风云",
	"上分",
	"下分",
	"客服",
	"管理员",
	"管理",
	"官方",
	"平台",
	"系统",
	"运营",
	"GM",
	"admin",
	"administrator",
	"service",
	"system",
	"official",
	"lulu",
	"LULU",
	"LUlu",
	"luLU",
	"luLu",
	"lULu",
	"lULU",
	"LuLU",
	"LuLu",
	"LUlu",
	"LUlu",
}

// HasRestrictedDisplayNameTerm reports whether a nickname could impersonate a platform or support account.
func HasRestrictedDisplayNameTerm(displayName string) bool {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return false
	}
	for _, term := range restrictedDisplayNameTerms {
		if strings.Contains(displayName, term) {
			return true
		}
	}
	return false
}
