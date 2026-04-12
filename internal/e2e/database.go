package e2e

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func openTestDatabase(t *testing.T, dsn string) *sql.DB {
	t.Helper()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func repoRoot(t *testing.T) string {
	t.Helper()

	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("stat repo root: %v", err)
	}
	return root
}

func schemaDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(repoRoot(t), "schema")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("stat schema dir: %v", err)
	}
	return dir
}

func requireNonEmptyEnv(t *testing.T, key string) string {
	t.Helper()

	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		t.Fatalf("%s is required for local e2e runs", key)
	}
	return value
}

func mustStatPath(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
}

func buildOutputPath(dir, name string) string {
	return filepath.Join(dir, name)
}

func trimLine(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf("command returned no non-empty output")
}
