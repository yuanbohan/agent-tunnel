package session

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"
)

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

func TestHubWriteInputSequencePreservesOrderAndCopiesChunks(t *testing.T) {
	var got [][]byte
	hub := NewHub(func(data []byte) error {
		got = append(got, data)
		return nil
	}, func(int, int) error { return nil })

	text := []byte("hello")
	enter := []byte{'\r'}
	if err := hub.WriteInputSequence(text, enter); err != nil {
		t.Fatalf("WriteInputSequence returned error: %v", err)
	}

	text[0] = 'j'
	enter[0] = '\n'

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if string(got[0]) != "hello" {
		t.Fatalf("first chunk = %q, want hello", string(got[0]))
	}
	if string(got[1]) != "\r" {
		t.Fatalf("second chunk = %q, want \\r", string(got[1]))
	}
}

func TestHubWriteInputNotifiesInputObserverWithCopyAndResult(t *testing.T) {
	events := make([]string, 0, 3)
	hub := NewHub(func(data []byte) error {
		events = append(events, "write:"+string(data))
		return nil
	}, func(int, int) error { return nil })
	hub.SetInputObserver(func(data []byte) func(error) {
		events = append(events, "observe:"+string(data))
		if len(data) > 0 {
			data[0] = 'x'
		}
		return func(err error) {
			if err != nil {
				events = append(events, "done:error")
				return
			}
			events = append(events, "done:ok")
		}
	})

	input := []byte("hello")
	if err := hub.WriteInput(input); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}
	input[0] = 'j'

	if !reflect.DeepEqual(events, []string{"observe:hello", "write:hello", "done:ok"}) {
		t.Fatalf("events = %#v, want observer before write and completion after write", events)
	}
}

func TestHubWriteInputNotifiesInputObserverOnWriteError(t *testing.T) {
	writeErr := errors.New("stop")
	events := make([]string, 0, 3)
	hub := NewHub(func(data []byte) error {
		events = append(events, "write:"+string(data))
		return writeErr
	}, func(int, int) error { return nil })
	hub.SetInputObserver(func(data []byte) func(error) {
		events = append(events, "observe:"+string(data))
		return func(err error) {
			if !errors.Is(err, writeErr) {
				t.Fatalf("completion error = %v, want %v", err, writeErr)
			}
			events = append(events, "done:error")
		}
	})

	if err := hub.WriteInput([]byte("hello")); !errors.Is(err, writeErr) {
		t.Fatalf("WriteInput error = %v, want %v", err, writeErr)
	}
	if !reflect.DeepEqual(events, []string{"observe:hello", "write:hello", "done:error"}) {
		t.Fatalf("events = %#v, want observer before failed write and completion after write", events)
	}
}

func TestHubClearInputObserverStopsNotifications(t *testing.T) {
	calls := 0
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	hub.SetInputObserver(func([]byte) func(error) {
		calls++
		return nil
	})
	hub.SetInputObserver(nil)

	if err := hub.WriteInput([]byte("hello")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}
	if calls != 0 {
		t.Fatalf("listener calls = %d, want 0 after removal", calls)
	}
}

func TestHubWriteInputSequenceWithGapBlocksInterleavingWrites(t *testing.T) {
	wroteText := make(chan struct{}, 1)
	got := make([]string, 0, 3)
	hub := NewHub(func(data []byte) error {
		got = append(got, string(data))
		if string(data) == "hello" {
			select {
			case wroteText <- struct{}{}:
			default:
			}
		}
		return nil
	}, func(int, int) error { return nil })

	sequenceDone := make(chan error, 1)
	go func() {
		sequenceDone <- hub.WriteInputSequenceWithGap(50*time.Millisecond, []byte("hello"), []byte{'\r'})
	}()

	select {
	case <-wroteText:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first submit chunk")
	}

	interleavingDone := make(chan error, 1)
	go func() {
		interleavingDone <- hub.WriteInput([]byte("!"))
	}()

	select {
	case err := <-interleavingDone:
		t.Fatalf("interleaving write finished early with err=%v", err)
	case <-time.After(20 * time.Millisecond):
	}

	if err := <-sequenceDone; err != nil {
		t.Fatalf("WriteInputSequenceWithGap returned error: %v", err)
	}
	if err := <-interleavingDone; err != nil {
		t.Fatalf("interleaving WriteInput returned error: %v", err)
	}

	if !reflect.DeepEqual(got, []string{"hello", "\r", "!"}) {
		t.Fatalf("writes = %#v, want %#v", got, []string{"hello", "\r", "!"})
	}
}

func TestHubWriteInputSequenceWithGapSleepsOnlyBetweenNonEmptyChunks(t *testing.T) {
	got := make([]string, 0, 2)
	slept := make([]time.Duration, 0, 1)
	hub := NewHub(func(data []byte) error {
		got = append(got, string(data))
		return nil
	}, func(int, int) error { return nil })
	hub.sleep = func(d time.Duration) {
		slept = append(slept, d)
	}

	if err := hub.WriteInputSequenceWithGap(25*time.Millisecond, nil, []byte("hello"), []byte{}, []byte{'\r'}, nil); err != nil {
		t.Fatalf("WriteInputSequenceWithGap returned error: %v", err)
	}

	if !reflect.DeepEqual(got, []string{"hello", "\r"}) {
		t.Fatalf("writes = %#v, want %#v", got, []string{"hello", "\r"})
	}
	if !reflect.DeepEqual(slept, []time.Duration{25 * time.Millisecond}) {
		t.Fatalf("slept = %#v, want one gap between non-empty writes", slept)
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

func TestHubResizeCallsAllResizeListeners(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	got := make(map[string][2]int)
	hub.AddResizeListener("status", func(cols, rows int) {
		got["status"] = [2]int{cols, rows}
	})
	hub.AddResizeListener("mirror", func(cols, rows int) {
		got["mirror"] = [2]int{cols, rows}
	})

	if err := hub.Resize(132, 43); err != nil {
		t.Fatalf("Resize returned error: %v", err)
	}

	if !reflect.DeepEqual(got["status"], [2]int{132, 43}) {
		t.Fatalf("status listener got %v, want %v", got["status"], [2]int{132, 43})
	}
	if !reflect.DeepEqual(got["mirror"], [2]int{132, 43}) {
		t.Fatalf("mirror listener got %v, want %v", got["mirror"], [2]int{132, 43})
	}
}

func TestHubRemoveResizeListenerStopsCallbacks(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	calls := 0
	hub.AddResizeListener("status", func(cols, rows int) {
		calls++
	})
	hub.RemoveResizeListener("status")

	if err := hub.Resize(120, 40); err != nil {
		t.Fatalf("Resize returned error: %v", err)
	}

	if calls != 0 {
		t.Fatalf("calls = %d, want 0 after removal", calls)
	}
}
