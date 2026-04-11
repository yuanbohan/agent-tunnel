package postgres

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadMigrationNamesSortsSQLFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"0002_second.sql",
		"0001_first.sql",
		"README.md",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("-- test\n"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}

	got, err := readMigrationNames(dir)
	if err != nil {
		t.Fatalf("readMigrationNames returned error: %v", err)
	}
	want := []string{"0001_first.sql", "0002_second.sql"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migration names = %#v, want %#v", got, want)
	}
}

func TestBaselineVersionsUntilReturnsPrefix(t *testing.T) {
	names := []string{"0001_first.sql", "0002_second.sql", "0003_third.sql"}

	got, err := baselineVersionsUntil(names, "0002_second.sql")
	if err != nil {
		t.Fatalf("baselineVersionsUntil returned error: %v", err)
	}
	want := []string{"0001_first.sql", "0002_second.sql"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("baseline versions = %#v, want %#v", got, want)
	}
}

func TestBaselineVersionsUntilRejectsUnknownVersion(t *testing.T) {
	_, err := baselineVersionsUntil([]string{"0001_first.sql"}, "0009_missing.sql")
	if err == nil {
		t.Fatal("expected missing baseline version to fail")
	}
}
