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
	"庄家",
	"荷官",
	// 新增
	"博彩",
	"赌",
	"下注",
	"盘口",
	"赔率",
	"打水",
	"水位",
	"滚盘",
	"信用盘",
	"现金盘",
	"出款",
	"入款",
	"充值",
	"回收",
	"变现",
	"返水",
	"返点",
	"代理",
	"推广",
	"代充",
	"兑换",
	"洗码",
	"码农",
	"赌球",
	"棋牌",
	"赌场",
	"娱乐城",
	"稳赢",
	"必中",
	"包赢",
	"root",
	"master",
	"op",
	"operator",
	"moderator",
	"mod",
	"manager",
	"help",
	"support",
	"bot",
	"微信",
	"wx",
	"vx",
	"qq",
	"加我",
	"联系",
	"私聊",
	"扫码",
	"客服号",
	"客服微信",
}

// HasRestrictedDisplayNameTerm reports whether a nickname could impersonate a platform or support account.
func HasRestrictedDisplayNameTerm(displayName string) bool {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return false
	}
	lowerName := strings.ToLower(displayName)
	for _, term := range restrictedDisplayNameTerms {
		lowerTerm := strings.ToLower(term)
		if strings.Contains(lowerName, lowerTerm) {
			return true
		}
	}
	return false
}
