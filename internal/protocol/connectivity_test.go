package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConnectivityNonSnapshotFramesOmitDaemonList(t *testing.T) {
	raw, err := json.Marshal(ConnectivityErrorFrame("req-1", "unsupported_event"))
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(raw), "daemons") {
		t.Fatalf("error frame JSON = %s, want daemons omitted", raw)
	}
}

func TestConnectivityEmptySnapshotIncludesDaemonList(t *testing.T) {
	raw, err := json.Marshal(ConnectivityDaemonSnapshotFrame(nil))
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if !strings.Contains(string(raw), `"daemons":[]`) {
		t.Fatalf("snapshot frame JSON = %s, want empty daemons array", raw)
	}
}

func TestConnectivityRateLimitErrorIncludesRetryAfter(t *testing.T) {
	raw, err := json.Marshal(ConnectivityErrorFrameWithRetryAfter("req-1", "relay_rate_limited", 60))
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if !strings.Contains(string(raw), `"retry_after_seconds":60`) {
		t.Fatalf("rate-limit frame JSON = %s, want retry_after_seconds", raw)
	}
}
