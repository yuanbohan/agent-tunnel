package launcher

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveSupportedLauncher(t *testing.T) {
	cmd, err := resolveWithLookPath("claude", []string{"--resume"}, func(file string) (string, error) {
		if file != "claude" {
			t.Fatalf("lookPath called with %q, want claude", file)
		}
		return "/usr/local/bin/claude", nil
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cmd.Name != "claude" {
		t.Fatalf("Name = %q, want claude", cmd.Name)
	}
	if cmd.Path != "/usr/local/bin/claude" {
		t.Fatalf("Path = %q, want /usr/local/bin/claude", cmd.Path)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "--resume" {
		t.Fatalf("Args = %#v, want [--resume]", cmd.Args)
	}
}

func TestResolveRejectsUnsupportedLauncher(t *testing.T) {
	_, err := resolveWithLookPath("python", nil, func(string) (string, error) {
		t.Fatal("lookPath should not be called for unsupported launchers")
		return "", nil
	})
	if err == nil {
		t.Fatal("expected an error for unsupported launcher")
	}
	if !strings.Contains(err.Error(), "supported launchers: claude, codex, gemini") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveReportsMissingExecutable(t *testing.T) {
	_, err := resolveWithLookPath("gemini", nil, func(string) (string, error) {
		return "", errors.New("not found")
	})
	if err == nil {
		t.Fatal("expected an error for missing executable")
	}
	if !strings.Contains(err.Error(), "gemini executable not found in PATH") {
		t.Fatalf("unexpected error: %v", err)
	}
}
