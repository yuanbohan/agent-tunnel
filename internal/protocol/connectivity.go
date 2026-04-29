package protocol

import "encoding/json"

const ConnectivityProtocolVersion = 1

type ConnectivityFrame struct {
	Type               string                       `json:"type"`
	ProtocolVersion    int                          `json:"protocol_version,omitempty"`
	RequestID          string                       `json:"request_id,omitempty"`
	Reason             string                       `json:"reason,omitempty"`
	RetryAfterSeconds  int                          `json:"retry_after_seconds,omitempty"`
	AccountID          string                       `json:"account_id,omitempty"`
	AttemptID          string                       `json:"attempt_id,omitempty"`
	Actor              string                       `json:"actor,omitempty"`
	DaemonID           string                       `json:"daemon_id,omitempty"`
	TunnelToken        string                       `json:"tunnel_token,omitempty"`
	Daemon             *ConnectivityDaemonInfo      `json:"daemon,omitempty"`
	Daemons            []ConnectivityDaemonInfo     `json:"daemons,omitempty"`
	TrustedDevices     []ConnectivityTrustedAndroid `json:"trusted_devices,omitempty"`
	AndroidFingerprint string                       `json:"android_fingerprint,omitempty"`
	PairingResponse    *ConnectivityPairingResponse `json:"pairing_response,omitempty"`
}

func (f ConnectivityFrame) MarshalJSON() ([]byte, error) {
	type connectivityFrame ConnectivityFrame
	raw, err := json.Marshal(connectivityFrame(f))
	if err != nil {
		return nil, err
	}
	if f.Type != "daemon_snapshot" {
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
	fields["daemons"] = daemons
	return json.Marshal(fields)
}

type ConnectivityDaemonInfo struct {
	DeviceID          string `json:"device_id"`
	DisplayName       string `json:"display_name,omitempty"`
	PlatformFamily    string `json:"platform_family,omitempty"`
	PlatformID        string `json:"platform_id,omitempty"`
	DaemonPublicKey   string `json:"daemon_public_key"`
	DaemonFingerprint string `json:"daemon_fingerprint"`
	TunnelVersion     string `json:"tunnel_version,omitempty"`
}

type ConnectivityTrustedAndroid struct {
	Fingerprint string `json:"fingerprint"`
	DisplayName string `json:"display_name,omitempty"`
}

type ConnectivityPairingResponse struct {
	AccountID          string `json:"account_id"`
	InvitationID       string `json:"invitation_id"`
	CorrelationID      string `json:"correlation_id"`
	AndroidPublicKey   string `json:"android_pubkey"`
	AndroidFingerprint string `json:"android_fingerprint"`
	AndroidDisplayName string `json:"android_display_name,omitempty"`
	Signature          string `json:"signature,omitempty"`
}

func ConnectivityAppRegisterFrame() ConnectivityFrame {
	return ConnectivityFrame{Type: "app_register", ProtocolVersion: ConnectivityProtocolVersion}
}

func ConnectivityDaemonRegisterFrame(info ConnectivityDaemonInfo, trusted []ConnectivityTrustedAndroid) ConnectivityFrame {
	return ConnectivityFrame{
		Type:            "daemon_register",
		ProtocolVersion: ConnectivityProtocolVersion,
		Daemon:          &info,
		TrustedDevices:  trusted,
	}
}

func ConnectivityDaemonSnapshotFrame(daemons []ConnectivityDaemonInfo) ConnectivityFrame {
	return ConnectivityFrame{Type: "daemon_snapshot", Daemons: daemons}
}

func ConnectivityDaemonUpsertFrame(info ConnectivityDaemonInfo) ConnectivityFrame {
	return ConnectivityFrame{Type: "paired_device_visible", Daemon: &info}
}

func ConnectivityDaemonRemovedFrame(deviceID, daemonFingerprint string) ConnectivityFrame {
	return ConnectivityFrame{
		Type: "paired_device_removed",
		Daemon: &ConnectivityDaemonInfo{
			DeviceID:          deviceID,
			DaemonFingerprint: daemonFingerprint,
		},
	}
}

func ConnectivityPairedDeviceRevokedFrame(deviceID, daemonFingerprint string) ConnectivityFrame {
	return ConnectivityFrame{
		Type: "paired_device_revoked",
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

func ConnectivityPairResponseAcceptedFrame(requestID string) ConnectivityFrame {
	return ConnectivityFrame{Type: "pair_response_accepted", RequestID: requestID}
}

func ConnectivityPairCompletedFrame(androidFingerprint string) ConnectivityFrame {
	return ConnectivityFrame{Type: "pair_completed", AndroidFingerprint: androidFingerprint}
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

func ConnectivityErrorFrame(requestID, reason string) ConnectivityFrame {
	return ConnectivityFrame{Type: "error", RequestID: requestID, Reason: reason}
}

func ConnectivityErrorFrameWithRetryAfter(requestID, reason string, retryAfterSeconds int) ConnectivityFrame {
	frame := ConnectivityErrorFrame(requestID, reason)
	frame.RetryAfterSeconds = retryAfterSeconds
	return frame
}
