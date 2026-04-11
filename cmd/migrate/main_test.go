package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func testEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadConfigDefaultsAndRequirements(t *testing.T) {
	_, err := loadConfig(testEnv(nil), nil)
	if err == nil {
		t.Fatal("expected missing env to fail")
	}

	cfg, err := loadConfig(testEnv(map[string]string{
		"RELAY_DATABASE_URL": "postgres://relay",
	}), []string{"--schema-dir", "/tmp/schema"})
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}
	if cfg.SchemaDir != "/tmp/schema" {
		t.Fatalf("SchemaDir = %q, want /tmp/schema", cfg.SchemaDir)
	}
}

func TestLoadConfigAllowsBaselineAndSchemaDir(t *testing.T) {
	cfg, err := loadConfig(testEnv(map[string]string{
		"RELAY_DATABASE_URL": "postgres://relay",
	}), []string{"--schema-dir", "/tmp/schema", "--baseline", "0002_operator_audit.sql"})
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}
	if cfg.SchemaDir != "/tmp/schema" {
		t.Fatalf("SchemaDir = %q, want /tmp/schema", cfg.SchemaDir)
	}
	if cfg.Baseline != "0002_operator_audit.sql" {
		t.Fatalf("Baseline = %q, want 0002_operator_audit.sql", cfg.Baseline)
	}
}

func TestLoadConfigRequiresSchemaDir(t *testing.T) {
	_, err := loadConfig(testEnv(map[string]string{
		"RELAY_DATABASE_URL": "postgres://relay",
	}), nil)
	if err == nil || !strings.Contains(err.Error(), "missing --schema-dir") {
		t.Fatalf("loadConfig error = %v, want missing --schema-dir", err)
	}
}

func TestRunPrintsBaselineMessage(t *testing.T) {
	var out bytes.Buffer
	openCalls := 0
	err := run([]string{"--schema-dir", "../testdata/schema", "--baseline", "0002_operator_audit.sql"}, runtimeEnv{
		getenv: testEnv(map[string]string{
			"RELAY_DATABASE_URL": "postgres://relay",
		}),
		openDB: func(string) (*sql.DB, error) {
			openCalls++
			return nil, errors.New("open failed")
		},
		stdout: &out,
	})
	if err == nil || !strings.Contains(err.Error(), "open failed") {
		t.Fatalf("run error = %v, want open failed", err)
	}
	if openCalls != 1 {
		t.Fatalf("openCalls = %d, want 1", openCalls)
	}
}

func TestUsagefReturnsUsageError(t *testing.T) {
	err := usagef("hello %s", "world")
	var usageErr usageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("errors.As returned false for %#v", err)
	}
	if usageErr.Error() != "hello world" {
		t.Fatalf("usage error = %q, want hello world", usageErr.Error())
	}
}

var _ = context.Background
