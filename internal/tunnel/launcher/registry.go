package launcher

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type Command struct {
	Name string
	Path string
	Args []string
}

func Resolve(name string, args []string) (Command, error) {
	return resolveWithLookPath(name, args, exec.LookPath)
}

func resolveWithLookPath(name string, args []string, lookPath func(string) (string, error)) (Command, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return Command{}, fmt.Errorf("launcher name is required")
	}

	path, err := lookPath(trimmed)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Command{}, fmt.Errorf("%s executable not found in PATH", trimmed)
		}
		return Command{}, fmt.Errorf("%s executable lookup failed: %w", trimmed, err)
	}

	return Command{
		Name: trimmed,
		Path: path,
		Args: append([]string(nil), args...),
	}, nil
}
