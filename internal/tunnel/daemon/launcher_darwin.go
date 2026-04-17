package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	strategyTerminalApp = "terminal_app"
	strategyITerm2      = "iterm2"
)

func inferDarwinRecipe() (LauncherRecipe, error) {
	termProgram := strings.TrimSpace(strings.ToLower(getenv("TERM_PROGRAM")))
	switch termProgram {
	case "iterm.app":
		return LauncherRecipe{Strategy: strategyITerm2}, nil
	default:
		if _, err := exec.LookPath("osascript"); err != nil {
			return LauncherRecipe{}, fmt.Errorf("unsupported terminal: osascript not available")
		}
		return LauncherRecipe{Strategy: strategyTerminalApp}, nil
	}
}

func launchWithRecipe(recipe LauncherRecipe, command string) error {
	switch recipe.Strategy {
	case strategyITerm2:
		return launchITerm2(command)
	case strategyTerminalApp:
		return launchTerminalApp(command)
	default:
		return fmt.Errorf("unsupported launcher strategy: %s", recipe.Strategy)
	}
}

func launchTerminalApp(command string) error {
	script := fmt.Sprintf(`
tell application "Terminal"
	activate
	do script %q
end tell
`, command)
	return runAppleScript(script)
}

func launchITerm2(command string) error {
	script := fmt.Sprintf(`
tell application "iTerm"
	activate
	create window with default profile command %q
end tell
`, command)
	return runAppleScript(script)
}

func runAppleScript(script string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("osascript timed out after 10s: %w", err)
		}
		if trimmed := strings.TrimSpace(stderr.String()); trimmed != "" {
			return fmt.Errorf("%w: %s", err, trimmed)
		}
		return err
	}
	return nil
}
