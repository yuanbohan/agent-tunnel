package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitForProcessOrShutdownIgnoresLocalTerminalCompletion(t *testing.T) {
	localDone := make(chan struct{})
	close(localDone)

	waitErr := make(chan error, 1)
	result := make(chan error, 1)

	go func() {
		result <- waitForProcessOrShutdown(context.Background(), localDone, waitErr)
	}()

	select {
	case err := <-result:
		t.Fatalf("returned before process exit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	want := errors.New("child exited")
	waitErr <- want

	select {
	case err := <-result:
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want %v", err, want)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not return after process exit")
	}
}

func TestWaitForProcessOrShutdownReturnsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	localDone := make(chan struct{})
	waitErr := make(chan error)

	result := make(chan error, 1)
	go func() {
		result <- waitForProcessOrShutdown(ctx, localDone, waitErr)
	}()

	cancel()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not return after context cancellation")
	}
}
