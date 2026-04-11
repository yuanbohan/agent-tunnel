package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"yuanbohan/tunnel/internal/relay/store/postgres"
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
	cfg, err := loadConfig(env.getenv, args)
	if err != nil {
		return err
	}

	db, err := env.openDB(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.PingContext(context.Background()); err != nil {
		return err
	}

	if strings.TrimSpace(cfg.Baseline) != "" {
		if err := postgres.BaselineMigrations(context.Background(), db, cfg.SchemaDir, cfg.Baseline); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(env.stdout, "baselined relay schema through %s\n", cfg.Baseline)
		return nil
	}

	if err := postgres.RunMigrations(context.Background(), db, cfg.SchemaDir); err != nil {
		return err
	}
	_, _ = io.WriteString(env.stdout, "relay schema migrations applied\n")
	return nil
}

func loadConfig(getenv func(string) string, args []string) (config, error) {
	cfg := config{
		DatabaseURL: envValue(getenv, "RELAY_DATABASE_URL"),
	}

	fs := flag.NewFlagSet("agentunnel-relay-migrate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.SchemaDir, "schema-dir", cfg.SchemaDir, "directory containing ordered relay schema SQL files")
	fs.StringVar(&cfg.Baseline, "baseline", "", "mark migrations up to and including this version as applied without executing SQL")
	if err := fs.Parse(args); err != nil {
		return config{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return config{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	switch {
	case cfg.DatabaseURL == "":
		return config{}, usagef("missing RELAY_DATABASE_URL")
	case strings.TrimSpace(cfg.SchemaDir) == "":
		return config{}, usagef("missing --schema-dir")
	default:
		return cfg, nil
	}
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

func envValue(getenv func(string) string, key string) string {
	if getenv == nil {
		return ""
	}
	return strings.TrimSpace(getenv(key))
}
