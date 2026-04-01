package session

import (
	"context"
	"os"

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

		type stdinMessage struct {
			data []byte
			err  error
		}

		stdinCh := make(chan stdinMessage, 1)
		go func() {
			buf := make([]byte, 256)
			for {
				n, readErr := os.Stdin.Read(buf)
				if n > 0 {
					cp := append([]byte(nil), buf[:n]...)
					stdinCh <- stdinMessage{data: cp}
				}
				if readErr != nil {
					stdinCh <- stdinMessage{err: readErr}
					return
				}
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-stdinCh:
				if msg.err != nil {
					return
				}
				_ = hub.WriteInput(msg.data)
			}
		}
	}()

	return restore, finished, nil
}
