package pairing

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestComputeSASGoldenVectors(t *testing.T) {
	tests := []struct {
		name         string
		daemonPub    []byte
		androidPub   []byte
		invitationID string
		nonce        []byte
		want         string
	}{
		{
			name:         "ascending keys",
			daemonPub:    bytesFromRange(0, 32),
			androidPub:   bytesFromRange(32, 64),
			invitationID: "invite-0001",
			nonce:        mustHex(t, "000102030405060708090a0b0c0d0e0f"),
			want:         "696700",
		},
		{
			name:         "boundary shaped ids",
			daemonPub:    descendingBytes(0xff, 32),
			androidPub:   multipliedBytes(3, 32),
			invitationID: "edge-boundary",
			nonce:        mustHex(t, "101112131415161718191a1b1c1d1e1f"),
			want:         "626209",
		},
		{
			name:         "high bit keys",
			daemonPub:    bytes.Repeat([]byte{0x7f}, 32),
			androidPub:   bytes.Repeat([]byte{0x80}, 32),
			invitationID: "unicode-safe-ascii",
			nonce:        mustHex(t, "ffffffff00000000aaaaaaaa55555555"),
			want:         "670900",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ComputeSAS(tt.daemonPub, tt.androidPub, tt.invitationID, tt.nonce)
			if err != nil {
				t.Fatalf("ComputeSAS returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ComputeSAS = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCanonicalSASInputLengthPrefixesBoundaries(t *testing.T) {
	first, err := CanonicalSASInput(
		append(bytes.Repeat([]byte{'A'}, 31), 'B'),
		bytes.Repeat([]byte{'C'}, 32),
		"xy",
		[]byte("z"),
	)
	if err != nil {
		t.Fatalf("CanonicalSASInput returned error: %v", err)
	}

	second, err := CanonicalSASInput(
		bytes.Repeat([]byte{'A'}, 31),
		append([]byte{'B'}, bytes.Repeat([]byte{'C'}, 31)...),
		"xy",
		[]byte("z"),
	)
	if err != nil {
		t.Fatalf("CanonicalSASInput returned error: %v", err)
	}

	if bytes.Equal(first, second) {
		t.Fatal("canonical inputs collided across different field boundaries")
	}
	if !bytes.HasPrefix(first, []byte{0x00, 0x20}) {
		t.Fatalf("canonical input starts with %#v, want u16 length prefix 32", first[:2])
	}
}

func TestComputeSASRejectsInvalidInputs(t *testing.T) {
	_, err := ComputeSAS(bytes.Repeat([]byte{1}, 31), bytes.Repeat([]byte{2}, 32), "invite", []byte("nonce"))
	if !errors.Is(err, ErrInvalidPublicKeyLength) {
		t.Fatalf("err = %v, want ErrInvalidPublicKeyLength", err)
	}

	_, err = ComputeSAS(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), strings.Repeat("a", 1<<16), []byte("nonce"))
	if !errors.Is(err, ErrSASInputTooLong) {
		t.Fatalf("err = %v, want ErrSASInputTooLong", err)
	}
}

func mustHex(t *testing.T, raw string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		t.Fatalf("DecodeString(%q): %v", raw, err)
	}
	return decoded
}

func bytesFromRange(start, end int) []byte {
	out := make([]byte, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, byte(i))
	}
	return out
}

func descendingBytes(start byte, count int) []byte {
	out := make([]byte, count)
	for i := range out {
		out[i] = start - byte(i)
	}
	return out
}

func multipliedBytes(multiplier byte, count int) []byte {
	out := make([]byte, count)
	for i := range out {
		out[i] = byte(i) * multiplier
	}
	return out
}
