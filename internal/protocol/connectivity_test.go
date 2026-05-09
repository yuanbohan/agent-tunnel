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
	if strings.Contains(string(raw), "computers") {
		t.Fatalf("error frame JSON = %s, want computers omitted", raw)
	}
}

func TestConnectivityEmptySnapshotIncludesDaemonList(t *testing.T) {
	raw, err := json.Marshal(ConnectivityDaemonSnapshotFrame(nil))
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if !strings.Contains(string(raw), `"computers":[]`) {
		t.Fatalf("snapshot frame JSON = %s, want empty computers array", raw)
	}
}

func TestConnectivityFrameAcceptsLegacyDaemonRegisterJSON(t *testing.T) {
	raw := []byte(`{"type":"daemon_register","protocol_version":2,"daemon":{"computer_id":"dev-1","computer_public_key":"key","computer_fingerprint":"fp"},"daemons":[{"computer_id":"dev-2","computer_public_key":"key2","computer_fingerprint":"fp2"}]}`)
	var frame ConnectivityFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if frame.Type != "daemon_register" || frame.Daemon == nil || frame.Daemon.DeviceID != "dev-1" || len(frame.Daemons) != 1 || frame.Daemons[0].DeviceID != "dev-2" {
		t.Fatalf("frame = %#v, want legacy daemon fields decoded into computer fields", frame)
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

func TestConnectivityDaemonRegisterUsesProtocolVersionTwoAndComputerKeys(t *testing.T) {
	raw, err := json.Marshal(ConnectivityDaemonRegisterFrame(ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DisplayName:       "Laptop",
		DaemonPublicKey:   "computer-key",
		DaemonFingerprint: "computer-fingerprint",
	}, []ConnectivityTrustedAndroid{{Fingerprint: "client-fingerprint", DisplayName: "Phone"}}))
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got := string(raw)
	for _, want := range []string{
		`"type":"computer_register"`,
		`"protocol_version":2`,
		`"computer":{`,
		`"computer_id":"dev-1"`,
		`"computer_public_key":"computer-key"`,
		`"computer_fingerprint":"computer-fingerprint"`,
		`"trusted_clients":[`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("daemon register JSON = %s, want %s", got, want)
		}
	}
	for _, old := range []string{"device_id", "daemon_public_key", "daemon_fingerprint", "trusted_devices", `"daemon":`} {
		if strings.Contains(got, old) {
			t.Fatalf("daemon register JSON = %s, did not expect old key %q", got, old)
		}
	}
}

func TestConnectivityPairingResponseUsesClientKeys(t *testing.T) {
	raw, err := json.Marshal(ConnectivityPairingResponse{
		AccountID:          "1",
		InvitationID:       "pair-1",
		CorrelationID:      "corr-1",
		AndroidPublicKey:   "client-key",
		AndroidFingerprint: "client-fingerprint",
		AndroidDisplayName: "Phone",
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got := string(raw)
	for _, want := range []string{
		`"client_public_key":"client-key"`,
		`"client_fingerprint":"client-fingerprint"`,
		`"client_display_name":"Phone"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("pairing response JSON = %s, want %s", got, want)
		}
	}
	for _, old := range []string{"android_pubkey", "android_public_key", "android_fingerprint", "android_display_name"} {
		if strings.Contains(got, old) {
			t.Fatalf("pairing response JSON = %s, did not expect old key %q", got, old)
		}
	}
}

func TestConnectivityRealtimeFramesUseClientAndComputerKeys(t *testing.T) {
	tests := []struct {
		name  string
		frame ConnectivityFrame
		want  []string
		old   []string
	}{
		{
			name:  "rendezvous_hint",
			frame: ConnectivityRendezvousHintFrame("req-1", "attempt-1", "client", "computer-1", "client-fingerprint", "203.0.113.1:1234", []string{"10.0.0.2:1234"}, 100),
			want:  []string{`"computer_id":"computer-1"`, `"client_fingerprint":"client-fingerprint"`},
			old:   []string{"device_id", "android_fingerprint", "daemon_id"},
		},
		{
			name:  "direct_session_open",
			frame: ConnectivityDirectSessionOpenFrame("req-1", "attempt-1", "computer-1", "client-fingerprint"),
			want:  []string{`"computer_id":"computer-1"`, `"client_fingerprint":"client-fingerprint"`},
			old:   []string{"device_id", "android_fingerprint", "daemon_id"},
		},
		{
			name:  "direct_session_close",
			frame: ConnectivityDirectSessionCloseFrame("req-1", "attempt-1", "computer-1", "client-fingerprint"),
			want:  []string{`"computer_id":"computer-1"`, `"client_fingerprint":"client-fingerprint"`},
			old:   []string{"device_id", "android_fingerprint", "daemon_id"},
		},
		{
			name:  "pair_completed",
			frame: ConnectivityPairCompletedFrame("client-fingerprint"),
			want:  []string{`"client_fingerprint":"client-fingerprint"`},
			old:   []string{"android_fingerprint"},
		},
		{
			name:  "relay_tunnel_ready",
			frame: ConnectivityRelayTunnelReadyFrame("req-1", "attempt-1", "daemon", "computer-1", "client-fingerprint", "token-1"),
			want:  []string{`"computer_id":"computer-1"`, `"client_fingerprint":"client-fingerprint"`},
			old:   []string{"device_id", "android_fingerprint", "daemon_id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.frame)
			if err != nil {
				t.Fatalf("Marshal returned error: %v", err)
			}
			got := string(raw)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("%s JSON = %s, want %s", tt.name, got, want)
				}
			}
			for _, old := range tt.old {
				if strings.Contains(got, old) {
					t.Fatalf("%s JSON = %s, did not expect old key %q", tt.name, got, old)
				}
			}
		})
	}
}
