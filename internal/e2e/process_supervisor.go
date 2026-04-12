package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const processOutputLimit = 64 * 1024

type tailBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...))
}

type managedProcess struct {
	name   string
	cmd    *exec.Cmd
	output *tailBuffer
	waitCh chan error
	done   chan struct{}

	mu      sync.Mutex
	waitErr error
}

func startManagedProcess(name string, cmd *exec.Cmd) (*managedProcess, error) {
	output := newTailBuffer(processOutputLimit)
	cmd.Stdout = io.MultiWriter(output)
	cmd.Stderr = io.MultiWriter(output)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}

	proc := &managedProcess{
		name:   name,
		cmd:    cmd,
		output: output,
		waitCh: make(chan error, 1),
		done:   make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		proc.mu.Lock()
		proc.waitErr = err
		proc.mu.Unlock()
		proc.waitCh <- err
		close(proc.done)
	}()
	return proc, nil
}

func (p *managedProcess) Tail() string {
	if p == nil || p.output == nil {
		return ""
	}
	return strings.TrimSpace(p.output.String())
}

func (p *managedProcess) Stop(timeout time.Duration) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return nil
	}

	_ = p.cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-p.done:
		return normalizeExitError(p.WaitErr())
	case <-time.After(timeout):
		_ = p.cmd.Process.Kill()
		<-p.done
		return normalizeExitError(p.WaitErr())
	}
}

func (p *managedProcess) WaitErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

func waitForHTTP(ctx context.Context, client *http.Client, url string, proc *managedProcess) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		if proc != nil {
			select {
			case <-proc.done:
				return fmt.Errorf("%s exited before readiness: %v\n%s", proc.name, normalizeExitError(proc.WaitErr()), proc.Tail())
			default:
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			if proc != nil {
				return fmt.Errorf("timed out waiting for %s readiness at %s\n%s", proc.name, url, proc.Tail())
			}
			return fmt.Errorf("timed out waiting for readiness at %s", url)
		case <-ticker.C:
		}
	}
}

func runCommand(name string, cmd *exec.Cmd) (string, error) {
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		if trimmed == "" {
			return "", fmt.Errorf("%s failed: %w", name, err)
		}
		return trimmed, fmt.Errorf("%s failed: %w\n%s", name, err, trimmed)
	}
	return trimmed, nil
}

func normalizeExitError(err error) error {
	if err == nil {
		return nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return nil
		}
	}
	return err
}
