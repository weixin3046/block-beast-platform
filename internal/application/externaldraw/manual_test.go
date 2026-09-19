package externaldraw

import "testing"

func TestValidateManual(t *testing.T) {
	for _, tc := range []struct {
		game   string
		result []int
		valid  bool
	}{
		{GameGreenSprint, []int{5}, true}, {GameAngryFeather, []int{2}, true}, {GameStarSea, []int{1, 3, 7}, true},
		{GameGreenSprint, []int{7}, false}, {GameGreenSprint, []int{1, 2}, false}, {GameAngryFeather, []int{3}, false},
		{GameStarSea, []int{1, 1}, false}, {GameStarSea, []int{0}, false}, {"unknown", []int{1}, false}, {GameStarSea, nil, false},
	} {
		_, _, _, err := validateManual(ManualInput{Game: tc.game, Issue: "15693", Result: tc.result, Reason: "用户提供的核实结果"})
		if (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	for _, in := range []ManualInput{
		{Game: GameGreenSprint, Issue: "0", Result: []int{5}, Reason: "test"},
		{Game: GameGreenSprint, Issue: "15693", Result: []int{5}, Reason: "  "},
	} {
		if _, _, _, err := validateManual(in); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}
