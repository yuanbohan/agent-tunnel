package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

type Running struct {
	Hub *Hub

	ptmx      *os.File
	cmd       *exec.Cmd
	closeOnce sync.Once
	cleanOnce sync.Once
	cleanErr  error
	waitOnce  sync.Once
	waitErr   error
	waitDone  chan struct{}
	readDone  chan struct{}

	mu             sync.Mutex
	closeRequested bool
}

func StartCommand(ctx context.Context, path string, args []string) (*Running, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	running := &Running{
		ptmx:     ptmx,
		cmd:      cmd,
		waitDone: make(chan struct{}),
		readDone: make(chan struct{}),
	}
	running.Hub = NewHub(
		func(data []byte) error {
			_, err := ptmx.Write(data)
			return err
		},
		func(cols, rows int) error {
			return pty.Setsize(ptmx, &pty.Winsize{
				Cols: uint16(cols),
				Rows: uint16(rows),
			})
		},
	)

	go func() {
		defer close(running.readDone)

		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				running.Hub.BroadcastOutput(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	return running, nil
}

func (r *Running) Wait() error {
	err := r.waitForProcess()
	cleanupErr := r.cleanupPTY()
	<-r.readDone
	if r.closeWasRequested() && isExpectedShutdownError(err) {
		err = nil
	}
	if err != nil {
		return err
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	return err
}

func (r *Running) Close() error {
	r.closeOnce.Do(func() {
		r.markCloseRequested()
		if r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
	})
	return r.Wait()
}

func (r *Running) waitForProcess() error {
	r.waitOnce.Do(func() {
		r.waitErr = r.cmd.Wait()
		close(r.waitDone)
	})

	<-r.waitDone
	return r.waitErr
}

func (r *Running) cleanupPTY() error {
	r.cleanOnce.Do(func() {
		r.cleanErr = r.ptmx.Close()
	})

	if errors.Is(r.cleanErr, os.ErrClosed) {
		return nil
	}
	return r.cleanErr
}

func (r *Running) markCloseRequested() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeRequested = true
}

func (r *Running) closeWasRequested() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closeRequested
}

func isExpectedShutdownError(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ProcessState != nil && !exitErr.ProcessState.Success()
}
