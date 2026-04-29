package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

type runHandler func(context.Context, io.Reader, runArgs, io.Writer, io.Writer) error

type loginHandler func(context.Context, authLoginArgs, io.Reader, io.Writer, io.Writer) error

type logoutHandler func(context.Context, io.Writer) error

type statusHandler func(context.Context, io.Writer) error

type updateHandler func(context.Context, io.Writer, io.Writer) error

type rollbackHandler func(context.Context, io.Writer, io.Writer) error
type sessionListHandler func(context.Context, sessionCommandArgs, io.Writer, io.Writer) error
type sessionStopHandler func(context.Context, sessionCommandArgs, string, io.Writer, io.Writer) error

type commandHandlers struct {
	run         runHandler
	login       loginHandler
	logout      logoutHandler
	status      statusHandler
	update      updateHandler
	rollback    rollbackHandler
	sessionList sessionListHandler
	sessionStop sessionStopHandler
}

func defaultCommandHandlers() commandHandlers {
	return commandHandlers{
		run:         runTunnelSession,
		login:       runAuthLogin,
		logout:      runAuthLogout,
		status:      runAuthStatus,
		update:      runManualUpdate,
		rollback:    runManualRollback,
		sessionList: runSessionList,
		sessionStop: runSessionStop,
	}
}

func newRootCmd(handlers commandHandlers) *cobra.Command {
	var showVersion bool

	root := &cobra.Command{
		Use:           "tunnel",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if showVersion {
				return writeVersion(cmd.OutOrStdout())
			}
			return cmd.Help()
		},
	}
	root.Flags().BoolVar(&showVersion, "version", false, "print tunnel version and exit")
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), rootHelpText())
	})
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(rootHelpText(), "%v", err)
	})
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(newRunCmd(handlers.run))
	root.AddCommand(newAuthCmd(handlers))
	root.AddCommand(newSessionCmd(handlers))
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newUpdateCmd(handlers.update))
	root.AddCommand(newRollbackCmd(handlers.rollback))
	root.AddCommand(newVersionCmd())
	return root
}

func newRunCmd(runFn runHandler) *cobra.Command {
	var (
		verbose         bool
		label           string
		baseURL         string
		launchSource    string
		launchRequestID string
	)

	cmd := &cobra.Command{
		Use:           "run [flags] <command> [args...]",
		Short:         "Launch a local command and connect it to the relay",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveBaseURL(baseURL, osEnv)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return usageWithHelp(runHelpText(), "missing launcher command")
			}

			return runFn(cmd.Context(), cmd.InOrStdin(), runArgs{
				Verbose:         verbose,
				Label:           label,
				BaseURL:         resolved,
				LaunchSource:    launchSource,
				LaunchRequestID: launchRequestID,
				Launcher:        args[0],
				LauncherArgs:    append([]string(nil), args[1:]...),
			}, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "print relay connection status on successful startup")
	cmd.Flags().StringVarP(&label, "label", "l", "", "optional session label for relay clients")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "relay base URL")
	cmd.Flags().StringVar(&launchSource, "launch-source", "", "internal launch source metadata")
	cmd.Flags().StringVar(&launchRequestID, "launch-request-id", "", "internal launch request correlation id")
	_ = cmd.Flags().MarkHidden("launch-source")
	_ = cmd.Flags().MarkHidden("launch-request-id")
	cmd.Flags().SetInterspersed(false)
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), runHelpText())
	})
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(runHelpText(), "%v", err)
	})
	return cmd
}

func newAuthCmd(handlers commandHandlers) *cobra.Command {
	authCmd := &cobra.Command{
		Use:           "auth",
		Short:         "Manage local tunnel authentication",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	authCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), authHelpText())
	})
	authCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(authHelpText(), "%v", err)
	})
	authCmd.AddCommand(newAuthLoginCmd(handlers.login))
	authCmd.AddCommand(newAuthLogoutCmd(handlers.logout))
	authCmd.AddCommand(newAuthStatusCmd(handlers.status))
	return authCmd
}

