package main

import (
	"context"
	"database/sql"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"yuanbohan/tunnel/internal/relay"
)

type runtimeEnv struct {
	getenv     func(string) string
	stdout     io.Writer
	stderr     io.Writer
	openDB     func(string) (*sql.DB, error)
	httpClient *http.Client
	listen     func(network, address string) (net.Listener, error)
	serveHTTP  func(*http.Server, net.Listener) error
}

type commandHandlers struct {
	serve         func(context.Context, serveConfig) error
	migrate       func(context.Context, migrateConfig) error
	inviteCreate  func(context.Context, inviteCreateConfig) error
	inviteDisable func(context.Context, inviteDisableConfig) error
	userDelete    func(context.Context, userDeleteConfig) error
}

func defaultRuntimeEnv() runtimeEnv {
	return runtimeEnv{
		getenv: os.Getenv,
		stdout: os.Stdout,
		stderr: os.Stderr,
		openDB: func(dsn string) (*sql.DB, error) {
			return sql.Open("pgx", dsn)
		},
		httpClient: &http.Client{Timeout: 10 * time.Second},
		listen:     net.Listen,
		serveHTTP: func(srv *http.Server, ln net.Listener) error {
			return srv.Serve(ln)
		},
	}
}

func run(args []string, env runtimeEnv) error {
	return runWithHandlers(args, env, newCommandHandlers(env))
}

func runWithHandlers(args []string, env runtimeEnv, handlers commandHandlers) error {
	if env.getenv == nil {
		env.getenv = func(string) string { return "" }
	}
	if len(args) == 0 {
		return usagef("%s", rootUsage())
	}

	switch args[0] {
	case "serve":
		cfg, err := loadServeConfig(env.getenv, args[1:])
		if err != nil {
			return err
		}
		return handlers.serve(context.Background(), cfg)
	case "migrate":
		cfg, err := loadMigrateConfig(env.getenv, args[1:])
		if err != nil {
			return err
		}
		return handlers.migrate(context.Background(), cfg)
	case "invite":
		if len(args) < 2 {
			return usagef("%s", inviteUsage())
		}
		switch args[1] {
		case "create":
			cfg, err := loadInviteCreateConfig(env.getenv, args[2:])
			if err != nil {
				return err
			}
			return handlers.inviteCreate(context.Background(), cfg)
		case "disable":
			cfg, err := loadInviteDisableConfig(env.getenv, args[2:])
			if err != nil {
				return err
			}
			return handlers.inviteDisable(context.Background(), cfg)
		default:
			return usagef("%s", inviteUsage())
		}
	case "user":
		if len(args) < 2 || args[1] != "delete" {
			return usagef("%s", userUsage())
		}
		cfg, err := loadUserDeleteConfig(env.getenv, args[2:])
		if err != nil {
			return err
		}
		return handlers.userDelete(context.Background(), cfg)
	default:
		return usagef("%s", rootUsage())
	}
}

func newCommandHandlers(env runtimeEnv) commandHandlers {
	if env.stdout == nil {
		env.stdout = io.Discard
	}
	if env.stderr == nil {
		env.stderr = io.Discard
	}
	if env.httpClient == nil {
		env.httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	return commandHandlers{
		serve: func(ctx context.Context, cfg serveConfig) error {
			db, err := env.openDB(cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer db.Close()
			if err := db.PingContext(ctx); err != nil {
				return err
			}

			digester, err := relay.NewSecretDigester(cfg.AppSecret)
			if err != nil {
				return err
			}
			store := relay.NewPostgresStore(db)
			registry := relay.NewRegistry()
			logger := relay.NewLogger(env.stderr)

			appAuth := relay.NewAppAuthService(store, digester, relay.DefaultPasswordHasher())
			agentTokens := relay.NewAgentTokenService(store, digester)
			operator := relay.NewOperatorService(store, digester)
			handler := relay.NewHandler(relay.HandlerConfig{
				Registry:         registry,
				AppAuth:          appAuth,
				AgentTokens:      agentTokens,
				Operator:         operator,
				OperatorToken:    cfg.OperatorToken,
				RegisterThrottle: relay.NewRegisterThrottle(5, 10*time.Minute),
				Logger:           logger,
			})

			return startRelay(cfg, handler, logger, env.listen, env.serveHTTP)
		},
		migrate: func(ctx context.Context, cfg migrateConfig) error {
			db, err := env.openDB(cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer db.Close()
			if err := db.PingContext(ctx); err != nil {
				return err
			}
			return relay.RunMigrations(ctx, db)
		},
		inviteCreate: func(ctx context.Context, cfg inviteCreateConfig) error {
			return runInviteCreate(ctx, newHTTPOperatorClient(cfg.RelayAddr, cfg.OperatorToken, env.httpClient), cfg, env.stdout)
		},
		inviteDisable: func(ctx context.Context, cfg inviteDisableConfig) error {
			return runInviteDisable(ctx, newHTTPOperatorClient(cfg.RelayAddr, cfg.OperatorToken, env.httpClient), cfg, env.stdout)
		},
		userDelete: func(ctx context.Context, cfg userDeleteConfig) error {
			return runUserDelete(ctx, newHTTPOperatorClient(cfg.RelayAddr, cfg.OperatorToken, env.httpClient), cfg, env.stdout)
		},
	}
}

func rootUsage() string {
	return strings.TrimSpace(`
Usage:
  relay serve            Start the relay HTTP and WebSocket service
  relay migrate          Apply PostgreSQL schema migrations
  relay invite create    Create one or more invite codes through the running relay
  relay invite disable   Disable an unconsumed invite code through the running relay
  relay user delete      Delete a user account through the running relay
`)
}

func inviteUsage() string {
	return strings.TrimSpace(`
Usage:
  relay invite create    Create invite codes with whole-day expiry
  relay invite disable   Disable an existing invite code
`)
}

func userUsage() string {
	return "Usage:\n  relay user delete      Delete a user account"
}
