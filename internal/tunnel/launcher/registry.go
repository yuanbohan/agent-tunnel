package launcher

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Command struct {
	Name        string
	DisplayName string
	Path        string
	Args        []string
	Official    bool
}

type Profile struct {
	Name        string
	DisplayName string
	Executables []string
	Official    bool
}

var defaultProfile = Profile{
	Name:        "default",
	DisplayName: "Command Line Agent",
}

var profiles = map[string]Profile{
	"aider": {
		Name:        "aider",
		DisplayName: "Aider",
		Executables: []string{"aider"},
		Official:    true,
	},
	"amp": {
		Name:        "amp",
		DisplayName: "Amp",
		Executables: []string{"amp"},
		Official:    true,
	},
	"claude": {
		Name:        "claude",
		DisplayName: "Claude Code",
		Executables: []string{"claude"},
		Official:    true,
	},
	"codex": {
		Name:        "codex",
		DisplayName: "OpenAI Codex",
		Executables: []string{"codex"},
		Official:    true,
	},
	"copilot": {
		Name:        "copilot",
		DisplayName: "GitHub Copilot CLI",
		Executables: []string{"copilot"},
		Official:    true,
	},
	"gemini": {
		Name:        "gemini",
		DisplayName: "Gemini CLI",
		Executables: []string{"gemini"},
		Official:    true,
	},
	"kiro-cli": {
		Name:        "kiro-cli",
		DisplayName: "Kiro CLI",
		Executables: []string{"kiro-cli"},
		Official:    true,
	},
	"opencode": {
		Name:        "opencode",
		DisplayName: "OpenCode",
		Executables: []string{"opencode"},
		Official:    true,
	},
	"qwen": {
		Name:        "qwen",
		DisplayName: "Qwen Code",
		Executables: []string{"qwen"},
		Official:    true,
	},
	"trae-cli": {
		Name:        "trae-cli",
		DisplayName: "Trae Agent CLI",
		Executables: []string{"trae-cli"},
		Official:    true,
	},
}

func Profiles() []Profile {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]Profile, 0, len(names))
	for _, name := range names {
		result = append(result, copyProfile(profiles[name]))
	}
	return result
}

func Lookup(name string) Profile {
	trimmed := strings.TrimSpace(name)
	if profile, ok := profiles[trimmed]; ok {
		return copyProfile(profile)
	}

	fallback := copyProfile(defaultProfile)
	if trimmed == "" {
		return fallback
	}

	fallback.Name = launcherLabel(trimmed)
	fallback.DisplayName = fallback.Name
	return fallback
}

func Resolve(name string, args []string) (Command, error) {
	return resolveWithLookPath(name, args, exec.LookPath)
}

func resolveWithLookPath(name string, args []string, lookPath func(string) (string, error)) (Command, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return Command{}, fmt.Errorf("launcher name is required")
	}

	if profile, ok := profiles[trimmed]; ok {
		path, err := resolveOfficialExecutable(profile, lookPath)
		if err != nil {
			return Command{}, err
		}
		return Command{
			Name:        profile.Name,
			DisplayName: profile.DisplayName,
			Path:        path,
			Args:        append([]string(nil), args...),
			Official:    profile.Official,
		}, nil
	}

	path, err := lookPath(trimmed)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Command{}, fmt.Errorf("%s executable not found in PATH", trimmed)
		}
		return Command{}, fmt.Errorf("%s executable lookup failed: %w", trimmed, err)
	}

	fallback := Lookup(trimmed)
	return Command{
		Name:        fallback.Name,
		DisplayName: fallback.DisplayName,
		Path:        path,
		Args:        append([]string(nil), args...),
		Official:    false,
	}, nil
}

func copyProfile(profile Profile) Profile {
	profile.Executables = append([]string(nil), profile.Executables...)
	return profile
}

func launcherLabel(name string) string {
	label := filepath.Base(name)
	if label == "." || label == string(filepath.Separator) || label == "" {
		return name
	}
	return label
}

func resolveOfficialExecutable(profile Profile, lookPath func(string) (string, error)) (string, error) {
	tried := make([]string, 0, len(profile.Executables))
	for _, executable := range profile.Executables {
		path, err := lookPath(executable)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("%s executable lookup failed: %w", executable, err)
		}
		tried = append(tried, executable)
	}

	switch len(tried) {
	case 0:
		return "", fmt.Errorf("%s executable not found in PATH", profile.Name)
	case 1:
		return "", fmt.Errorf("%s executable not found in PATH", tried[0])
	default:
		return "", fmt.Errorf("%s executable not found in PATH (tried: %s)", profile.Name, strings.Join(tried, ", "))
	}
}
