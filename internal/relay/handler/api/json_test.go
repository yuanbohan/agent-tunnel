package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONBodyRejectsTooLargeRequest(t *testing.T) {
	body := `{"value":"` + strings.Repeat("a", maxJSONBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()

	var payload struct {
		Value string `json:"value"`
	}
	err := DecodeJSONBody(rec, req, &payload)
	if err == nil {
		t.Fatal("expected oversized JSON body to fail")
	}

	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) {
		t.Fatalf("DecodeJSONBody error = %v, want *http.MaxBytesError", err)
	}
}
