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
	root.AddCommand(newPairCmd())
	root.AddCommand(newWorkspaceCmd())
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
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), sessionHelpText())
	})
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(sessionHelpText(), "%v", err)
	})

	var listJSON bool
	listCmd := &cobra.Command{
		Use:           "list",
		Short:         "List live sessions",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runList := func() error {
				return handlers.sessionList(cmd.Context(), sessionCommandArgs{JSON: listJSON}, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			if listJSON {
				return runSessionJSONCommand(cmd.OutOrStdout(), runList)
			}
			return runList()
		},
	}
	listCmd.Flags().BoolVar(&listJSON, "json", false, "print live sessions as JSON")
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
			return handlers.sessionStop(cmd.Context(), sessionCommandArgs{}, args[0], cmd.OutOrStdout(), cmd.ErrOrStderr())
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

	var startJSON bool
	startCmd := &cobra.Command{
		Use:           "start",
		Short:         "Start the background daemon",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if startJSON {
				return runDaemonJSONCommand(cmd.OutOrStdout(), func() error {
					return runDaemonStartWithOptions(cmd.Context(), baseURL, cmd.OutOrStdout(), cmd.ErrOrStderr(), daemonStartOptions{JSON: true})
				})
			}
			return runDaemonStartWithOptions(cmd.Context(), baseURL, cmd.OutOrStdout(), cmd.ErrOrStderr(), daemonStartOptions{JSON: startJSON})
		},
	}
	startCmd.Flags().BoolVar(&startJSON, "json", false, "print daemon status as JSON")
	startCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), daemonStartHelpText())
	})
	startCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(daemonStartHelpText(), "%v", err)
	})
	cmd.AddCommand(startCmd)
	var statusJSON bool
	statusCmd := &cobra.Command{
		Use:           "status",
		Short:         "Show daemon status",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if statusJSON {
				return runDaemonJSONCommand(cmd.OutOrStdout(), func() error {
					return runDaemonStatusWithOptions(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), true)
				})
			}
			return runDaemonStatusWithOptions(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), statusJSON)
		},
	}
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "print daemon status as JSON")
	cmd.AddCommand(statusCmd)
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
	var doctorJSON bool
	doctorCmd := &cobra.Command{
		Use:           "doctor",
		Short:         "Run daemon diagnostics",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if doctorJSON {
				return runDaemonJSONCommand(cmd.OutOrStdout(), func() error {
					return runDaemonDoctorWithOptions(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), true)
				})
			}
			return runDaemonDoctorWithOptions(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), doctorJSON)
		},
	}
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "print daemon diagnostics as JSON")
	cmd.AddCommand(doctorCmd)
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

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "workspace",
		Short:         "Open or close the Tunnel workspace view",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), workspaceHelpText())
	})
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(workspaceHelpText(), "%v", err)
	})
	openCmd := &cobra.Command{
		Use:           "open",
		Short:         "Open the Tunnel workspace view",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkspaceOpen(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	openCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), workspaceOpenHelpText())
	})
	closeCmd := &cobra.Command{
		Use:           "close",
		Short:         "Close one open workspace view",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkspaceClose(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	closeCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), workspaceCloseHelpText())
	})
	cmd.AddCommand(openCmd)
	cmd.AddCommand(closeCmd)
	return cmd
}

func newPairCmd() *cobra.Command {
	var pairJSON bool
	pairCmd := &cobra.Command{
		Use:           "pair",
		Short:         "Pair and manage trusted client devices",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if pairJSON {
				return runDaemonJSONCommand(cmd.OutOrStdout(), func() error {
					return runPair(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), true)
				})
			}
			if !pairInteractiveTerminal(cmd.InOrStdin(), cmd.OutOrStdout()) {
				return guidancef("interactive pairing requires a terminal; use `tunnel pair --json` for automation")
			}
			return runPair(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), false)
		},
	}
	pairCmd.Flags().BoolVar(&pairJSON, "json", false, "print pairing invitation as JSON")
	pairCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), pairHelpText())
	})
	pairCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(pairHelpText(), "%v", err)
	})
	var devicesJSON bool
	devicesCmd := &cobra.Command{
		Use:           "devices",
		Short:         "List trusted client devices",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if devicesJSON {
				return runDaemonJSONCommand(cmd.OutOrStdout(), func() error {
					return runPairDevices(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), true)
				})
			}
			return runPairDevices(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), devicesJSON)
		},
	}
	devicesCmd.Flags().BoolVar(&devicesJSON, "json", false, "print trusted client devices as JSON")
	devicesCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), pairDevicesHelpText())
	})
	devicesCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(pairDevicesHelpText(), "%v", err)
	})
	pairCmd.AddCommand(devicesCmd)
	var revokeJSON bool
	revokeCmd := &cobra.Command{
		Use:           "revoke <fingerprint>",
		Short:         "Revoke a trusted client device",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if revokeJSON {
				return runDaemonJSONCommand(cmd.OutOrStdout(), func() error {
					return runPairRevoke(cmd.Context(), args[0], cmd.OutOrStdout(), cmd.ErrOrStderr(), true)
				})
			}
			return runPairRevoke(cmd.Context(), args[0], cmd.OutOrStdout(), cmd.ErrOrStderr(), revokeJSON)
		},
	}
	revokeCmd.Flags().BoolVar(&revokeJSON, "json", false, "print revoked device as JSON")
	revokeCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), pairRevokeHelpText())
	})
	revokeCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(pairRevokeHelpText(), "%v", err)
	})
	pairCmd.AddCommand(revokeCmd)
	return pairCmd
}
