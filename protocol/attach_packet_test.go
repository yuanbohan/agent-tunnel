package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeTerminalBytesPacketRoundTrip(t *testing.T) {
	raw, err := EncodeTerminalBytesPacket("4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1", []byte("\x1b[31mhello"))
	if err != nil {
		t.Fatalf("EncodeTerminalBytesPacket returned error: %v", err)
	}

	packet, err := DecodeAttachPacket(raw)
	if err != nil {
		t.Fatalf("DecodeAttachPacket returned error: %v", err)
	}

	if packet.Type != AttachPacketTypeTerminalBytes {
		t.Fatalf("Type = 0x%02x, want 0x%02x", packet.Type, AttachPacketTypeTerminalBytes)
	}
	if packet.ClientID != "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1" {
		t.Fatalf("ClientID = %q, want original id", packet.ClientID)
	}
	if !bytes.Equal(packet.Payload, []byte("\x1b[31mhello")) {
		t.Fatalf("Payload = %#v, want original payload", packet.Payload)
	}
}

func TestEncodeAttachPacketRejectsInvalidClientID(t *testing.T) {
	_, err := EncodeTerminalBytesPacket("bad-client-id", []byte("hello"))
	if !errors.Is(err, ErrAttachPacketBadClientID) {
		t.Fatalf("err = %v, want ErrAttachPacketBadClientID", err)
	}
}

func TestDecodeAttachPacketRejectsShortHeader(t *testing.T) {
	_, err := DecodeAttachPacket([]byte{AttachPacketTypeTerminalBytes})
	if !errors.Is(err, ErrAttachPacketTooShort) {
		t.Fatalf("err = %v, want ErrAttachPacketTooShort", err)
	}
}

func TestDecodeAttachPacketRejectsEmptyPayload(t *testing.T) {
	raw, err := EncodeTerminalBytesPacket("4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1", nil)
	if err != nil {
		t.Fatalf("EncodeTerminalBytesPacket returned error: %v", err)
	}

	_, err = DecodeAttachPacket(raw)
	if !errors.Is(err, ErrAttachPacketEmptyPayload) {
		t.Fatalf("err = %v, want ErrAttachPacketEmptyPayload", err)
	}
}

func TestDecodeAttachPacketRejectsUnknownType(t *testing.T) {
	raw, err := EncodeTerminalBytesPacket("4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1", []byte("hello"))
	if err != nil {
		t.Fatalf("EncodeTerminalBytesPacket returned error: %v", err)
	}
	raw[0] = 0x7f

	_, err = DecodeAttachPacket(raw)
	if !errors.Is(err, ErrAttachPacketUnknownType) {
		t.Fatalf("err = %v, want ErrAttachPacketUnknownType", err)
	}
}
