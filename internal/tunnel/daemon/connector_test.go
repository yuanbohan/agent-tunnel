package daemon

import (
	"strings"
	"testing"
)

func TestBuildShellWrapperScopesInjectedTokenToTunnelCommand(t *testing.T) {
	wrapper := buildShellWrapper("https://relay.example.com", "secret-token", []string{"codex", "--profile", "prod"})

	if !strings.Contains(wrapper, `TUNNEL_AUTH_TOKEN=`) || !strings.Contains(wrapper, ` tunnel run codex --profile prod`) {
		t.Fatalf("wrapper = %q, want command-scoped auth token assignment", wrapper)
	}
	if strings.Contains(wrapper, `export TUNNEL_AUTH_TOKEN=secret-token`) {
		t.Fatalf("wrapper = %q, want no persistent export of the injected auth token", wrapper)
	}
	if !strings.Contains(wrapper, `if [ -n "$__tunnel_had_auth" ]; then export TUNNEL_AUTH_TOKEN="$__tunnel_prev_auth"; else unset TUNNEL_AUTH_TOKEN; fi`) {
		t.Fatalf("wrapper = %q, want auth token restoration before interactive shell", wrapper)
	}
}
