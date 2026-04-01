package session

import (
	"context"
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
	waitOnce  sync.Once
	waitErr   error
	waitDone  chan struct{}
	readDone  chan struct{}
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
	<-r.readDone
	return err
}

func (r *Running) Close() error {
	var err error
	r.closeOnce.Do(func() {
		_ = r.ptmx.Close()
		if r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		err = r.Wait()
	})
	return err
}

func (r *Running) waitForProcess() error {
	r.waitOnce.Do(func() {
		r.waitErr = r.cmd.Wait()
		close(r.waitDone)
	})

	<-r.waitDone
	return r.waitErr
}
