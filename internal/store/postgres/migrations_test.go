package postgres

import (
	"strings"
	"testing"
)

// These tests need no database: they check the migration files that are embedded in the binary.

func TestMigrationsAreNumberedWithoutGaps(t *testing.T) {
	t.Parallel()
	ms, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no migrations")
	}
	for i, m := range ms {
		if m.version != i+1 {
			t.Errorf("migration %s is number %d, want %d: numbers must run 1, 2, 3 without gaps", m.name, m.version, i+1)
		}
		if strings.TrimSpace(m.sql) == "" {
			t.Errorf("migration %s is empty", m.name)
		}
	}
	if got := SchemaVersion(); got != len(ms) {
		t.Errorf("SchemaVersion = %d, want %d", got, len(ms))
	}
}

func TestMigrationsOnlyTouchTheEngineSchema(t *testing.T) {
	t.Parallel()
	ms, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		for _, bad := range []string{"DROP SCHEMA", "public.", "DROP DATABASE", "TRUNCATE"} {
			if strings.Contains(strings.ToUpper(m.sql), strings.ToUpper(bad)) {
				t.Errorf("migration %s contains %q; the engine owns only its own schema and never drops data", m.name, bad)
			}
		}
	}
}
