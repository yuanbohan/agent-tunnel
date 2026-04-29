package e2e

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type TunnelConfig struct {
	BaseURL      string
	AgentToken   string
	LauncherName string
	LauncherPath string
	Label        string
	HomeDir      string
	ExtraEnv     []string
}

type TunnelProcess struct {
	cmd    *exec.Cmd
	ptmx   *os.File
	output *tailBuffer
	waitCh chan error
	done   chan struct{}

	mu   sync.Mutex
	err  error
	once sync.Once
}

func startTunnelProcess(binary string, cfg TunnelConfig) (*TunnelProcess, error) {
	cmd := exec.Command(binary, "run", "--label", cfg.Label, cfg.LauncherName)
	cmd.Env = append([]string(nil), os.Environ()...)
	cmd.Env = append(cmd.Env, "TUNNEL_BASE_URL="+cfg.BaseURL)
	cmd.Env = append(cmd.Env, "TUNNEL_UPDATE_DISABLED=1")
	if strings.TrimSpace(cfg.AgentToken) != "" {
		cmd.Env = append(cmd.Env, "TUNNEL_AUTH_TOKEN="+cfg.AgentToken)
	} else {
		cmd.Env = append(cmd.Env, "TUNNEL_AUTH_TOKEN=")
	}
	if strings.TrimSpace(cfg.HomeDir) != "" {
		cmd.Env = append(cmd.Env, "HOME="+cfg.HomeDir)
	}
	cmd.Env = append(cmd.Env, "PATH="+prependPath(cfg.LauncherPath))
	cmd.Env = append(cmd.Env, cfg.ExtraEnv...)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: 100,
		Rows: 30,
	})
	if err != nil {
		return nil, fmt.Errorf("start tunnel: %w", err)
	}

	proc := &TunnelProcess{
		cmd:    cmd,
		ptmx:   ptmx,
		output: newTailBuffer(processOutputLimit),
		waitCh: make(chan error, 1),
		done:   make(chan struct{}),
	}
	go func() {
		_, _ = io.Copy(proc.output, ptmx)
	}()
	go func() {
		err := cmd.Wait()
		proc.mu.Lock()
		proc.err = err
		proc.mu.Unlock()
		proc.waitCh <- err
		close(proc.done)
	}()
	return proc, nil
}

func (p *TunnelProcess) Tail() string {
	if p == nil || p.output == nil {
		return ""
	}
	return strings.TrimSpace(p.output.String())
}

func (p *TunnelProcess) Exited() bool {
	if p == nil {
		return true
	}
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *TunnelProcess) WaitErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *TunnelProcess) Stop(timeout time.Duration) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}

	var result error
	p.once.Do(func() {
		defer func() {
			if p.ptmx != nil {
				_ = p.ptmx.Close()
			}
		}()

		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.done:
			result = normalizeExitError(p.WaitErr())
		case <-time.After(timeout):
			_ = p.cmd.Process.Kill()
			<-p.done
			result = normalizeExitError(p.WaitErr())
		}
	})
	return result
}

func prependPath(dir string) string {
	current := os.Getenv("PATH")
	if strings.TrimSpace(current) == "" {
		return dir
	}
	return dir + string(filepath.ListSeparator) + current
}
