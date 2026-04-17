package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type authLoginArgs struct {
	BaseURL string
}

type fdReader interface {
	io.Reader
	Fd() uintptr
}

type credentialPrompter interface {
	Prompt(io.Reader, io.Writer) (string, string, error)
}

type terminalCredentialPrompter struct{}

var (
	credentialsPrompter credentialPrompter = terminalCredentialPrompter{}
	nowFunc                                = time.Now
	hostNameFunc                           = os.Hostname
	newAuthStore                           = defaultAuthStore
	newAuthAPI                             = func(baseURL string) *relayAuthAPI { return newRelayAuthAPI(baseURL) }
)

func newAuthLoginCmd(loginFn loginHandler) *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:           "login",
		Short:         "Sign in and save one local agent token",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := resolveBaseURL(baseURL, osEnv)
			if err != nil {
				return err
			}
			return loginFn(cmd.Context(), authLoginArgs{BaseURL: resolved}, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&baseURL, "base-url", "", "relay base URL")
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), authLoginHelpText())
	})
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageWithHelp(authLoginHelpText(), "%v", err)
	})
	return cmd
}

func newAuthLogoutCmd(logoutFn logoutHandler) *cobra.Command {
	return &cobra.Command{
		Use:           "logout",
		Short:         "Remove the local saved login",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return logoutFn(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func newAuthStatusCmd(statusFn statusHandler) *cobra.Command {
	return &cobra.Command{
		Use:           "status",
		Short:         "Show local auth source status as JSON",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statusFn(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func runAuthLogin(ctx context.Context, args authLoginArgs, stdin io.Reader, stdout, stderr io.Writer) error {
	username, password, err := credentialsPrompter.Prompt(stdin, stderr)
	if err != nil {
		return err
	}

	api := newAuthAPI(args.BaseURL)
	session, err := api.login(ctx, username, password)
	if err != nil {
		return err
	}

	tokenName := autoTokenName(nowFunc())
	created, err := api.createAgentToken(ctx, session.AccessToken, tokenName)
	if err != nil {
		return err
	}

	record := newStoredAuth(username, created.Name, created.ID, created.Token, created.CreatedAt, nowFunc())
	store := newAuthStore()
	if err := store.Save(record); err != nil {
		return err
	}

	authPath, err := store.AuthFilePath()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Saved local auth for %s to %s\n", username, authPath)
	return err
}

func runAuthLogout(_ context.Context, stdout io.Writer) error {
	store := newAuthStore()
	if err := store.Clear(); err != nil {
		return err
	}
	authPath, err := store.AuthFilePath()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Removed local auth file %s\n", authPath)
	return err
}

func runAuthStatus(_ context.Context, stdout io.Writer) error {
	output, err := buildAuthStatus(newAuthStore(), osEnv)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func (terminalCredentialPrompter) Prompt(stdin io.Reader, stderr io.Writer) (string, string, error) {
	terminal, ok := stdin.(fdReader)
	if !ok || !term.IsTerminal(int(terminal.Fd())) {
		return "", "", fmt.Errorf("auth login requires an interactive terminal stdin")
	}

	reader := bufio.NewReader(stdin)
	if _, err := io.WriteString(stderr, "Username: "); err != nil {
		return "", "", err
	}
	username, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", "", err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return "", "", fmt.Errorf("username is required")
	}

	if _, err := io.WriteString(stderr, "Password: "); err != nil {
		return "", "", err
	}
	passwordBytes, err := term.ReadPassword(int(terminal.Fd()))
	if err != nil {
		return "", "", fmt.Errorf("read password from terminal: %w", err)
	}
	if _, err := io.WriteString(stderr, "\n"); err != nil {
		return "", "", err
	}
	password := strings.TrimSpace(string(passwordBytes))
	if password == "" {
		return "", "", fmt.Errorf("password is required")
	}
	return username, password, nil
}

func autoTokenName(now time.Time) string {
	host := "local"
	if value, err := hostNameFunc(); err == nil {
		if cleaned := cleanTokenLabel(value); cleaned != "" {
			host = cleaned
		}
	}
	return fmt.Sprintf("tunnel-%s-%s", host, now.UTC().Format("20060102-150405"))
}

func cleanTokenLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
