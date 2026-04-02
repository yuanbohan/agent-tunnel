package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/launcher"
	"yuanbohan/tunnel/internal/server"
	"yuanbohan/tunnel/internal/session"
)

func TestRunWithArgsStopsBeforeStartingSessionWhenLocalTerminalPreparationFails(t *testing.T) {
	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartServer := startServer
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startServer = oldStartServer
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/bin/echo", Args: append([]string(nil), args...)}, nil
	}

	wantErr := errors.New("inappropriate ioctl for device")
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return nil, wantErr
	}

	startedSession := false
	startSession = func(context.Context, string, []string, map[string]session.OutputSink) (*session.Running, error) {
		startedSession = true
		return nil, nil
	}

	startedServer := false
	startServer = func(server.LiveSession) (*server.Running, error) {
		startedServer = true
		return nil, nil
	}

	var stderr bytes.Buffer
	err := runWithArgs([]string{"agentunnel", "codex", "--version"}, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if startedSession {
		t.Fatal("runWithArgs started the child session before local terminal preparation succeeded")
	}
	if startedServer {
		t.Fatal("runWithArgs started the web server before local terminal preparation succeeded")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want no startup banner on terminal preparation failure", got)
	}
}

func TestWaitForProcessOrShutdownIgnoresLocalTerminalCompletion(t *testing.T) {
	localDone := make(chan struct{})
	close(localDone)

	waitErr := make(chan error, 1)
	result := make(chan error, 1)

	go func() {
		result <- waitForProcessOrShutdown(context.Background(), localDone, waitErr)
	}()

	select {
	case err := <-result:
		t.Fatalf("returned before process exit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	want := errors.New("child exited")
	waitErr <- want

	select {
	case err := <-result:
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want %v", err, want)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not return after process exit")
	}
}

func TestWaitForProcessOrShutdownReturnsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	localDone := make(chan struct{})
	waitErr := make(chan error)

	result := make(chan error, 1)
	go func() {
		result <- waitForProcessOrShutdown(ctx, localDone, waitErr)
	}()

	cancel()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not return after context cancellation")
	}
}
