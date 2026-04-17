package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/spf13/cobra"
	relayconfig "yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/logx"
	"yuanbohan/tunnel/internal/relay/bootstrap"
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
	inviteCreate  func(context.Context, inviteCreateConfig) error
	inviteDisable func(context.Context, inviteDisableConfig) error
	inviteList    func(context.Context, inviteListConfig) error
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
	stdout := env.stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := env.stderr
	if stderr == nil {
		stderr = io.Discard
	}
	root := newRootCmd(env, handlers)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)
	err := root.Execute()
	if err != nil && strings.Contains(err.Error(), "unknown command") {
		return usagef("%v", err)
	}
	return err
}

func newRootCmd(env runtimeEnv, handlers commandHandlers) *cobra.Command {
	root := &cobra.Command{
		Use:   "relay",
		Short: "Tunnel relay server and operator CLI",
		Long: `Run the relay server and local-only operator commands.

The public server entrypoint is "relay serve". It requires:
  - RELAY_DATABASE_URL
  - RELAY_APP_SECRET
  - RELAY_OPERATOR_TOKEN

The operator commands under "relay invite" and "relay user" are intentionally
local-only. Run them on the relay host after "relay serve" is already running.
They use RELAY_OPERATOR_TOKEN and connect to RELAY_LISTEN_ADDR (default
127.0.0.1:8586).`,
		Example: `  relay serve --listen-addr 127.0.0.1:8586
  relay invite create --count 3 --expires-in 7d
  relay invite disable --code AB2C3D
  relay user delete --username alice`,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	wrapFlagErrors(root)
	root.AddCommand(newVersionCmd())
	root.AddCommand(newServeCmd(env, handlers))
	root.AddCommand(newInviteCmd(env, handlers))
	root.AddCommand(newUserCmd(env, handlers))
	return root
}

func wrapFlagErrors(cmd *cobra.Command) {
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usagef("%v", err)
	})
}

func markFlagRequired(cmd *cobra.Command, name string) {
	if err := cmd.MarkFlagRequired(name); err != nil {
		panic(err)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "version",
		Short:         "Print the relay version",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return writeVersion(c.OutOrStdout())
		},
	}
}

func newServeCmd(env runtimeEnv, handlers commandHandlers) *cobra.Command {
	var cfg serveConfig
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the relay HTTP and WebSocket service",
		Long: `Start the relay API and WebSocket service.

Required environment variables:
  - RELAY_DATABASE_URL
  - RELAY_APP_SECRET
  - RELAY_OPERATOR_TOKEN

Optional environment variables:
  - RELAY_LISTEN_ADDR (default 127.0.0.1:8586)
  - RELAY_LOG_FILE`,
		Example: `  relay serve
  relay serve --listen-addr 127.0.0.1:8586
  RELAY_LOG_FILE=/var/log/relay.log relay serve`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			final, err := finalizeServeConfig(cfg, env.getenv)
			if err != nil {
				return err
			}
			return handlers.serve(c.Context(), final)
		},
	}
	wrapFlagErrors(cmd)
	applyServeFlags(cmd.Flags(), &cfg)
	return cmd
}

func newInviteCmd(env runtimeEnv, handlers commandHandlers) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "Manage invite codes through the running relay",
		Long: `Manage invite codes through the relay's local-only operator API.

These commands must run on the relay host after "relay serve" is already
running. They require RELAY_OPERATOR_TOKEN and use RELAY_LISTEN_ADDR
(default 127.0.0.1:8586).`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	wrapFlagErrors(cmd)
	cmd.AddCommand(newInviteCreateCmd(env, handlers))
	cmd.AddCommand(newInviteDisableCmd(env, handlers))
	cmd.AddCommand(newInviteListCmd(env, handlers))
	return cmd
}

func newInviteCreateCmd(env runtimeEnv, handlers commandHandlers) *cobra.Command {
	var flags inviteCreateFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create one or more invite codes",
		Long: `Create invite codes through the relay's local-only operator API.

Requires:
  - RELAY_OPERATOR_TOKEN

Uses RELAY_LISTEN_ADDR when set, otherwise defaults to 127.0.0.1:8586.`,
		Example: `  relay invite create
  relay invite create --count 3 --expires-in 7d`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := finalizeInviteCreateConfig(flags, env.getenv)
			if err != nil {
				return err
			}
			return handlers.inviteCreate(c.Context(), cfg)
		},
	}
	wrapFlagErrors(cmd)
	applyInviteCreateFlags(cmd.Flags(), &flags)
	return cmd
}

