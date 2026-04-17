package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newRootCmd(runFn func(context.Context, runArgs, io.Writer, io.Writer) error) *cobra.Command {
	var (
		showVersion bool
		verbose     bool
		label       string
		baseURL     string
	)

	cmd := &cobra.Command{
		Use:           "tunnel",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				return writeVersion(cmd.OutOrStdout())
			}
			resolved := baseURL
			if strings.TrimSpace(resolved) == "" {
				resolved = strings.TrimSpace(os.Getenv(tunnelBaseURLEnv))
			}
			if strings.TrimSpace(resolved) == "" {
				resolved = defaultTunnelBaseURL
			}
			validated, err := validateBaseURL(resolved)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return usagef("missing launcher command")
			}

			token := strings.TrimSpace(os.Getenv(tunnelAuthTokenEnv))
			if token == "" {
				return fmt.Errorf("TUNNEL_AUTH_TOKEN environment variable is required")
			}

			return runFn(cmd.Context(), runArgs{
				Verbose:      verbose,
				Label:        label,
				BaseURL:      validated,
				AuthToken:    token,
				Launcher:     args[0],
				LauncherArgs: append([]string(nil), args[1:]...),
			}, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&showVersion, "version", false, "print tunnel version and exit")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "print relay connection status on successful startup")
	cmd.Flags().StringVarP(&label, "label", "l", "", "optional session label for relay clients")
	cmd.Flags().StringVar(&baseURL, "base-url", "",
		fmt.Sprintf("relay base URL (fallback: %s, default: %s)", tunnelBaseURLEnv, defaultTunnelBaseURL))
	cmd.Flags().SetInterspersed(false)
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), tunnelHelpText())
	})
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usagef("%v", err)
	})
	return cmd
}

func parseRunArgsForTest(args []string) (runArgs, error) {
	var captured runArgs
	cmd := newRootCmd(func(_ context.Context, parsed runArgs, _, _ io.Writer) error {
		captured = parsed
		return nil
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		return runArgs{}, err
	}
	return captured, nil
}
