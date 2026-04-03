package session

import (
	"context"
	"os"
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