func newInviteDisableCmd(env runtimeEnv, handlers commandHandlers) *cobra.Command {
	var flags inviteDisableFlags
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable an existing invite code",
		Long: `Disable an invite code through the relay's local-only operator API.

Requires:
  - RELAY_OPERATOR_TOKEN
  - --code

Uses RELAY_LISTEN_ADDR when set, otherwise defaults to 127.0.0.1:8586.`,
		Example:       `  relay invite disable --code AB2C3D`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := finalizeInviteDisableConfig(flags, env.getenv)
			if err != nil {
				return err
			}
			return handlers.inviteDisable(c.Context(), cfg)
		},
	}
	wrapFlagErrors(cmd)
	applyInviteDisableFlags(cmd.Flags(), &flags)
	markFlagRequired(cmd, "code")
	return cmd
}

func newInviteListCmd(env runtimeEnv, handlers commandHandlers) *cobra.Command {
	var flags inviteListFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List invite codes and their status",
		Long: `List invite codes through the relay's local-only operator API.

Requires:
  - RELAY_OPERATOR_TOKEN

Uses RELAY_LISTEN_ADDR when set, otherwise defaults to 127.0.0.1:8586.`,
		Example:       `  relay invite list`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := finalizeInviteListConfig(flags, env.getenv)
			if err != nil {
				return err
			}
			return handlers.inviteList(c.Context(), cfg)
		},
	}
	wrapFlagErrors(cmd)
	applyInviteListFlags(cmd.Flags(), &flags)
	return cmd
}

func newUserCmd(env runtimeEnv, handlers commandHandlers) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage user accounts through the running relay",
		Long: `Manage user accounts through the relay's local-only operator API.

These commands must run on the relay host after "relay serve" is already
running. They require RELAY_OPERATOR_TOKEN and use RELAY_LISTEN_ADDR
(default 127.0.0.1:8586).`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	wrapFlagErrors(cmd)
	cmd.AddCommand(newUserDeleteCmd(env, handlers))
	return cmd
}

func newUserDeleteCmd(env runtimeEnv, handlers commandHandlers) *cobra.Command {
	var flags userDeleteFlags
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a user account",
		Long: `Delete a user account through the relay's local-only operator API.

Requires:
  - RELAY_OPERATOR_TOKEN
  - --username

Uses RELAY_LISTEN_ADDR when set, otherwise defaults to 127.0.0.1:8586.`,
		Example:       `  relay user delete --username alice`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := finalizeUserDeleteConfig(flags, env.getenv)
			if err != nil {
				return err
			}
			return handlers.userDelete(c.Context(), cfg)
		},
	}
	wrapFlagErrors(cmd)
	applyUserDeleteFlags(cmd.Flags(), &flags)
	markFlagRequired(cmd, "username")
	return cmd
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
			logSink := env.stderr
			if cfg.LogFile != "" {
				f, err := os.OpenFile(cfg.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
				if err != nil {
					return fmt.Errorf("open log file %q: %w", cfg.LogFile, err)
				}
				defer f.Close()
				logSink = f
			}
			logx.Setup(logSink)

			db, err := env.openDB(relayconfig.RelayDatabaseURL())
			if err != nil {
				return err
			}
			defer db.Close()
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := db.PingContext(pingCtx); err != nil {
				return err
			}

			handler, err := bootstrap.NewServeHandler(db)
			if err != nil {
				return err
			}

			return startRelay(handler, env.listen, env.serveHTTP)
		},
		inviteCreate: func(ctx context.Context, cfg inviteCreateConfig) error {
			return runInviteCreate(ctx, newHTTPOperatorClient(cfg.RelayAddr, cfg.OperatorToken, env.httpClient), cfg, env.stdout)
		},
		inviteDisable: func(ctx context.Context, cfg inviteDisableConfig) error {
			return runInviteDisable(ctx, newHTTPOperatorClient(cfg.RelayAddr, cfg.OperatorToken, env.httpClient), cfg, env.stdout)
		},
		inviteList: func(ctx context.Context, cfg inviteListConfig) error {
			return runInviteList(ctx, newHTTPOperatorClient(cfg.RelayAddr, cfg.OperatorToken, env.httpClient), cfg, env.stdout)
		},
		userDelete: func(ctx context.Context, cfg userDeleteConfig) error {
			return runUserDelete(ctx, newHTTPOperatorClient(cfg.RelayAddr, cfg.OperatorToken, env.httpClient), cfg, env.stdout)
		},
	}
}
