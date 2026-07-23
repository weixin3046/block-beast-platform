package agent

import "testing"

func TestContainsPathLabel(t *testing.T) {
	if !containsPathLabel("a.b_c.d", "b_c") {
		t.Fatal("expected path label to be found")
	}
	if containsPathLabel("a.b_c.d", "b") {
		t.Fatal("partial path label must not match")
	}
}
