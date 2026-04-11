package session

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestForwardLocalTerminalResizesSendsInitialSizeAndUpdates(t *testing.T) {
	resizes := make(chan [2]int, 2)
	hub := NewHub(
		func([]byte) error { return nil },
		func(cols, rows int) error {
			resizes <- [2]int{cols, rows}
			return nil
		},
	)

	sizeMu := sync.Mutex{}
	sizes := [][2]int{{80, 24}, {120, 40}}
	getSize := func() (int, int, error) {
		sizeMu.Lock()
		defer sizeMu.Unlock()
		size := sizes[0]
		if len(sizes) > 1 {
			sizes = sizes[1:]
		}
		return size[0], size[1], nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		forwardLocalTerminalResizes(ctx, hub, signals, getSize)
	}()

	select {
	case got := <-resizes:
		if got != [2]int{80, 24} {
			t.Fatalf("initial resize = %v, want %v", got, [2]int{80, 24})
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not send initial resize")
	}

	signals <- os.Interrupt

	select {
	case got := <-resizes:
		if got != [2]int{120, 40} {
			t.Fatalf("updated resize = %v, want %v", got, [2]int{120, 40})
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not send resize update")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("resize forwarding did not stop after cancellation")
	}
}

func TestCopyInputStopsOnCancel(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe returned error: %v", err)
	}
	defer reader.Close()
	defer writer.Close()

	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		copyInput(ctx, reader, hub)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("copyInput did not stop after context cancellation")
	}
}

func TestCopyInputStopsOnWriteError(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe returned error: %v", err)
	}
	defer reader.Close()
	defer writer.Close()

	writeErr := errors.New("stop")
	hub := NewHub(func([]byte) error { return writeErr }, func(int, int) error { return nil })

	done := make(chan error, 1)
	go func() {
		done <- copyInput(context.Background(), reader, hub)
	}()

	if _, err := writer.Write([]byte("hello")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, writeErr) {
			t.Fatalf("copyInput error = %v, want %v", err, writeErr)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("copyInput did not stop after hub write failure")
	}
}

func TestNextLocalTerminalSinkIDIsUnique(t *testing.T) {
	first := nextLocalTerminalSinkID()
	second := nextLocalTerminalSinkID()

	if first == second {
		t.Fatalf("sink ids must be unique, got %q", first)
	}
	if !strings.HasPrefix(first, "local-terminal-") {
		t.Fatalf("first sink id %q missing expected prefix", first)
	}
	if !strings.HasPrefix(second, "local-terminal-") {
		t.Fatalf("second sink id %q missing expected prefix", second)
	}
}
