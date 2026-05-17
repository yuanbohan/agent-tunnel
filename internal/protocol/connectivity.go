package protocol

import "encoding/json"

const ConnectivityProtocolVersion = 2

type ConnectivityFrame struct {
	Type                 string                       `json:"type"`
	ProtocolVersion      int                          `json:"protocol_version,omitempty"`
	RequestID            string                       `json:"request_id,omitempty"`
	Reason               string                       `json:"reason,omitempty"`
	RetryAfterSeconds    int                          `json:"retry_after_seconds,omitempty"`
	AccountID            string                       `json:"account_id,omitempty"`
	AttemptID            string                       `json:"attempt_id,omitempty"`
	Actor                string                       `json:"actor,omitempty"`
	DaemonID             string                       `json:"computer_id,omitempty"`
	TunnelToken          string                       `json:"tunnel_token,omitempty"`
	FallbackReason       string                       `json:"fallback_reason,omitempty"`
	DirectSetupLatencyMS int                          `json:"direct_setup_latency_ms,omitempty"`
	RelaySetupLatencyMS  int                          `json:"relay_setup_latency_ms,omitempty"`
	PublicUDPAddr        string                       `json:"public_udp_addr,omitempty"`
	PrivateUDPAddrs      []string                     `json:"private_udp_addrs,omitempty"`
	ExpiresAt            int64                        `json:"expires_at,omitempty"`
	Daemon               *ConnectivityDaemonInfo      `json:"computer,omitempty"`
	Daemons              []ConnectivityDaemonInfo     `json:"computers,omitempty"`
	TrustedDevices       []ConnectivityTrustedAndroid `json:"trusted_clients,omitempty"`
	DirectSessions       []ConnectivityDirectSession  `json:"direct_sessions,omitempty"`
	AndroidFingerprint   string                       `json:"client_fingerprint,omitempty"`
	PairingResponse      *ConnectivityPairingResponse `json:"pairing_response,omitempty"`
}

func ConnectivityRendezvousHintFrame(requestID, attemptID, actor, daemonID, androidFingerprint, publicUDPAddr string, privateUDPAddrs []string, expiresAt int64) ConnectivityFrame {
	return ConnectivityFrame{
		Type:               "rendezvous_hint",
		RequestID:          requestID,
		AttemptID:          attemptID,
		Actor:              actor,
		DaemonID:           daemonID,
		AndroidFingerprint: androidFingerprint,
		PublicUDPAddr:      publicUDPAddr,
		PrivateUDPAddrs:    privateUDPAddrs,
		ExpiresAt:          expiresAt,
	}
}

func ConnectivityRendezvousCloseFrame(requestID, attemptID, actor, daemonID, androidFingerprint string) ConnectivityFrame {
	return ConnectivityFrame{
		Type:               "rendezvous_close",
		RequestID:          requestID,
		AttemptID:          attemptID,
		Actor:              actor,
		DaemonID:           daemonID,
		AndroidFingerprint: androidFingerprint,
	}
}

