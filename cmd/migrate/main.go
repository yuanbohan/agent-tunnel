package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
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

	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return err
	}

	if strings.TrimSpace(cfg.Baseline) != "" {
		if err := migration.BaselineMigrations(context.Background(), db, cfg.SchemaDir, cfg.Baseline); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(env.stdout, "baselined relay schema through %s\n", cfg.Baseline)
		return nil
	}

	if err := migration.RunMigrations(context.Background(), db, cfg.SchemaDir); err != nil {
		return err
	}
	_, _ = io.WriteString(env.stdout, "relay schema migrations applied\n")
	return nil
}

func loadConfig(getenv func(string) string, args []string) (config, error) {
	cfg := config{}

	fs := flag.NewFlagSet("relay-migrate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.EnvFile, "env-file", "", "read RELAY_* values from this env file before falling back to the process environment")
	fs.StringVar(&cfg.SchemaDir, "schema-dir", cfg.SchemaDir, "directory containing ordered relay schema SQL files")
	fs.StringVar(&cfg.Baseline, "baseline", "", "mark migrations up to and including this version as applied without executing SQL")
	if err := fs.Parse(args); err != nil {
		return config{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return config{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
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
		return config{}, usagef("missing --schema-dir")
	default:
		return cfg, nil
	}
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
