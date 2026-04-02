package session

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
	clientterm "yuanbohan/tunnel/internal/client"
)

type outputSinkFunc func([]byte) error

func (f outputSinkFunc) WriteOutput(data []byte) error {
	return f(data)
}

var localTerminalSinkID uint64

func AttachLocalTerminal(ctx context.Context, hub *Hub) (restore func(), done <-chan struct{}, err error) {
	restore, err = clientterm.EnterRawMode()
	if err != nil {
		return nil, nil, err
	}

	sinkID := nextLocalTerminalSinkID()
	hub.AddSink(sinkID, outputSinkFunc(func(data []byte) error {
		_, writeErr := os.Stdout.Write(data)
		return writeErr
	}))

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer hub.RemoveSink(sinkID)

		_ = copyInput(ctx, os.Stdin, hub)
	}()

	return restore, finished, nil
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
