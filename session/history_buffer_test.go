package session

import (
	"encoding/base64"
	"testing"
	"time"

	"yuanbohan/tunnel/protocol"
)

func TestHistoryBufferAppendAndLatestSeq(t *testing.T) {
	buffer := NewHistoryBuffer(1024)
	now := time.Unix(40, 0).UTC()

	frame := buffer.AppendOutput([]byte("hello"), 132, 43, now)

	if frame.Seq != 1 {
		t.Fatalf("Seq = %d, want 1", frame.Seq)
	}
	if frame.Cols != 132 || frame.Rows != 43 {
		t.Fatalf("size = %dx%d, want 132x43", frame.Cols, frame.Rows)
	}
	if !frame.TS.Equal(now) {
		t.Fatalf("TS = %v, want %v", frame.TS, now)
	}
	if frame.DataB64 != base64.StdEncoding.EncodeToString([]byte("hello")) {
		t.Fatalf("DataB64 = %q, want hello in base64", frame.DataB64)
	}
	if buffer.LatestSeq() != 1 {
		t.Fatalf("LatestSeq = %d, want 1", buffer.LatestSeq())
	}
}

func TestHistoryBufferSnapshotUsesInclusiveRange(t *testing.T) {
	buffer := NewHistoryBuffer(1024)
	buffer.AppendOutput([]byte("one"), 120, 40, time.Unix(40, 0).UTC())
	buffer.AppendOutput([]byte("two"), 120, 40, time.Unix(41, 0).UTC())
	frameTime := time.Unix(42, 0).UTC()
	buffer.AppendOutput([]byte("three"), 132, 43, frameTime)

	frames := buffer.Snapshot(2, true, 2, true)
	if len(frames) != 1 {
		t.Fatalf("len(Snapshot) = %d, want 1", len(frames))
	}
	if frames[0].Seq != 2 {
		t.Fatalf("Seq = %d, want 2", frames[0].Seq)
	}
	if frames[0].Cols != 120 || frames[0].Rows != 40 {
		t.Fatalf("size = %dx%d, want 120x40", frames[0].Cols, frames[0].Rows)
	}
	if frames[0].TS.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}
}

func TestHistoryBufferEvictsOldFramesButKeepsLatestSeqMonotonic(t *testing.T) {
	buffer := NewHistoryBuffer(5)

	first := buffer.AppendOutput([]byte("abc"), 120, 40, time.Unix(40, 0).UTC())
	second := buffer.AppendOutput([]byte("def"), 120, 40, time.Unix(41, 0).UTC())
	third := buffer.AppendOutput([]byte("ghi"), 120, 40, time.Unix(42, 0).UTC())

	if first.Seq != 1 || second.Seq != 2 || third.Seq != 3 {
		t.Fatalf("seqs = %d, %d, %d, want 1, 2, 3", first.Seq, second.Seq, third.Seq)
	}

	frames := buffer.Snapshot(0, false, 0, false)
	if len(frames) != 1 {
		t.Fatalf("len(Snapshot) = %d, want 1", len(frames))
	}
	if frames[0].Seq != 3 {
		t.Fatalf("remaining seq = %d, want 3", frames[0].Seq)
	}
	if buffer.LatestSeq() != 3 {
		t.Fatalf("LatestSeq = %d, want 3", buffer.LatestSeq())
	}
}

func TestEncodeHistoryResponseCopiesReplayFrames(t *testing.T) {
	frame := protocol.EncodeReplayFrame(4, []byte("hello"), 120, 40, time.Unix(50, 0).UTC())
	response := protocol.EncodeHistoryResponse("req-1", []protocol.ReplayFrame{frame})

	response.Frames[0].Seq = 99

	original := frame
	if original.Seq != 4 {
		t.Fatalf("original seq = %d, want 4", original.Seq)
	}
}
