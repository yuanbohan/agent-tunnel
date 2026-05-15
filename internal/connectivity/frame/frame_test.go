package frame

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"yuanbohan/tunnel/internal/connectivity/sessionproto"
)

const (
	ssotFrameTypesSource = "https://github.com/yuanbohan/agent-tunnel-protocols"
	ssotProtocolVersion  = 2
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	raw, err := Encode(Frame{Type: TypeHello, Payload: []byte(`{"protocol_version":1,"unknown":"ok"}`)})
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	got, consumed, err := Decode(raw, DefaultMaxPayload)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if consumed != len(raw) {
		t.Fatalf("consumed = %d, want %d", consumed, len(raw))
	}
	if got.Type != TypeHello {
		t.Fatalf("Type = 0x%02x, want 0x%02x", got.Type, TypeHello)
	}
	if string(got.Payload) != `{"protocol_version":1,"unknown":"ok"}` {
		t.Fatalf("Payload = %q", got.Payload)
	}
}

func TestDecodeToleratesUnknownFrameType(t *testing.T) {
	raw, err := Encode(Frame{Type: 0xfe, Payload: []byte("opaque")})
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	got, _, err := Decode(raw, DefaultMaxPayload)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if got.Type != 0xfe || string(got.Payload) != "opaque" {
		t.Fatalf("frame = %#v, want unknown type with payload", got)
	}
}

func TestStep4FrameTypeRegistry(t *testing.T) {
	tests := []struct {
		name string
		typ  byte
	}{
		{name: "hello", typ: TypeHello},
		{name: "session_index", typ: TypeSessionIndex},
		{name: "preview_subscribe", typ: TypePreviewSubscribe},
		{name: "session_upsert", typ: TypeSessionUpsert},
		{name: "session_gone", typ: TypeSessionGone},
		{name: "preview_unsubscribe", typ: TypePreviewUnsubscribe},
		{name: "preview_snapshot", typ: TypePreviewSnapshot},
		{name: "interactive_request", typ: TypeInteractiveRequest},
		{name: "interactive_granted", typ: TypeInteractiveGranted},
		{name: "interactive_denied", typ: TypeInteractiveDenied},
		{name: "interactive_release", typ: TypeInteractiveRelease},
		{name: "input_text", typ: TypeInputText},
		{name: "input_key", typ: TypeInputKey},
		{name: "resize", typ: TypeResize},
		{name: "path_state", typ: TypePathState},
		{name: "snapshot_begin", typ: TypeSnapshotBegin},
		{name: "snapshot_chunk", typ: TypeSnapshotChunk},
		{name: "live_bytes", typ: TypeLiveBytes},
		{name: "snapshot_end", typ: TypeSnapshotEnd},
		{name: "error", typ: TypeError},
	}

	seen := make(map[byte]string)
	for _, tt := range tests {
		if previous, ok := seen[tt.typ]; ok {
			t.Fatalf("frame type 0x%02x reused by %s and %s", tt.typ, previous, tt.name)
		}
		seen[tt.typ] = tt.name
	}
}

func TestSSOTFrameTypeRegistryMatchesLocalMirrorForProtocolV2(t *testing.T) {
	if ssotProtocolVersion == 0 || ssotFrameTypesSource == "" {
		t.Fatal("SSOT frame mirror provenance is missing")
	}
	if ssotFrameTypesSource != ssotProtocolSource {
		t.Fatalf("ssotFrameTypesSource=%q, want %q", ssotFrameTypesSource, ssotProtocolSource)
	}
	if ssotProtocolVersion != ssotProtocolCompatibilityV2 {
		t.Fatalf("ssotProtocolVersion=%d, want %d", ssotProtocolVersion, ssotProtocolCompatibilityV2)
	}
	if ssotProtocolVersion != sessionproto.ProtocolVersion {
		t.Fatalf("ssotProtocolVersion=%d, want %d", ssotProtocolVersion, sessionproto.ProtocolVersion)
	}
	if ssotProtocolVersion != 2 {
		t.Fatalf("ssotProtocolVersion=%d, want 2", ssotProtocolVersion)
	}

	expected := map[string]byte{
		"hello":              0x01,
		"session_index":       0x02,
		"preview_subscribe":   0x03,
		"session_upsert":      0x04,
		"session_gone":        0x05,
		"preview_unsubscribe": 0x06,
		"preview_snapshot":    0x07,
		"interactive_request": 0x08,
		"interactive_granted": 0x09,
		"interactive_denied":  0x0a,
		"interactive_release": 0x0b,
		"input_text":          0x0c,
		"input_key":           0x0d,
		"resize":              0x0e,
		"path_state":          0x0f,
		"snapshot_begin":      0x10,
		"snapshot_chunk":      0x11,
		"live_bytes":          0x12,
		"snapshot_end":        0x13,
		"error":               0x7f,
	}

	got := map[string]byte{
		"hello":              TypeHello,
		"session_index":      TypeSessionIndex,
		"preview_subscribe":  TypePreviewSubscribe,
		"session_upsert":     TypeSessionUpsert,
		"session_gone":       TypeSessionGone,
		"preview_unsubscribe": TypePreviewUnsubscribe,
		"preview_snapshot":    TypePreviewSnapshot,
		"interactive_request": TypeInteractiveRequest,
		"interactive_granted": TypeInteractiveGranted,
		"interactive_denied":  TypeInteractiveDenied,
		"interactive_release": TypeInteractiveRelease,
		"input_text":          TypeInputText,
		"input_key":           TypeInputKey,
		"resize":              TypeResize,
		"path_state":          TypePathState,
		"snapshot_begin":      TypeSnapshotBegin,
		"snapshot_chunk":      TypeSnapshotChunk,
		"live_bytes":          TypeLiveBytes,
		"snapshot_end":        TypeSnapshotEnd,
		"error":               TypeError,
	}

	if len(got) != len(expected) {
		t.Fatalf("mirror registry size = %d, want %d", len(got), len(expected))
	}
	for name, expectedType := range expected {
		if got[name] != expectedType {
			t.Fatalf("SSOT frame type mismatch for %s: got 0x%02x, want 0x%02x", name, got[name], expectedType)
		}
	}
}