func newSessionCmd(handlers commandHandlers) *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:           "session",
		Short:         "List and stop live tunnel sessions",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().StringVar(&baseURL, "base-url", "", "relay base URL")
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), sessionHelpText())
	})
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(sessionHelpText(), "%v", err)
	})

	listCmd := &cobra.Command{
		Use:           "list",
		Short:         "List live sessions",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := resolveBaseURL(baseURL, osEnv)
			if err != nil {
				return err
			}
			return handlers.sessionList(cmd.Context(), sessionCommandArgs{BaseURL: resolved}, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	listCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), sessionListHelpText())
	})
	listCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(sessionListHelpText(), "%v", err)
	})

	stopCmd := &cobra.Command{
		Use:           "stop <session-id>",
		Short:         "Stop a live session",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageWithHelp(sessionStopHelpText(), "expected exactly one session id")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveBaseURL(baseURL, osEnv)
			if err != nil {
				return err
			}
			return handlers.sessionStop(cmd.Context(), sessionCommandArgs{BaseURL: resolved}, args[0], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	stopCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), sessionStopHelpText())
	})
	stopCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(sessionStopHelpText(), "%v", err)
	})

	cmd.AddCommand(listCmd)
	cmd.AddCommand(stopCmd)
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "version",
		Short:         "Print the tunnel version",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeVersion(cmd.OutOrStdout())
		},
	}
}

func newUpdateCmd(updateFn updateHandler) *cobra.Command {
	return &cobra.Command{
		Use:           "update",
		Short:         "Update tunnel to the latest official release",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return updateFn(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func newRollbackCmd(rollbackFn rollbackHandler) *cobra.Command {
	return &cobra.Command{
		Use:           "rollback",
		Short:         "Roll back tunnel to the previous official release",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return rollbackFn(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func parseRunArgsForTest(args []string) (runArgs, error) {
	var captured runArgs
	handlers := defaultCommandHandlers()
	handlers.run = func(_ context.Context, _ io.Reader, parsed runArgs, _, _ io.Writer) error {
		captured = parsed
		return nil
	}

	cmd := newRootCmd(handlers)
	cmd.SetIn(emptyReader{})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		return runArgs{}, err
	}
	return captured, nil
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) {
	return 0, io.EOF
}

func newDaemonCmd() *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:           "daemon",
		Short:         "Manage the background mobile-launch daemon",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), daemonHelpText())
	})
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(daemonHelpText(), "%v", err)
	})
	cmd.PersistentFlags().StringVar(&baseURL, "base-url", "",
		fmt.Sprintf("relay base URL (fallback: %s, default: %s)", tunnelBaseURLEnv, defaultTunnelBaseURL))

	startCmd := &cobra.Command{
		Use:           "start",
		Short:         "Start the background daemon",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonStart(cmd.Context(), baseURL, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	startCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), daemonStartHelpText())
	})
	startCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(daemonStartHelpText(), "%v", err)
	})
	cmd.AddCommand(startCmd)
	cmd.AddCommand(&cobra.Command{
		Use:           "status",
		Short:         "Show daemon status",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonStatus(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "stop",
		Short:         "Stop the background daemon",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonStop(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "doctor",
		Short:         "Run daemon diagnostics",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonDoctor(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "open",
		Short:         "Open the daemon tmux workspace",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonOpen(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "close",
		Short:         "Close one open daemon tmux workspace view",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonClose(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "sessions",
		Short:         "List daemon tmux sessions",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonSessions(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	pairCmd := &cobra.Command{
		Use:           "pair",
		Short:         "Create a pairing invitation",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonPair(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	pairCmd.AddCommand(&cobra.Command{
		Use:           "pending",
		Short:         "List pending pairing responses",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonPairPending(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	pairCmd.AddCommand(&cobra.Command{
		Use:   "confirm <invitation-id> <sas>",
		Short: "Confirm a pending pairing response",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 2 {
				return usageWithHelp(daemonHelpText(), "expected invitation id and SAS")
			}
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonPairConfirm(cmd.Context(), args[0], args[1], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	cmd.AddCommand(pairCmd)
	cmd.AddCommand(&cobra.Command{
		Use:           "devices",
		Short:         "List paired Android devices",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonDevices(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "revoke <fingerprint>",
		Short: "Revoke a paired Android device",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageWithHelp(daemonHelpText(), "expected exactly one device fingerprint")
			}
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonRevoke(cmd.Context(), args[0], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	internalCmd := &cobra.Command{
		Use:           "internal-run",
		Hidden:        true,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonInternal(cmd.Context(), baseURL, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.AddCommand(internalCmd)

	return cmd
}
