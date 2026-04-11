package session

import (
	"context"
	"os"
	"testing"
	"time"
)

// This locks down the shared contract for both platform implementations:
// cancellation must break the wait loop quickly even though readiness polling
// uses multiple timed syscalls underneath.
func TestWaitForInputReturnsFalseWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ready, err := waitForInput(ctx, int(os.Stdin.Fd()))
	if err != nil {
		t.Fatalf("waitForInput returned error: %v", err)
	}
	if ready {
		t.Fatal("waitForInput reported ready after context cancellation")
	}
}

// This verifies that the Darwin select workaround and the non-Darwin poll path
// both still report readiness once input arrives on the file descriptor.
func TestWaitForInputReturnsTrueWhenDataAvailable(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe returned error: %v", err)
	}
	defer reader.Close()
	defer writer.Close()

	writeDone := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, writeErr := writer.Write([]byte("x"))
		writeDone <- writeErr
	}()

	ready, err := waitForInput(context.Background(), int(reader.Fd()))
	if err != nil {
		t.Fatalf("waitForInput returned error: %v", err)
	}
	if !ready {
		t.Fatal("waitForInput reported not ready after input became available")
	}

	if err := <-writeDone; err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
}
