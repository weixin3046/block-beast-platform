package identity

import "testing"

func TestHasRestrictedDisplayNameTerm(t *testing.T) {
	for _, name := range []string{"平台客服", "官方运营", "系统管理员", "我的管理号"} {
		if !HasRestrictedDisplayNameTerm(name) {
			t.Fatalf("display name %q was not rejected", name)
		}
	}
	for _, name := range []string{"小明", "玩家001", "星海猎手"} {
		if HasRestrictedDisplayNameTerm(name) {
			t.Fatalf("display name %q was rejected", name)
		}
	}
}
