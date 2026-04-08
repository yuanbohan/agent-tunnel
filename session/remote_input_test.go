package session

import (
	"bytes"
	"testing"
)

func TestEncodeRemoteTextInputReturnsUTF8Bytes(t *testing.T) {
	got := EncodeRemoteTextInput("hello", false)
	if string(got) != "hello" {
		t.Fatalf("got %q, want hello", string(got))
	}
}

func TestEncodeRemoteTextInputAppendsEnterBytesWhenSubmitIsTrue(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []byte
	}{
		{name: "plain text", text: "hello", want: []byte("hello\r")},
		{name: "multiline text", text: "line1\nline2", want: []byte("line1\nline2\r")},
		{name: "empty text", text: "", want: []byte{'\r'}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EncodeRemoteTextInput(tc.text, true)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestEncodeRemoteKeyInputMapsSupportedKeys(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		ctrl  bool
		alt   bool
		shift bool
		want  []byte
	}{
		{name: "tab", key: "TAB", want: []byte{'\t'}},
		{name: "enter", key: "ENTER", want: []byte{'\r'}},
		{name: "backspace", key: "BACKSPACE", want: []byte{0x7f}},
		{name: "escape", key: "ESCAPE", want: []byte{0x1b}},
		{name: "up", key: "UP", want: []byte("\x1b[A")},
		{name: "delete", key: "DELETE", want: []byte("\x1b[3~")},
		{name: "ctrl-c", key: "C", ctrl: true, want: []byte{0x03}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := EncodeRemoteKeyInput(tc.key, tc.ctrl, tc.alt, tc.shift)
			if !ok {
				t.Fatal("expected supported key")
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestEncodeRemoteKeyInputRejectsUnsupportedKeys(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		ctrl  bool
		alt   bool
		shift bool
	}{
		{name: "plain character", key: "a"},
		{name: "alt combo", key: "X", alt: true},
		{name: "ctrl non letter", key: "1", ctrl: true},
		{name: "empty", key: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := EncodeRemoteKeyInput(tc.key, tc.ctrl, tc.alt, tc.shift)
			if ok {
				t.Fatalf("got supported result %#v, want unsupported", got)
			}
		})
	}
}
