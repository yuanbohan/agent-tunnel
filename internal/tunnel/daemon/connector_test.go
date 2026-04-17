package daemon

import (
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
