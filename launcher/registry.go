package launcher

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type Command struct {
	Name string
	Path string
	Args []string
}

var supported = map[string]string{
	"claude": "claude",
	"codex":  "codex",
	"gemini": "gemini",
}

func Resolve(name string, args []string) (Command, error) {
	return resolveWithLookPath(name, args, exec.LookPath)
}

func resolveWithLookPath(name string, args []string, lookPath func(string) (string, error)) (Command, error) {
	executable, ok := supported[name]
	if !ok {
		names := make([]string, 0, len(supported))
		for launcherName := range supported {
			names = append(names, launcherName)
		}
		sort.Strings(names)
		return Command{}, fmt.Errorf("unsupported launcher %q (supported launchers: %s)", name, strings.Join(names, ", "))
	}

	path, err := lookPath(executable)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Command{}, fmt.Errorf("%s executable not found in PATH", executable)
		}
		return Command{}, fmt.Errorf("%s executable lookup failed: %w", executable, err)
	}

	return Command{
		Name: name,
		Path: path,
		Args: append([]string(nil), args...),
	}, nil
}
