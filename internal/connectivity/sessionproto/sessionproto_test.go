package sessionproto

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestPayloadsRoundTripAndIgnoreFutureFields(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		want    any
	}{
		{name: "hello", payload: Hello{ProtocolVersion: ProtocolVersion, ActorType: ActorMobile, ClientFingerprint: "abc123", PathKind: PathRelay}, want: Hello{ProtocolVersion: ProtocolVersion, ActorType: ActorMobile, ClientFingerprint: "abc123", PathKind: PathRelay}},
		{name: "session_index", payload: SessionIndex{Sessions: []SessionMetadata{sessionMetadata()}}, want: SessionIndex{Sessions: []SessionMetadata{sessionMetadata()}}},
		{name: "session_upsert", payload: SessionUpsert{Session: sessionMetadata()}, want: SessionUpsert{Session: sessionMetadata()}},
		{name: "session_gone", payload: SessionGone{SessionID: "session-1"}, want: SessionGone{SessionID: "session-1"}},
		{name: "preview_subscribe", payload: PreviewSubscribe{SessionID: "session-1"}, want: PreviewSubscribe{SessionID: "session-1"}},
		{name: "preview_unsubscribe", payload: PreviewUnsubscribe{SessionID: "session-1"}, want: PreviewUnsubscribe{SessionID: "session-1"}},
		{name: "preview_snapshot", payload: PreviewSnapshot{SessionID: "session-1", Preview: "latest", UpdatedAt: 11}, want: PreviewSnapshot{SessionID: "session-1", Preview: "latest", UpdatedAt: 11}},
		{name: "interactive_request", payload: InteractiveRequest{SessionID: "session-1", Cols: 120, Rows: 40}, want: InteractiveRequest{SessionID: "session-1", Cols: 120, Rows: 40}},
		{name: "interactive_granted", payload: InteractiveGranted{SessionID: "session-1", InteractiveStreamID: 4, Cols: 120, Rows: 40}, want: InteractiveGranted{SessionID: "session-1", InteractiveStreamID: 4, Cols: 120, Rows: 40}},
		{name: "interactive_denied", payload: InteractiveDenied{SessionID: "session-1", Reason: "daemon_busy"}, want: InteractiveDenied{SessionID: "session-1", Reason: "daemon_busy"}},
		{name: "interactive_release", payload: InteractiveRelease{SessionID: "session-1"}, want: InteractiveRelease{SessionID: "session-1"}},
		{name: "input_text", payload: InputText{SessionID: "session-1", Text: "echo hi"}, want: InputText{SessionID: "session-1", Text: "echo hi"}},
		{name: "input_key", payload: InputKey{SessionID: "session-1", Key: "enter"}, want: InputKey{SessionID: "session-1", Key: "enter"}},
		{name: "resize", payload: Resize{SessionID: "session-1", Cols: 100, Rows: 30}, want: Resize{SessionID: "session-1", Cols: 100, Rows: 30}},
		{name: "path_state", payload: PathState{AttemptID: "attempt-1", PathKind: PathRelay, FallbackReason: "direct_timeout", DirectSetupLatencyMS: 3000, RelaySetupLatencyMS: 120}, want: PathState{AttemptID: "attempt-1", PathKind: PathRelay, FallbackReason: "direct_timeout", DirectSetupLatencyMS: 3000, RelaySetupLatencyMS: 120}},
		{name: "error", payload: Error{Code: "protocol_version_mismatch", Message: "bad version"}, want: Error{Code: "protocol_version_mismatch", Message: "bad version"}},
		{name: "snapshot_begin", payload: SnapshotBegin{SessionID: "session-1", Cols: 120, Rows: 40}, want: SnapshotBegin{SessionID: "session-1", Cols: 120, Rows: 40}},
		{name: "snapshot_end", payload: SnapshotEnd{SessionID: "session-1", ChunkCount: 2}, want: SnapshotEnd{SessionID: "session-1", ChunkCount: 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.payload)
			if err != nil {
				t.Fatalf("Marshal returned error: %v", err)
			}
			var withFuture map[string]any
			if err := json.Unmarshal(raw, &withFuture); err != nil {
				t.Fatalf("Unmarshal map returned error: %v", err)
			}
			withFuture["future_field"] = "ignored"
			raw, err = json.Marshal(withFuture)
			if err != nil {
				t.Fatalf("Marshal future map returned error: %v", err)
			}

			got := reflect.New(reflect.TypeOf(tt.want)).Interface()
			if err := json.Unmarshal(raw, got); err != nil {
				t.Fatalf("Unmarshal returned error: %v", err)
			}
			gotValue := reflect.ValueOf(got).Elem().Interface()
			if !reflect.DeepEqual(gotValue, tt.want) {
				t.Fatalf("got %#v, want %#v", gotValue, tt.want)
			}
		})
	}
}

