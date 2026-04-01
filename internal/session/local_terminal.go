package session

import (
	"context"
	"os"
	"syscall"
	"time"

	clientterm "yuanbohan/tunnel/internal/client"
)

type outputSinkFunc func([]byte) error

func (f outputSinkFunc) WriteOutput(data []byte) error {
	return f(data)
}

func AttachLocalTerminal(ctx context.Context, hub *Hub) (restore func(), done <-chan struct{}, err error) {
	restore, err = clientterm.EnterRawMode()
	if err != nil {
		return nil, nil, err
	}

	hub.AddSink("local-terminal", outputSinkFunc(func(data []byte) error {
		_, writeErr := os.Stdout.Write(data)
		return writeErr
	}))

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer hub.RemoveSink("local-terminal")

		copyInput(ctx, os.Stdin, hub)
	}()

	return restore, finished, nil
}

func copyInput(ctx context.Context, input *os.File, hub *Hub) {
	buf := make([]byte, 256)
	fd := int(input.Fd())

	for {
		if ctx.Err() != nil {
			return
		}

		ready, err := waitForInput(ctx, fd)
		if err != nil || !ready {
			if err == nil {
				continue
			}
			return
		}

		n, readErr := input.Read(buf)
		if n > 0 {
			cp := append([]byte(nil), buf[:n]...)
			_ = hub.WriteInput(cp)
		}
		if readErr != nil {
			return
		}
	}
}

func waitForInput(ctx context.Context, fd int) (bool, error) {
	for {
		if ctx.Err() != nil {
			return false, nil
		}

		timeout := syscall.NsecToTimeval((100 * time.Millisecond).Nanoseconds())
		var readfds syscall.FdSet
		fdSet(fd, &readfds)

		err := syscall.Select(fd+1, &readfds, nil, nil, &timeout)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		return fdIsSet(fd, &readfds), nil
	}
}

func fdSet(fd int, set *syscall.FdSet) {
	set.Bits[fd/64] |= 1 << (uint(fd) % 64)
}

func fdIsSet(fd int, set *syscall.FdSet) bool {
	return set.Bits[fd/64]&(1<<(uint(fd)%64)) != 0
}