func TestDecodeRejectsTruncatedVarint(t *testing.T) {
	_, _, err := Decode([]byte{TypeHello, 0x40}, DefaultMaxPayload)
	if !errors.Is(err, ErrTruncatedVarint) {
		t.Fatalf("err = %v, want ErrTruncatedVarint", err)
	}
}

func TestDecodeRejectsOversizedDeclaredLength(t *testing.T) {
	raw, err := Encode(Frame{Type: TypeHello, Payload: bytes.Repeat([]byte{'x'}, 9)})
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	_, _, err = Decode(raw, 8)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

func TestDecodeRejectsIncompletePayload(t *testing.T) {
	raw, err := Encode(Frame{Type: TypeHello, Payload: []byte("hello")})
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	_, _, err = Decode(raw[:len(raw)-1], DefaultMaxPayload)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestEncodeDecodeVarintLengthBoundaries(t *testing.T) {
	for _, size := range []int{0, 63, 64, 16_383, 16_384, DefaultMaxPayload} {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			payload := bytes.Repeat([]byte{'x'}, size)
			raw, err := Encode(Frame{Type: TypeLiveBytes, Payload: payload})
			if err != nil {
				t.Fatalf("Encode returned error: %v", err)
			}
			got, consumed, err := Decode(raw, DefaultMaxPayload)
			if err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if consumed != len(raw) {
				t.Fatalf("consumed = %d, want %d", consumed, len(raw))
			}
			if !bytes.Equal(got.Payload, payload) {
				t.Fatalf("payload length = %d, want %d", len(got.Payload), len(payload))
			}
		})
	}
}

func TestReadRejectsTruncatedHeaderAndOversizedPayload(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  []byte
	}{
		{name: "empty", raw: nil},
		{name: "one byte header", raw: []byte{TypeHello}},
		{name: "two byte varint missing second byte", raw: []byte{TypeHello, 0x40}},
		{name: "four byte varint missing trailing bytes", raw: []byte{TypeHello, 0x80, 0x00}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Read(bytes.NewReader(tt.raw), DefaultMaxPayload)
			if !errors.Is(err, ErrTruncatedVarint) {
				t.Fatalf("err = %v, want ErrTruncatedVarint", err)
			}
		})
	}

	raw, err := Encode(Frame{Type: TypeHello, Payload: bytes.Repeat([]byte{'x'}, 9)})
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	_, err = Read(bytes.NewReader(raw), 8)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

func TestReadRejectsIncompletePayload(t *testing.T) {
	raw, err := Encode(Frame{Type: TypeHello, Payload: []byte("hello")})
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	for _, tt := range []struct {
		name string
		raw  []byte
	}{
		{name: "missing entire payload", raw: raw[:2]},
		{name: "missing partial payload", raw: raw[:len(raw)-1]},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Read(bytes.NewReader(tt.raw), DefaultMaxPayload)
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
			}
		})
	}
}

func TestReadWriteRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, Frame{Type: TypeLiveBytes, Payload: []byte("\x1b[31mhello")}); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	got, err := Read(&buf, DefaultMaxPayload)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if got.Type != TypeLiveBytes || !bytes.Equal(got.Payload, []byte("\x1b[31mhello")) {
		t.Fatalf("frame = %#v, want live bytes frame", got)
	}
}
