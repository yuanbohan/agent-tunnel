package launcher

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestResolveLauncher(t *testing.T) {
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

func TestResolvePreservesUserProvidedCommand(t *testing.T) {
	cmd, err := resolveWithLookPath("/opt/bin/custom-agent", nil, func(file string) (string, error) {
		if file != "/opt/bin/custom-agent" {
			t.Fatalf("lookPath called with %q, want /opt/bin/custom-agent", file)
		}
		return "/private/opt/custom-agent", nil
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cmd.Name != "/opt/bin/custom-agent" {
		t.Fatalf("Name = %q, want /opt/bin/custom-agent", cmd.Name)
	}
	if cmd.Path != "/private/opt/custom-agent" {
		t.Fatalf("Path = %q, want /private/opt/custom-agent", cmd.Path)
	}
}

func TestResolveReportsMissingExecutable(t *testing.T) {
	_, err := resolveWithLookPath("openclaw", nil, func(string) (string, error) {
		return "", exec.ErrNotFound
	})
	if err == nil {
		t.Fatal("expected an error for missing executable")
	}
	if !strings.Contains(err.Error(), "openclaw executable not found in PATH") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveWrapsUnexpectedLookupFailure(t *testing.T) {
	wantErr := errors.New("permission denied")
	_, err := resolveWithLookPath("codex", nil, func(string) (string, error) {
		return "", wantErr
	})
	if err == nil {
		t.Fatal("expected an error for lookup failure")
	}
	if !strings.Contains(err.Error(), "codex executable lookup failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestResolveCopiesArgsDefensively(t *testing.T) {
	args := []string{"--resume"}
	cmd, err := resolveWithLookPath("claude", args, func(string) (string, error) {
		return "/usr/local/bin/claude", nil
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	args[0] = "--other"
	args = append(args, "--new-flag")

	if len(cmd.Args) != 1 || cmd.Args[0] != "--resume" {
		t.Fatalf("Args = %#v, want [--resume]", cmd.Args)
	}
}
