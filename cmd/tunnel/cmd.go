package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

type runHandler func(context.Context, runArgs, io.Writer, io.Writer) error

type loginHandler func(context.Context, authLoginArgs, io.Reader, io.Writer, io.Writer) error

type logoutHandler func(context.Context, io.Writer) error

type statusHandler func(context.Context, io.Writer) error

type commandHandlers struct {
	run    runHandler
	login  loginHandler
	logout logoutHandler
	status statusHandler
}

func defaultCommandHandlers() commandHandlers {
	return commandHandlers{
		run:    runTunnelSession,
		login:  runAuthLogin,
		logout: runAuthLogout,
		status: runAuthStatus,
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
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newVersionCmd())
	return root
}

func newRunCmd(runFn runHandler) *cobra.Command {
	var (
		verbose bool
		label   string
		baseURL string
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

			return runFn(cmd.Context(), runArgs{
				Verbose:      verbose,
				Label:        label,
				BaseURL:      resolved,
				Launcher:     args[0],
				LauncherArgs: append([]string(nil), args[1:]...),
			}, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "print relay connection status on successful startup")
	cmd.Flags().StringVarP(&label, "label", "l", "", "optional session label for relay clients")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "relay base URL")
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

func parseRunArgsForTest(args []string) (runArgs, error) {
	var captured runArgs
	handlers := defaultCommandHandlers()
	handlers.run = func(_ context.Context, parsed runArgs, _, _ io.Writer) error {
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
