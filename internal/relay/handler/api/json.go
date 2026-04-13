package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"yuanbohan/tunnel/internal/relay/handler/response"
)

const maxJSONBodyBytes = 1 << 20

func DecodeJSONBody(w http.ResponseWriter, r *http.Request, dest any) error {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing data")
	}
	return nil
}

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	response.Write(w, status, payload)
}

func WriteJSONError(w http.ResponseWriter, status int, reason string) {
	response.WriteError(w, status, reason)
}

func WriteJSONErrorWithMessage(w http.ResponseWriter, status int, reason, message string) {
	response.WriteErrorWithMessage(w, status, reason, message)
}

func formatRetryAfter(delay time.Duration) string {
	if delay <= 0 {
		return "0"
	}
	seconds := int(delay.Round(time.Second) / time.Second)
	if seconds <= 0 {
		return "1"
	}
	return strconv.Itoa(seconds)
}