func (f ConnectivityFrame) MarshalJSON() ([]byte, error) {
	type connectivityFrame ConnectivityFrame
	raw, err := json.Marshal(connectivityFrame(f))
	if err != nil {
		return nil, err
	}
	if f.Type != "computer_snapshot" {
		return raw, nil
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	daemons := f.Daemons
	if daemons == nil {
		daemons = []ConnectivityDaemonInfo{}
	}
	fields["computers"] = daemons
	return json.Marshal(fields)
}

func (f *ConnectivityFrame) UnmarshalJSON(data []byte) error {
	type connectivityFrame ConnectivityFrame
	var aux connectivityFrame
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*f = ConnectivityFrame(aux)
	return nil
}

type ConnectivityDaemonInfo struct {
	DeviceID          string `json:"computer_id"`
	DisplayName       string `json:"display_name,omitempty"`
	PlatformFamily    string `json:"platform_family,omitempty"`
	PlatformID        string `json:"platform_id,omitempty"`
	DaemonPublicKey   string `json:"computer_public_key"`
	DaemonFingerprint string `json:"computer_fingerprint"`
	TunnelVersion     string `json:"tunnel_version,omitempty"`
}

type ConnectivityTrustedAndroid struct {
	Fingerprint string `json:"fingerprint"`
	DisplayName string `json:"display_name,omitempty"`
}

type ConnectivityDirectSession struct {
	AttemptID          string `json:"attempt_id"`
	AndroidFingerprint string `json:"client_fingerprint"`
}

type ConnectivityPairingResponse struct {
	AccountID          string `json:"account_id"`
	InvitationID       string `json:"invitation_id"`
	CorrelationID      string `json:"correlation_id"`
	AndroidPublicKey   string `json:"client_public_key"`
	AndroidFingerprint string `json:"client_fingerprint"`
	AndroidDisplayName string `json:"client_display_name,omitempty"`
	Signature          string `json:"signature,omitempty"`
}

func ConnectivityAppRegisterFrame() ConnectivityFrame {
	return ConnectivityFrame{Type: "app_register", ProtocolVersion: ConnectivityProtocolVersion}
}

func ConnectivityDaemonRegisterFrame(info ConnectivityDaemonInfo, trusted []ConnectivityTrustedAndroid) ConnectivityFrame {
	return ConnectivityFrame{
		Type:            "computer_register",
		ProtocolVersion: ConnectivityProtocolVersion,
		Daemon:          &info,
		TrustedDevices:  trusted,
	}
}

func ConnectivityDaemonSnapshotFrame(daemons []ConnectivityDaemonInfo) ConnectivityFrame {
	return ConnectivityFrame{Type: "computer_snapshot", Daemons: daemons}
}

func ConnectivityDaemonUpsertFrame(info ConnectivityDaemonInfo) ConnectivityFrame {
	return ConnectivityFrame{Type: "computer_visible", Daemon: &info}
}

func ConnectivityDaemonRemovedFrame(deviceID, daemonFingerprint string) ConnectivityFrame {
	return ConnectivityFrame{
		Type: "computer_removed",
		Daemon: &ConnectivityDaemonInfo{
			DeviceID:          deviceID,
			DaemonFingerprint: daemonFingerprint,
		},
	}
}

func ConnectivityPairedDeviceRevokedFrame(deviceID, daemonFingerprint string) ConnectivityFrame {
	return ConnectivityFrame{
		Type: "client_revoked",
		Daemon: &ConnectivityDaemonInfo{
			DeviceID:          deviceID,
			DaemonFingerprint: daemonFingerprint,
		},
	}
}

func ConnectivityPairResponseForwardFrame(response ConnectivityPairingResponse) ConnectivityFrame {
	return ConnectivityFrame{Type: "pair_response_forward", PairingResponse: &response}
}

func ConnectivityPairInvitationReservedFrame(requestID, accountID string) ConnectivityFrame {
	return ConnectivityFrame{Type: "pair_invitation_reserved", RequestID: requestID, AccountID: accountID}
}

func ConnectivityPairCompletedFrame(androidFingerprint string) ConnectivityFrame {
	return ConnectivityFrame{Type: "pair_completed", AndroidFingerprint: androidFingerprint}
}

func ConnectivityDirectSessionOpenFrame(requestID, attemptID, daemonID, androidFingerprint string) ConnectivityFrame {
	return ConnectivityFrame{
		Type:               "direct_session_open",
		RequestID:          requestID,
		AttemptID:          attemptID,
		DaemonID:           daemonID,
		AndroidFingerprint: androidFingerprint,
	}
}

func ConnectivityDirectSessionCloseFrame(requestID, attemptID, daemonID, androidFingerprint string) ConnectivityFrame {
	return ConnectivityFrame{
		Type:               "direct_session_close",
		RequestID:          requestID,
		AttemptID:          attemptID,
		DaemonID:           daemonID,
		AndroidFingerprint: androidFingerprint,
	}
}

func ConnectivityRelayTunnelReadyFrame(requestID, attemptID, actor, daemonID, androidFingerprint, token string) ConnectivityFrame {
	return ConnectivityFrame{
		Type:               "relay_tunnel_ready",
		RequestID:          requestID,
		AttemptID:          attemptID,
		Actor:              actor,
		DaemonID:           daemonID,
		AndroidFingerprint: androidFingerprint,
		TunnelToken:        token,
	}
}

func ConnectivityRelayTunnelReadyFrameWithDiagnostics(requestID, attemptID, actor, daemonID, androidFingerprint, token, fallbackReason string, directSetupLatencyMS, relaySetupLatencyMS int) ConnectivityFrame {
	frame := ConnectivityRelayTunnelReadyFrame(requestID, attemptID, actor, daemonID, androidFingerprint, token)
	frame.FallbackReason = fallbackReason
	frame.DirectSetupLatencyMS = directSetupLatencyMS
	frame.RelaySetupLatencyMS = relaySetupLatencyMS
	return frame
}

func ConnectivityErrorFrame(requestID, reason string) ConnectivityFrame {
	return ConnectivityFrame{Type: "error", RequestID: requestID, Reason: reason}
}

func ConnectivityErrorFrameWithRetryAfter(requestID, reason string, retryAfterSeconds int) ConnectivityFrame {
	frame := ConnectivityErrorFrame(requestID, reason)
	frame.RetryAfterSeconds = retryAfterSeconds
	return frame
}
