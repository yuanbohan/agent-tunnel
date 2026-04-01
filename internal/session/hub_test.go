package session

import (
	"bytes"
	"testing"
)

type recordingSink struct {
	chunks [][]byte
}

func (s *recordingSink) WriteOutput(data []byte) error {
	cp := append([]byte(nil), data...)
	s.chunks = append(s.chunks, cp)
	return nil
}

func TestHubBroadcastsOutputToAllSinks(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	left := &recordingSink{}
	right := &recordingSink{}
	hub.AddSink("left", left)
	hub.AddSink("right", right)

	hub.BroadcastOutput([]byte("hello"))

	if got := bytes.Join(left.chunks, nil); string(got) != "hello" {
		t.Fatalf("left sink got %q, want hello", string(got))
	}
	if got := bytes.Join(right.chunks, nil); string(got) != "hello" {
		t.Fatalf("right sink got %q, want hello", string(got))
	}
}

func TestHubWriteInputPassesBytesToWriter(t *testing.T) {
	var got []byte
	hub := NewHub(func(data []byte) error {
		got = append([]byte(nil), data...)
		return nil
	}, func(int, int) error { return nil })

	if err := hub.WriteInput([]byte("input")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}
	if string(got) != "input" {
		t.Fatalf("got input %q, want input", string(got))
	}
}

func TestHubResizeRejectsInvalidDimensions(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	if err := hub.Resize(0, 24); err == nil {
		t.Fatal("expected an error for zero columns")
	}
	if err := hub.Resize(80, 0); err == nil {
		t.Fatal("expected an error for zero rows")
	}
}
