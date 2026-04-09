package relay

import (
	"encoding/base64"
	"time"
)

const (
	maxSessionHistoryBytes = 10 << 20
	defaultHistoryTTL      = 24 * time.Hour
)

type historyFramePayload struct {
	DataB64 string    `json:"data_b64"`
	Cols    int       `json:"cols"`
	Rows    int       `json:"rows"`
	TS      time.Time `json:"ts"`
}

type outputFrameMessage struct {
	Seq     uint64    `json:"seq"`
	DataB64 string    `json:"data_b64"`
	Cols    int       `json:"cols"`
	Rows    int       `json:"rows"`
	TS      time.Time `json:"ts"`
}

func newHistoryFramePayload(chunk []byte, cols, rows int, ts time.Time) historyFramePayload {
	return historyFramePayload{
		DataB64: base64.StdEncoding.EncodeToString(chunk),
		Cols:    cols,
		Rows:    rows,
		TS:      ts,
	}
}

func historyFrameMessages(startSeq uint64, payloads []historyFramePayload) []outputFrameMessage {
	frames := make([]outputFrameMessage, 0, len(payloads))
	seq := startSeq
	for _, payload := range payloads {
		frames = append(frames, outputFrameMessage{
			Seq:     seq,
			DataB64: payload.DataB64,
			Cols:    payload.Cols,
			Rows:    payload.Rows,
			TS:      payload.TS,
		})
		seq++
	}
	return frames
}
