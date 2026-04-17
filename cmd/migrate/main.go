package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"yuanbohan/tunnel/internal/migration"
)

type runtimeEnv struct {
	getenv func(string) string
	openDB func(string) (*sql.DB, error)
	stdout io.Writer
}

type config struct {
	DatabaseURL string
	SchemaDir   string
	Baseline    string
	EnvFile     string
}

type migrateFlags struct {
	EnvFile   string
	SchemaDir string
	Baseline  string
}

type usageError struct {
	msg string
}

func (e usageError) Error() string {
	return e.msg
}

func usagef(format string, args ...any) error {
	return usageError{msg: fmt.Sprintf(format, args...)}
}

func main() {
	if err := run(os.Args[1:], defaultRuntimeEnv()); err != nil {
		log.Fatal(err)
	}
}

func defaultRuntimeEnv() runtimeEnv {
	return runtimeEnv{
		getenv: os.Getenv,
		openDB: func(dsn string) (*sql.DB, error) {
			return sql.Open("pgx", dsn)
		},
		stdout: os.Stdout,
	}
}

func run(args []string, env runtimeEnv) error {
	stdout := env.stdout
	if stdout == nil {
		stdout = io.Discard
	}
	cmd := newMigrateCmd(env)
	cmd.SetOut(stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func newMigrateCmd(env runtimeEnv) *cobra.Command {
	var flags migrateFlags
	cmd := &cobra.Command{
		Use:   "relay-migrate",
		Short: "Apply relay schema migrations to PostgreSQL",
		Long: `Apply or baseline relay schema migrations in PostgreSQL.

Required input:
  - RELAY_DATABASE_URL
  - --schema-dir

Optional input:
  - --env-file to read RELAY_* values from a literal KEY=VALUE file
  - --baseline to mark migrations through a version as already applied

The env file takes precedence over the current process environment for RELAY_*
keys.`,
		Example: `  relay-migrate --schema-dir ./schema
  relay-migrate --env-file /etc/agentunnel/relay.env --schema-dir /etc/agentunnel/schema
  relay-migrate --schema-dir ./schema --baseline 0002_operator_audit.sql`,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := finalizeMigrateConfig(flags, env.getenv)
			if err != nil {
				return err
			}
			return executeMigration(c.Context(), cfg, env)
		},
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usagef("%v", err)
	})
	applyMigrateFlags(cmd.Flags(), &flags)
	if err := cmd.MarkFlagRequired("schema-dir"); err != nil {
		panic(err)
	}
	return cmd
}

func applyMigrateFlags(fs *pflag.FlagSet, flags *migrateFlags) {
	fs.StringVarP(&flags.EnvFile, "env-file", "f", "", "read RELAY_* values from this env file before falling back to the process environment")
	fs.StringVarP(&flags.SchemaDir, "schema-dir", "s", "", "directory containing ordered relay schema SQL files (required)")
	fs.StringVarP(&flags.Baseline, "baseline", "b", "", "mark migrations up to and including this version as applied without executing SQL")
}

func finalizeMigrateConfig(flags migrateFlags, getenv func(string) string) (config, error) {
	cfg := config{
		EnvFile:   flags.EnvFile,
		SchemaDir: flags.SchemaDir,
		Baseline:  flags.Baseline,
	}
	lookupEnv := getenv
	if strings.TrimSpace(cfg.EnvFile) != "" {
		fileEnv, err := loadLiteralEnvFile(cfg.EnvFile)
		if err != nil {
			return config{}, usagef("load --env-file %s: %v", cfg.EnvFile, err)
		}
		lookupEnv = func(key string) string {
			if value, ok := fileEnv[key]; ok {
				return value
			}
			return envValue(getenv, key)
		}
	}
	cfg.DatabaseURL = envValue(lookupEnv, "RELAY_DATABASE_URL")
	switch {
	case cfg.DatabaseURL == "":
		return config{}, usagef("missing RELAY_DATABASE_URL")
	case strings.TrimSpace(cfg.SchemaDir) == "":
		return config{}, usagef(`required flag(s) "schema-dir" not set`)
	default:
		return cfg, nil
	}
}

func loadConfig(getenv func(string) string, args []string) (config, error) {
	var flags migrateFlags
	fs := pflag.NewFlagSet("relay-migrate", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	applyMigrateFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return config{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return config{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return finalizeMigrateConfig(flags, getenv)
}

func executeMigration(ctx context.Context, cfg config, env runtimeEnv) error {
	db, err := env.openDB(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return err
	}

	stdout := env.stdout
	if stdout == nil {
		stdout = io.Discard
	}

	if strings.TrimSpace(cfg.Baseline) != "" {
		if err := migration.BaselineMigrations(ctx, db, cfg.SchemaDir, cfg.Baseline); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "baselined relay schema through %s\n", cfg.Baseline)
		return nil
	}

	if err := migration.RunMigrations(ctx, db, cfg.SchemaDir); err != nil {
		return err
	}
	_, _ = io.WriteString(stdout, "relay schema migrations applied\n")
	return nil
}

func loadLiteralEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#"):
			continue
		}

		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			return nil, fmt.Errorf("line %d is not KEY=VALUE", lineNo)
		}

		key := line[:idx]
		if !isValidEnvKey(key) {
			return nil, fmt.Errorf("line %d has invalid env key %q", lineNo, key)
		}
		values[key] = line[idx+1:]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func isValidEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		ch := key[i]
		switch {
		case i == 0 && ch >= 'A' && ch <= 'Z':
		case i == 0 && ch >= 'a' && ch <= 'z':
		case i == 0 && ch == '_':
		case i > 0 && ch >= 'A' && ch <= 'Z':
		case i > 0 && ch >= 'a' && ch <= 'z':
		case i > 0 && ch >= '0' && ch <= '9':
		case i > 0 && ch == '_':
		default:
			return false
		}
	}
	return true
}

func envValue(getenv func(string) string, key string) string {
	if getenv == nil {
		return ""
	}
	return strings.TrimSpace(getenv(key))
}
