package session

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStartCommandBridgesInputAndOutput(t *testing.T) {
	running, err := StartCommandWithInitialSinks(context.Background(), "/bin/sh", []string{
		"-c",
		"read line; printf '<child>%s</child>' \"$line\"",
	}, nil)
	if err != nil {
		t.Fatalf("StartCommandWithInitialSinks returned error: %v", err)
	}
	defer running.Close()

	sink := &recordingSink{}
	running.Hub.AddSink("test", sink)

	if err := running.Hub.WriteInput([]byte("hello\n")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}
	if err := running.Wait(); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}

	if got := string(bytes.Join(sink.chunks, nil)); strings.Count(got, "<child>hello</child>") != 1 || !strings.HasSuffix(got, "<child>hello</child>") {
		t.Fatalf("output %q does not show the expected child-only marker", got)
	}
}

func TestStartCommandWithInitialSinksCapturesImmediateOutput(t *testing.T) {
	sink := &recordingSink{}
	running, err := StartCommandWithInitialSinks(context.Background(), "/bin/sh", []string{
		"-c",
		"printf 'ready'",
	}, map[string]OutputSink{
		"initial": sink,
	})
	if err != nil {
		t.Fatalf("StartCommandWithInitialSinks returned error: %v", err)
	}
	defer running.Close()

	if err := running.Wait(); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}

	if got := string(bytes.Join(sink.chunks, nil)); got != "ready" {
		t.Fatalf("initial sink output = %q, want ready", got)
	}
}

func TestRunningCloseReapsChildProcess(t *testing.T) {
	running, err := StartCommandWithInitialSinks(context.Background(), "/bin/sh", []string{
		"-c",
		"sleep 30",
	}, nil)
	if err != nil {
		t.Fatalf("StartCommandWithInitialSinks returned error: %v", err)
	}

	if err := running.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if running.cmd.ProcessState == nil {
		t.Fatal("Close did not reap child process")
	}
}

func TestRunningCloseReturnsProcessExitWhenCommandAlreadyFinished(t *testing.T) {
	running, err := StartCommandWithInitialSinks(context.Background(), "/bin/sh", []string{
		"-c",
		"sleep 0.05; exit 7",
	}, nil)
	if err != nil {
		t.Fatalf("StartCommandWithInitialSinks returned error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := running.Close(); err == nil {
		t.Fatal("Close returned nil for a process that had already exited with failure")
	}
}

func TestRunningWaitDrainsPTYOutput(t *testing.T) {
	running, err := StartCommandWithInitialSinks(context.Background(), "/bin/sh", []string{
		"-c",
		"i=0; while [ $i -lt 8192 ]; do printf x; i=$((i+1)); done",
	}, nil)
	if err != nil {
		t.Fatalf("StartCommandWithInitialSinks returned error: %v", err)
	}
	defer running.Close()

	sink := &recordingSink{}
	running.Hub.AddSink("test", sink)

	if err := running.Wait(); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}

	got := bytes.Join(sink.chunks, nil)
	if len(got) != 8192 {
		t.Fatalf("got %d bytes, want 8192", len(got))
	}
}

func TestRunningWaitClosesPTY(t *testing.T) {
	running, err := StartCommandWithInitialSinks(context.Background(), "/bin/sh", []string{
		"-c",
		"printf done",
	}, nil)
	if err != nil {
		t.Fatalf("StartCommandWithInitialSinks returned error: %v", err)
	}

	if err := running.Wait(); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}

	if _, err := running.ptmx.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("PTY write error = %v, want %v", err, os.ErrClosed)
	}
}
