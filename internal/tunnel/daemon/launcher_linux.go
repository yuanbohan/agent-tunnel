package daemon

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	strategyXDGTerminalExec = "xdg_terminal_exec"
	strategyExecTemplate    = "exec_template"
)

func inferLinuxRecipe() (LauncherRecipe, error) {
	if command, err := exec.LookPath("xdg-terminal-exec"); err == nil {
		return LauncherRecipe{
			Strategy: strategyXDGTerminalExec,
			Command:  command,
		}, nil
	}

	for _, candidate := range linuxTerminalCandidates() {
		if command, err := exec.LookPath(candidate.name); err == nil {
			return LauncherRecipe{
				Strategy: strategyExecTemplate,
				Command:  command,
				Args:     append([]string(nil), candidate.args...),
			}, nil
		}
	}

	return LauncherRecipe{}, fmt.Errorf("unsupported terminal: no launchable Linux terminal recipe detected")
}

func launchWithRecipe(recipe LauncherRecipe, command string) error {
	var args []string
	switch recipe.Strategy {
	case strategyXDGTerminalExec:
		args = []string{"--hold", "sh", "-lc", command}
	case strategyExecTemplate:
		args = append([]string(nil), recipe.Args...)
		args = append(args, "sh", "-lc", command)
	default:
		return fmt.Errorf("unsupported launcher strategy: %s", recipe.Strategy)
	}

	cmd := exec.Command(recipe.Command, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func linuxTerminalCandidates() []struct {
	name string
	args []string
} {
	if terminal := strings.TrimSpace(getenv("TERMINAL")); terminal != "" {
		return append([]struct {
			name string
			args []string
		}{{name: terminal, args: []string{"-e"}}}, []struct {
			name string
			args []string
		}{
			{name: "x-terminal-emulator", args: []string{"-e"}},
			{name: "gnome-terminal", args: []string{"--"}},
			{name: "konsole", args: []string{"-e"}},
			{name: "kitty"},
			{name: "wezterm", args: []string{"start", "--"}},
			{name: "alacritty", args: []string{"-e"}},
			{name: "xterm", args: []string{"-hold", "-e"}},
		}...)
	}

	return []struct {
		name string
		args []string
	}{
		{name: "x-terminal-emulator", args: []string{"-e"}},
		{name: "gnome-terminal", args: []string{"--"}},
		{name: "konsole", args: []string{"-e"}},
		{name: "kitty"},
		{name: "wezterm", args: []string{"start", "--"}},
		{name: "alacritty", args: []string{"-e"}},
		{name: "xterm", args: []string{"-hold", "-e"}},
	}
}

func detectLinuxPlatformID() string {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return PlatformIDUnknown
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "ID=") {
			continue
		}
		value := strings.TrimPrefix(line, "ID=")
		return strings.Trim(strings.TrimSpace(value), `"`)
	}
	return PlatformIDUnknown
}
