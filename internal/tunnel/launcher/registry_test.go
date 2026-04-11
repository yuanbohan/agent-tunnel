package launcher

import (
	"errors"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

func TestResolveOfficialLauncher(t *testing.T) {
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
	if cmd.DisplayName != "Claude Code" {
		t.Fatalf("DisplayName = %q, want Claude Code", cmd.DisplayName)
	}
	if cmd.Path != "/usr/local/bin/claude" {
		t.Fatalf("Path = %q, want /usr/local/bin/claude", cmd.Path)
	}
	if !cmd.Official {
		t.Fatal("Official = false, want true")
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "--resume" {
		t.Fatalf("Args = %#v, want [--resume]", cmd.Args)
	}
}

func TestResolveAllowsUnverifiedLaunchers(t *testing.T) {
	cmd, err := resolveWithLookPath("goose", []string{"run"}, func(file string) (string, error) {
		if file != "goose" {
			t.Fatalf("lookPath called with %q, want goose", file)
		}
		return "/usr/local/bin/goose", nil
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cmd.Name != "goose" {
		t.Fatalf("Name = %q, want goose", cmd.Name)
	}
	if cmd.DisplayName != "goose" {
		t.Fatalf("DisplayName = %q, want goose", cmd.DisplayName)
	}
	if cmd.Path != "/usr/local/bin/goose" {
		t.Fatalf("Path = %q, want /usr/local/bin/goose", cmd.Path)
	}
	if cmd.Official {
		t.Fatal("Official = true, want false")
	}
}

func TestResolveUsesBaseNameForUnverifiedAbsolutePath(t *testing.T) {
	cmd, err := resolveWithLookPath("/opt/bin/custom-agent", nil, func(file string) (string, error) {
		if file != "/opt/bin/custom-agent" {
			t.Fatalf("lookPath called with %q, want /opt/bin/custom-agent", file)
		}
		return "/opt/bin/custom-agent", nil
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cmd.Name != "custom-agent" {
		t.Fatalf("Name = %q, want custom-agent", cmd.Name)
	}
	if cmd.DisplayName != "custom-agent" {
		t.Fatalf("DisplayName = %q, want custom-agent", cmd.DisplayName)
	}
}

func TestResolveReportsMissingOfficialExecutable(t *testing.T) {
	_, err := resolveWithLookPath("trae-cli", nil, func(string) (string, error) {
		return "", exec.ErrNotFound
	})
	if err == nil {
		t.Fatal("expected an error for missing executable")
	}
	if !strings.Contains(err.Error(), "trae-cli executable not found in PATH") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveReportsMissingUnverifiedExecutable(t *testing.T) {
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

func TestProfilesReturnsSortedOfficialProfiles(t *testing.T) {
	got := Profiles()
	if len(got) != 10 {
		t.Fatalf("len(Profiles()) = %d, want 10", len(got))
	}

	names := make([]string, 0, len(got))
	for _, profile := range got {
		if !profile.Official {
			t.Fatalf("profile %q Official = false, want true", profile.Name)
		}
		names = append(names, profile.Name)
	}

	want := append([]string(nil), names...)
	sort.Strings(want)
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("Profiles() names = %#v, want sorted %#v", names, want)
		}
	}
}

func TestLookupFallsBackToDefaultProfile(t *testing.T) {
	profile := Lookup("openclaw")
	if profile.Name != "openclaw" {
		t.Fatalf("Name = %q, want openclaw", profile.Name)
	}
	if profile.DisplayName != "openclaw" {
		t.Fatalf("DisplayName = %q, want openclaw", profile.DisplayName)
	}
	if profile.Official {
		t.Fatal("Official = true, want false")
	}
}
