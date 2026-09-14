package operations

import "testing"

func TestAssembleLuluMenusGroupsOrderedRows(t *testing.T) {
	menus := assembleLuluMenus([]luluMenuRow{
		{gameCode: "lulu-xdy", gameName: "星海", roomID: "r1", roomCode: "rate", roomName: "1.94", playCode: "direct", playName: "直选", currency: "POINTS", multiplier: 750, divisor: 100, min: 1, max: 2000},
		{gameCode: "lulu-xdy", gameName: "星海", roomID: "r1", roomCode: "rate", roomName: "1.94", playCode: "direct", playName: "直选", currency: "USDT", multiplier: 750, divisor: 100, min: 1, max: 2000},
		{gameCode: "lulu-race", gameName: "绿茵", roomID: "r2", roomCode: "rate2", roomName: "1.95", playCode: "dodge", playName: "躲避", dodge: true, currency: "POINTS", multiplier: 112, divisor: 100, min: 1, max: 10000},
	})
	if len(menus.Games) != 2 || len(menus.Games[0].Rooms) != 1 || len(menus.Games[0].Rooms[0].Plays) != 1 {
		t.Fatalf("unexpected menu hierarchy: %#v", menus)
	}
	configs := menus.Games[0].Rooms[0].Plays[0].CurrencyConfigs
	if len(configs) != 2 || configs[1].Currency != "USDT" {
		t.Fatalf("currency configs = %#v", configs)
	}
	if !menus.Games[1].Rooms[0].Plays[0].DodgeMode {
		t.Fatal("dodge mode was not preserved")
	}
}
