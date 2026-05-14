package httpx

import (
	"net/http"
	"testing"
)

func TestRequestRemoteIPUsesLastForwardedForHop(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://relay.example.com/api/auth/register", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	req.RemoteAddr = "10.0.0.10:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.250, 198.51.100.20")

	if got := RequestRemoteIP(req); got != "198.51.100.20" {
		t.Fatalf("RequestRemoteIP = %q, want last trusted proxy-appended hop", got)
	}
}

func TestRequestRemoteIPFallsBackToRemoteAddr(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://relay.example.com/api/auth/register", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	req.RemoteAddr = "198.51.100.20:54321"

	if got := RequestRemoteIP(req); got != "198.51.100.20" {
		t.Fatalf("RequestRemoteIP = %q, want RemoteAddr host", got)
	}
}
