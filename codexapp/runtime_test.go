package codexapp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"yuanbohan/tunnel/launcher"
)

func TestStartBuildsRemoteCommandAfterReady(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ready.Close()

	oldExec := execCommandContext
	t.Cleanup(func() { execCommandContext = oldExec })
	execCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		script := fmt.Sprintf("printf 'listening on: ws://127.0.0.1:51723\n'; printf 'readyz: %s/readyz\n'; trap 'exit 0' TERM INT; while :; do sleep 1; done", ready.URL)
		return exec.CommandContext(ctx, "/bin/sh", "-c", script)
	}

	runtime, err := Start(context.Background(), launcher.Command{
		Name: "codex",
		Path: "/usr/bin/codex",
		Args: []string{"--profile", "prod"},
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer runtime.Close()

	remote := runtime.RemoteCommand()
	if remote.Path != "/usr/bin/codex" {
		t.Fatalf("remote path = %q, want /usr/bin/codex", remote.Path)
	}
	wantArgs := []string{"--remote", "ws://127.0.0.1:51723", "--profile", "prod"}
	if len(remote.Args) != len(wantArgs) {
		t.Fatalf("remote args = %#v, want %#v", remote.Args, wantArgs)
	}
	for i := range wantArgs {
		if remote.Args[i] != wantArgs[i] {
			t.Fatalf("remote args = %#v, want %#v", remote.Args, wantArgs)
		}
	}
}

func TestStartFailsWhenReadyNeverSucceeds(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer ready.Close()

	oldExec := execCommandContext
	t.Cleanup(func() { execCommandContext = oldExec })
	execCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		script := fmt.Sprintf("printf 'listening on: ws://127.0.0.1:51723\n'; printf 'readyz: %s/readyz\n'; trap 'exit 0' TERM INT; while :; do sleep 1; done", ready.URL)
		return exec.CommandContext(ctx, "/bin/sh", "-c", script)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := Start(ctx, launcher.Command{Name: "codex", Path: "/usr/bin/codex"})
	if err == nil {
		t.Fatal("expected readiness failure")
	}
}

func TestCloseStopsManagedProcess(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ready.Close()

	oldExec := execCommandContext
	t.Cleanup(func() { execCommandContext = oldExec })
	execCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		script := fmt.Sprintf("printf 'listening on: ws://127.0.0.1:51723\n'; printf 'readyz: %s/readyz\n'; trap 'exit 0' TERM INT; while :; do sleep 1; done", ready.URL)
		return exec.CommandContext(ctx, "/bin/sh", "-c", script)
	}

	runtime, err := Start(context.Background(), launcher.Command{Name: "codex", Path: "/usr/bin/codex"})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := runtime.Wait(); err != nil {
		t.Fatalf("Wait returned error after Close: %v", err)
	}
}
