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

type recordingSink struct {
	chunks [][]byte
}

func (s *recordingSink) WriteOutput(data []byte) error {
	s.chunks = append(s.chunks, data)
	return nil
}

type mutatingSink struct {
	seen [][]byte
}

func (s *mutatingSink) WriteOutput(data []byte) error {
	if len(data) > 0 {
		data[0] = 'x'
	}
	cp := append([]byte(nil), data...)
	s.seen = append(s.seen, cp)
	return nil
}

func TestHubBroadcastsOutputToAllSinks(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	left := &recordingSink{}
	right := &recordingSink{}
	hub.AddSink("left", left)
	hub.AddSink("right", right)

	output := []byte("hello")
	hub.BroadcastOutput(output)
	output[0] = 'j'

	if got := bytes.Join(left.chunks, nil); string(got) != "hello" {
		t.Fatalf("left sink got %q, want hello", string(got))
	}
	if got := bytes.Join(right.chunks, nil); string(got) != "hello" {
		t.Fatalf("right sink got %q, want hello", string(got))
	}
}

func TestHubBroadcastsIndependentCopiesToEachSink(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	mutator := &mutatingSink{}
	observer := &recordingSink{}
	hub.AddSink("mutator", mutator)
	hub.AddSink("observer", observer)

	hub.BroadcastOutput([]byte("hello"))

	if got := bytes.Join(observer.chunks, nil); string(got) != "hello" {
		t.Fatalf("observer sink got %q, want hello", string(got))
	}
	if got := bytes.Join(mutator.seen, nil); string(got) != "xello" {
		t.Fatalf("mutator sink saw %q, want xello", string(got))
	}
}

func TestHubWriteInputPassesBytesToWriter(t *testing.T) {
	var got []byte
	hub := NewHub(func(data []byte) error {
		got = data
		return nil
	}, func(int, int) error { return nil })

	input := []byte("input")
	if err := hub.WriteInput(input); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}
	input[0] = 'o'
	if string(got) != "input" {
		t.Fatalf("got input %q, want input", string(got))
	}
}

func TestHubResizeRejectsInvalidDimensions(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	tests := []struct {
		name string
		cols int
		rows int
		want string
	}{
		{name: "zero columns", cols: 0, rows: 24, want: "invalid resize 0x24"},
		{name: "zero rows", cols: 80, rows: 0, want: "invalid resize 80x0"},
		{name: "negative rows", cols: 80, rows: -1, want: "invalid resize 80x-1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := hub.Resize(tc.cols, tc.rows)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("Resize error = %q, want %q", got, tc.want)
			}
		})
	}
}

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

func TestHubCurrentSizeReturnsZeroBeforeFirstResize(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	cols, rows := hub.CurrentSize()
	if cols != 0 || rows != 0 {
		t.Fatalf("CurrentSize = %dx%d, want 0x0", cols, rows)
	}
}

func TestHubResizeStoresCurrentSize(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	if err := hub.Resize(120, 40); err != nil {
		t.Fatalf("Resize returned error: %v", err)
	}

	cols, rows := hub.CurrentSize()
	if cols != 120 || rows != 40 {
		t.Fatalf("CurrentSize = %dx%d, want 120x40", cols, rows)
	}
}

func TestHubResizeCallsOnResizeCallback(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	var gotCols, gotRows int
	hub.OnResize(func(cols, rows int) {
		gotCols = cols
		gotRows = rows
	})

	if err := hub.Resize(100, 50); err != nil {
		t.Fatalf("Resize returned error: %v", err)
	}

	if gotCols != 100 || gotRows != 50 {
		t.Fatalf("OnResize callback got %dx%d, want 100x50", gotCols, gotRows)
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
