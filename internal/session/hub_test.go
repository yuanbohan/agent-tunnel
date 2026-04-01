package session

import (
	"bytes"
	"testing"
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
