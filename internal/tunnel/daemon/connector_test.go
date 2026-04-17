package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildShellWrapperScopesInjectedTokenToTunnelCommand(t *testing.T) {
	wrapper := buildShellWrapper("https://relay.example.com", "secret-token", "req-123", "/repo", "api-fix", []string{"codex", "--profile", "prod"})

	if !strings.Contains(wrapper, `TUNNEL_AUTH_TOKEN=`) || !strings.Contains(wrapper, `cd /repo && TUNNEL_BASE_URL=`) || !strings.Contains(wrapper, ` tunnel run --label api-fix codex --profile prod`) {
		t.Fatalf("wrapper = %q, want cwd-scoped tunnel run command", wrapper)
	}
	if strings.Contains(wrapper, `export TUNNEL_AUTH_TOKEN=secret-token`) {
		t.Fatalf("wrapper = %q, want no persistent export of the injected auth token", wrapper)
	}
	if !strings.Contains(wrapper, `if [ -n "$__tunnel_had_auth" ]; then export TUNNEL_AUTH_TOKEN="$__tunnel_prev_auth"; else unset TUNNEL_AUTH_TOKEN; fi`) {
		t.Fatalf("wrapper = %q, want auth token restoration before interactive shell", wrapper)
	}
	if !strings.Contains(wrapper, `TUNNEL_LAUNCH_REQUEST_ID=req-123`) {
		t.Fatalf("wrapper = %q, want launch request id assignment", wrapper)
	}
}

func TestResolveLaunchCWDReturnsAbsolutePath(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("Chdir restore returned error: %v", err)
		}
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir returned error: %v", err)
	}

	resolved, err := resolveLaunchCWD("project")
	if err != nil {
		t.Fatalf("resolveLaunchCWD returned error: %v", err)
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		t.Fatalf("Stat resolved returned error: %v", err)
	}
	projectInfo, err := os.Stat(projectDir)
	if err != nil {
		t.Fatalf("Stat projectDir returned error: %v", err)
	}
	if !os.SameFile(resolvedInfo, projectInfo) {
		t.Fatalf("resolved = %q, want same directory as %q", resolved, projectDir)
	}
}

func TestResolveLaunchCWDRejectsBlankPath(t *testing.T) {
	if _, err := resolveLaunchCWD("   "); err == nil {
		t.Fatal("resolveLaunchCWD error = nil, want blank path rejection")
	}
}
