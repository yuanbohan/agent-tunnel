package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestTunnelAuthLoginThenRunFromStoredAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), scenarioTimeout)
	defer cancel()

	h := newHarness(t)
	h.Prepare(ctx)

	client := newAppClient(h.baseURL)
	runID := time.Now().UTC().UnixNano()
	username := fmt.Sprintf("authuser%d", runID)
	password := "password123"

	inviteCode := h.CreateInvite(ctx)
	if _, err := client.Register(inviteCode, username, password); err != nil {
		t.Fatalf("register returned error: %v", err)
	}

	homeDir := t.TempDir()
	loginOutput, err := runTunnelAuthLogin(ctx, h, homeDir, username, password)
	if err != nil {
		t.Fatalf("runTunnelAuthLogin returned error: %v\n%s", err, loginOutput)
	}

	authPath := filepath.Join(homeDir, ".tunnel", "auth.json")
	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}

	tunnel, err := startTunnelProcess(h.binaries.tunnel, TunnelConfig{
		BaseURL:      h.baseURL,
		LauncherName: filepath.Base(h.binaries.launcher),
		LauncherPath: h.binDir,
		Label:        "auth-e2e",
		HomeDir:      homeDir,
	})
	if err != nil {
		t.Fatalf("startTunnelProcess returned error: %v", err)
	}
	h.tunnel = tunnel
	waitForTunnelOutput(t, ctx, tunnel, "READY e2e-launcher")

	writeTunnelInput(t, tunnel, "stored-auth\r")
	waitForTunnelOutput(t, ctx, tunnel, "REPLY stored-auth")
}

func runTunnelAuthLogin(ctx context.Context, h *Harness, homeDir, username, password string) (string, error) {
	cmd := exec.CommandContext(h.commandContext(ctx), h.binaries.tunnel, "auth", "login", "--base-url", h.baseURL)
	cmd.Env = append([]string(nil), os.Environ()...)
	cmd.Env = append(cmd.Env, "HOME="+homeDir)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return "", fmt.Errorf("start tunnel auth login: %w", err)
	}
	defer ptmx.Close()

	output := newTailBuffer(processOutputLimit)
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, ptmx)
		close(copyDone)
	}()

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()

	if err := waitForTailContains(ctx, output, waitErr, "Username: "); err != nil {
		return output.String(), err
	}
	if _, err := io.WriteString(ptmx, username+"\r"); err != nil {
		return output.String(), err
	}

	if err := waitForTailContains(ctx, output, waitErr, "Password: "); err != nil {
		return output.String(), err
	}
	if _, err := io.WriteString(ptmx, password+"\r"); err != nil {
		return output.String(), err
	}

	err = <-waitErr
	<-copyDone
	return strings.TrimSpace(output.String()), err
}

func waitForTailContains(ctx context.Context, output *tailBuffer, waitErr <-chan error, want string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		if strings.Contains(output.String(), want) {
			return nil
		}

		select {
		case err := <-waitErr:
			if err == nil {
				return fmt.Errorf("process exited before output %q appeared", want)
			}
			return fmt.Errorf("process exited before output %q appeared: %w", want, err)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
