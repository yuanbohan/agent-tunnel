package session

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
	clientterm "yuanbohan/tunnel/internal/client"
)

type outputSinkFunc func([]byte) error

func (f outputSinkFunc) WriteOutput(data []byte) error {
	return f(data)
}

type LocalTerminal struct {
	restore func()
	sinkID  string
	sink    OutputSink
}

var localTerminalSinkID uint64

func PrepareLocalTerminal() (*LocalTerminal, error) {
	restore, err := clientterm.EnterRawMode()
	if err != nil {
		return nil, err
	}

	return &LocalTerminal{
		restore: restore,
		sinkID:  nextLocalTerminalSinkID(),
		sink: outputSinkFunc(func(data []byte) error {
			_, writeErr := os.Stdout.Write(data)
			return writeErr
		}),
	}, nil
}

func (t *LocalTerminal) Restore() {
	if t == nil || t.restore == nil {
		return
	}
	t.restore()
}

func (t *LocalTerminal) SinkRegistration() (string, OutputSink) {
	return t.sinkID, t.sink
}

func (t *LocalTerminal) Start(ctx context.Context, hub *Hub) <-chan struct{} {
	resizeSignals := make(chan os.Signal, 1)
	signal.Notify(resizeSignals, syscall.SIGWINCH)

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer hub.RemoveSink(t.sinkID)

		_ = copyInput(ctx, os.Stdin, hub)
	}()
	go func() {
		defer signal.Stop(resizeSignals)
		forwardLocalTerminalResizes(ctx, hub, resizeSignals, getLocalTerminalSize)
	}()

	return finished
}

func AttachLocalTerminal(ctx context.Context, hub *Hub) (restore func(), done <-chan struct{}, err error) {
	local, err := PrepareLocalTerminal()
	if err != nil {
		return nil, nil, err
	}

	sinkID, sink := local.SinkRegistration()
	hub.AddSink(sinkID, sink)

	return local.Restore, local.Start(ctx, hub), nil
}

func forwardLocalTerminalResizes(ctx context.Context, hub *Hub, signals <-chan os.Signal, getSize func() (int, int, error)) {
	sendResize := func() {
		cols, rows, err := getSize()
		if err != nil {
			return
		}
		_ = hub.Resize(cols, rows)
	}

	sendResize()

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-signals:
			if !ok {
				return
			}
			sendResize()
		}
	}
}

func getLocalTerminalSize() (int, int, error) {
	return term.GetSize(int(os.Stdin.Fd()))
}

func copyInput(ctx context.Context, input *os.File, hub *Hub) error {
	buf := make([]byte, 256)
	fd := int(input.Fd())

	for {
		if ctx.Err() != nil {
			return nil
		}

		ready, err := waitForInput(ctx, fd)
		if err != nil || !ready {
			if err == nil {
				continue
			}
			return err
		}

		n, readErr := input.Read(buf)
		if n > 0 {
			cp := append([]byte(nil), buf[:n]...)
			if err := hub.WriteInput(cp); err != nil {
				return err
			}
		}
		if readErr != nil {
			return readErr
		}
	}
}

func waitForInput(ctx context.Context, fd int) (bool, error) {
	fds := []unix.PollFd{{
		Fd:     int32(fd),
		Events: unix.POLLIN,
	}}

	for {
		if ctx.Err() != nil {
			return false, nil
		}

		n, err := unix.Poll(fds, int((100 * time.Millisecond).Milliseconds()))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		if n == 0 {
			return false, nil
		}
		return fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0, nil
	}
}

func nextLocalTerminalSinkID() string {
	id := atomic.AddUint64(&localTerminalSinkID, 1)
	return fmt.Sprintf("local-terminal-%d", id)
}