func TestSessionMetadataDoesNotCarryPreviewText(t *testing.T) {
	raw, err := json.Marshal(sessionMetadata())
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if _, ok := fields["preview"]; ok {
		t.Fatal("SessionMetadata JSON contains preview field")
	}
	if _, ok := fields["latest_preview"]; ok {
		t.Fatal("SessionMetadata JSON contains latest_preview field")
	}
}

func TestSessionProtocolPayloadsDoNotCarryTierPolicyFields(t *testing.T) {
	forbiddenKeys := map[string]struct{}{
		"tier":                {},
		"locked":              {},
		"unlocked":            {},
		"unlocked_session_id": {},
		"policy":              {},
		"subscription":        {},
		"entitlement":         {},
	}
	forbiddenTokens := [][]byte{
		[]byte("policy_locked_session"),
		[]byte("policy_determining_available_session"),
		[]byte("unlocked_session_id"),
	}

	tests := []struct {
		name    string
		payload any
	}{
		{name: "hello", payload: Hello{ProtocolVersion: ProtocolVersion, ActorType: ActorMobile, ClientFingerprint: "abc123", PathKind: PathRelay}},
		{name: "session_index", payload: SessionIndex{Sessions: []SessionMetadata{sessionMetadata()}}},
		{name: "session_upsert", payload: SessionUpsert{Session: sessionMetadata()}},
		{name: "preview_snapshot", payload: PreviewSnapshot{SessionID: "session-1", Preview: "latest", UpdatedAt: 11}},
		{name: "interactive_request", payload: InteractiveRequest{SessionID: "session-1", Cols: 120, Rows: 40}},
		{name: "interactive_granted", payload: InteractiveGranted{SessionID: "session-1", InteractiveStreamID: 4, Cols: 120, Rows: 40}},
		{name: "path_state", payload: PathState{AttemptID: "attempt-1", PathKind: PathRelay, FallbackReason: "direct_timeout"}},
		{name: "error", payload: Error{Code: "protocol_version_mismatch", Message: "bad version"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.payload)
			if err != nil {
				t.Fatalf("Marshal returned error: %v", err)
			}
			for _, token := range forbiddenTokens {
				if bytes.Contains(raw, token) {
					t.Fatalf("payload JSON %s contains legacy token %q", raw, token)
				}
			}
			var decoded any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("Unmarshal returned error: %v", err)
			}
			assertNoForbiddenJSONKeys(t, decoded, forbiddenKeys)
		})
	}
}

func TestMalformedJSONFailsDecode(t *testing.T) {
	var payload Hello
	if err := json.Unmarshal([]byte(`{"protocol_version":`), &payload); err == nil {
		t.Fatal("Unmarshal succeeded, want malformed JSON error")
	}
}

func assertNoForbiddenJSONKeys(t *testing.T, value any, forbidden map[string]struct{}) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, ok := forbidden[key]; ok {
				t.Fatalf("JSON contains forbidden key %q", key)
			}
			assertNoForbiddenJSONKeys(t, child, forbidden)
		}
	case []any:
		for _, child := range typed {
			assertNoForbiddenJSONKeys(t, child, forbidden)
		}
	}
}

func sessionMetadata() SessionMetadata {
	return SessionMetadata{
		SessionID:      "session-1",
		Label:          "Terminal",
		CommandPreview: "codex",
		CWD:            "/workspace/project",
		GitBranch:      "feat/connectivity",
		StartedAt:      1_700_000_000,
		UpdatedAt:      1_700_000_010,
		Online:         true,
	}
}
