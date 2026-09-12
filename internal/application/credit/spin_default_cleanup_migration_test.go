package credit

import (
	"os"
	"strings"
	"testing"
)

func TestSpinDefaultConfigCleanupMigrationContract(t *testing.T) {
	migration, err := os.ReadFile("../../../migrations/0085_remove_default_lucky_spin.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(migration)
	for _, required := range []string{
		"DELETE FROM spin_configs",
		"code = 'lucky-spin'",
		"NOT EXISTS",
		"FROM lucky_spin_records",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration must contain %q", required)
		}
	}
}
