package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const migrationAdvisoryLockKey int64 = 1_843_077_011

func RunMigrations(ctx context.Context, db *sql.DB, schemaDir string) error {
	if err := ensureSchemaMigrationsTable(ctx, db); err != nil {
		return err
	}

	entries, err := readMigrationNames(schemaDir)
	if err != nil {
		return err
	}
	return withMigrationLock(ctx, db, func() error {
		applied, err := appliedMigrationVersions(ctx, db)
		if err != nil {
			return err
		}
		if err := ensureAppliedMigrationPrefix(entries, applied); err != nil {
			return err
		}
		for _, name := range entries {
			if _, ok := applied[name]; ok {
				continue
			}

			contents, err := os.ReadFile(filepath.Join(schemaDir, name))
			if err != nil {
				return err
			}
			if err := applyMigration(ctx, db, name, string(contents)); err != nil {
				return fmt.Errorf("apply migration %s: %w", name, err)
			}
		}
		return nil
	})
}

func BaselineMigrations(ctx context.Context, db *sql.DB, schemaDir string, version string) error {
	if err := ensureSchemaMigrationsTable(ctx, db); err != nil {
		return err
	}

	names, err := readMigrationNames(schemaDir)
	if err != nil {
		return err
	}
	baselineNames, err := baselineVersionsUntil(names, version)
	if err != nil {
		return err
	}

	baselineSet := make(map[string]struct{}, len(baselineNames))
	for _, name := range baselineNames {
		baselineSet[name] = struct{}{}
	}

	return withMigrationLock(ctx, db, func() error {
		applied, err := appliedMigrationVersions(ctx, db)
		if err != nil {
			return err
		}
		if err := ensureAppliedMigrationPrefix(names, applied); err != nil {
			return err
		}
		for version := range applied {
			if _, ok := baselineSet[version]; ok {
				continue
			}
			return fmt.Errorf("cannot baseline %s: migration %s is already applied", baselineNames[len(baselineNames)-1], version)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()

		for _, name := range baselineNames {
			if _, ok := applied[name]; ok {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				insert into schema_migrations(version, applied_at)
				values ($1, now())
			`, name); err != nil {
				return err
			}
		}

		return tx.Commit()
	})
}

func ensureSchemaMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		create table if not exists schema_migrations (
			version text primary key,
			applied_at timestamptz not null
		)
	`)
	return err
}

func readMigrationNames(schemaDir string) ([]string, error) {
	if strings.TrimSpace(schemaDir) == "" {
		return nil, fmt.Errorf("missing schema directory")
	}

	entries, err := os.ReadDir(schemaDir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func baselineVersionsUntil(names []string, version string) ([]string, error) {
	target := strings.TrimSpace(version)
	if target == "" {
		return nil, fmt.Errorf("missing baseline version")
	}
	idx := -1
	for i, name := range names {
		if name == target {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("baseline version %s not found", target)
	}
	return append([]string(nil), names[:idx+1]...), nil
}

func appliedMigrationVersions(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `select version from schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]struct{})
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		out[version] = struct{}{}
	}
	return out, rows.Err()
}

func ensureAppliedMigrationPrefix(names []string, applied map[string]struct{}) error {
	known := make(map[string]struct{}, len(names))
	seenGap := false
	for _, name := range names {
		known[name] = struct{}{}
		_, isApplied := applied[name]
		if !isApplied {
			seenGap = true
			continue
		}
		if seenGap {
			return fmt.Errorf("migration %s is already applied before earlier migrations", name)
		}
	}
	for version := range applied {
		if _, ok := known[version]; ok {
			continue
		}
		return fmt.Errorf("migration %s is applied but not present in schema dir", version)
	}
	return nil
}

func withMigrationLock(ctx context.Context, db *sql.DB, fn func() error) error {
	if _, err := db.ExecContext(ctx, `select pg_advisory_lock($1)`, migrationAdvisoryLockKey); err != nil {
		return err
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `select pg_advisory_unlock($1)`, migrationAdvisoryLockKey)
	}()
	return fn()
}

func applyMigration(ctx context.Context, db *sql.DB, version, sqlText string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, sqlText); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		insert into schema_migrations(version, applied_at)
		values ($1, now())
	`, version); err != nil {
		return err
	}
	return tx.Commit()
}
