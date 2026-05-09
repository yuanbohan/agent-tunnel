package sessionproto

const (
	ActorMobile = "mobile"
	ActorDaemon = "daemon"

	PathDirect = "direct"
	PathRelay  = "relay"

	ProtocolVersion = 2
)

type Hello struct {
	ProtocolVersion   int    `json:"protocol_version"`
	ActorType         string `json:"actor_type"`
	ClientFingerprint string `json:"client_fingerprint"`
	PathKind          string `json:"path_kind"`
}

type SessionMetadata struct {
	SessionID      string `json:"session_id"`
	Label          string `json:"label"`
	CommandPreview string `json:"command_preview"`
	CWD            string `json:"cwd"`
	GitBranch      string `json:"git_branch"`
	StartedAt      int    `json:"started_at"`
	UpdatedAt      int    `json:"updated_at"`
	Online         bool   `json:"online"`
}

type SessionIndex struct {
	Sessions []SessionMetadata `json:"sessions"`
}

type SessionUpsert struct {
	Session SessionMetadata `json:"session"`
}

type SessionGone struct {
	SessionID string `json:"session_id"`
}

type PreviewSubscribe struct {
	SessionID string `json:"session_id"`
}

type PreviewUnsubscribe struct {
	SessionID string `json:"session_id"`
}

type PreviewSnapshot struct {
	SessionID string `json:"session_id"`
	Preview   string `json:"preview"`
	UpdatedAt int    `json:"updated_at,omitempty"`
}

type InteractiveRequest struct {
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

type InteractiveGranted struct {
	SessionID           string `json:"session_id"`
	InteractiveStreamID int64  `json:"interactive_stream_id"`
	Cols                int    `json:"cols"`
	Rows                int    `json:"rows"`
}

type InteractiveDenied struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
}

type InteractiveRelease struct {
	SessionID string `json:"session_id"`
}

type InputText struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	Submit    bool   `json:"submit,omitempty"`
}

type InputKey struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key"`
}

type Resize struct {
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

type PathState struct {
	AttemptID            string `json:"attempt_id,omitempty"`
	PathKind             string `json:"path_kind"`
	FallbackReason       string `json:"fallback_reason,omitempty"`
	DirectSetupLatencyMS int    `json:"direct_setup_latency_ms,omitempty"`
	RelaySetupLatencyMS  int    `json:"relay_setup_latency_ms,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

type SnapshotBegin struct {
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

type SnapshotEnd struct {
	SessionID  string `json:"session_id,omitempty"`
	ChunkCount int    `json:"chunk_count,omitempty"`
}
