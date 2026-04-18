package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"yuanbohan/tunnel/internal/buildinfo"
	tunnelupdate "yuanbohan/tunnel/internal/tunnel/update"
)

const (
	startupUpdateCheckInterval = 24 * time.Hour
	startupUpdateCheckTimeout  = 2 * time.Second
)

var (
	startupUpdateNow      = time.Now
	isInteractiveTerminal = func(stdin io.Reader, stdout io.Writer) bool {
		stdinFile, stdinOK := stdin.(*os.File)
		stdoutFile, stdoutOK := stdout.(*os.File)
		if !stdinOK || !stdoutOK {
			return false
		}
		return term.IsTerminal(int(stdinFile.Fd())) && term.IsTerminal(int(stdoutFile.Fd()))
	}
	startupUpdatePrompt = promptStartupUpdate
	reexecTunnelProcess = func() error {
		path, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}
		return syscall.Exec(path, os.Args, os.Environ())
	}
)

func maybeHandleStartupUpdate(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	if !isInteractiveTerminal(stdin, stdout) {
		return nil
	}

	disabledValue, err := settingsEnv(tunnelUpdateDisabledEnv, osEnv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: ignoring broken %s while checking Tunnel updates: %v\n", tunnelSettingsFileName, err)
		disabledValue = strings.TrimSpace(osEnv(tunnelUpdateDisabledEnv))
	}
	if envEnabled(disabledValue) {
		return nil
	}

	state, err := loadUpdaterState()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: ignoring broken %s while checking Tunnel updates: %v\n", tunnelUpdaterFileName, err)
		state = updaterState{Version: updaterSchemaVersion}
	}

	now := startupUpdateNow()
	if checkedAt := time.Unix(state.LastCheckedAt, 0); state.LastCheckedAt > 0 && now.Sub(checkedAt) < startupUpdateCheckInterval {
		return nil
	}

	engine := newUpdaterEngine(updaterCallbacks{})
	checkCtx, cancel := context.WithTimeout(ctx, startupUpdateCheckTimeout)
	manifest, available, err := engine.UpdateAvailable(checkCtx)
	cancel()
	state.LastCheckedAt = now.Unix()
	if err := saveUpdaterState(state); err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: could not save %s after checking Tunnel updates: %v\n", tunnelUpdaterFileName, err)
	}
	if err != nil {
		return nil
	}
	if !available {
		return nil
	}

	updateNow, err := startupUpdatePrompt(stdin, stdout, buildinfo.String(), manifest.Version)
	if err != nil {
		return err
	}
	if !updateNow {
		return nil
	}

	callbacks := newUpdaterStateCallbacks(state, func(next *updaterState, result tunnelupdate.InstallResult) {
		next.RollbackVersion = result.RollbackVersion
		next.RollbackReason = result.RollbackUnavailableReason
	})
	engine = newUpdaterEngine(callbacks)
	result, err := engine.InstallVersion(ctx, manifest.Version)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tunnel update failed: %v\n", err)
		return nil
	}

	if err := reexecTunnelProcess(); err != nil {
		recovery := "run `tunnel rollback` to return to the previous version"
		if strings.TrimSpace(result.RollbackUnavailableReason) != "" {
			recovery = result.RollbackUnavailableReason
		}
		return fmt.Errorf("tunnel update succeeded, but restarting under %s failed: %w\n%s", manifest.Version, err, recovery)
	}
	return nil
}

func envEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func promptStartupUpdate(stdin io.Reader, stdout io.Writer, currentVersion, latestVersion string) (bool, error) {
	terminal, ok := stdin.(*os.File)
	if !ok {
		return false, fmt.Errorf("startup update prompt requires terminal stdin")
	}
	fd := int(terminal.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return false, fmt.Errorf("prepare startup update prompt: %w", err)
	}
	defer term.Restore(fd, oldState)

	selection := 0
	if _, err := fmt.Fprintf(stdout,
		"A new Tunnel version is available\n\nCurrent: %s\nLatest:  %s\n\n? Update Tunnel now?\n",
		currentVersion,
		latestVersion,
	); err != nil {
		return false, err
	}
	if err := renderStartupUpdateOptions(stdout, selection, false); err != nil {
		return false, err
	}

	var input [3]byte
	for {
		n, err := terminal.Read(input[:1])
		if err != nil {
			return false, fmt.Errorf("read startup update prompt input: %w", err)
		}
		if n == 0 {
			continue
		}

		switch input[0] {
		case '\r', '\n':
			_, _ = io.WriteString(stdout, "\n")
			return selection == 0, nil
		case 3:
			return false, exitError{code: 130}
		case 0x1b:
			if _, err := io.ReadFull(terminal, input[1:3]); err != nil {
				return false, fmt.Errorf("read startup update prompt escape sequence: %w", err)
			}
			switch string(input[1:3]) {
			case "[A":
				selection = 0
			case "[B":
				selection = 1
			default:
				continue
			}
			if err := renderStartupUpdateOptions(stdout, selection, true); err != nil {
				return false, err
			}
		}
	}
}

func renderStartupUpdateOptions(stdout io.Writer, selection int, rerender bool) error {
	if rerender {
		if _, err := io.WriteString(stdout, "\x1b[2F"); err != nil {
			return err
		}
	}
	options := []string{"Update now", "Skip and continue"}
	for index, option := range options {
		prefix := "  "
		if index == selection {
			prefix = "> "
		}
		if _, err := fmt.Fprintf(stdout, "\x1b[2K%s%s\n", prefix, option); err != nil {
			return err
		}
	}
	return nil
}
