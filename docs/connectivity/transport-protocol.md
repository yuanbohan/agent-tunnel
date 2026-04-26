# QUIC Transport Protocol Direction

## Status

This document captures the target daemon-to-Android transport for session connectivity. It replaces the old WebRTC/DataChannel direction with a simpler QUIC-based transport.

## Transport Responsibilities

The end-to-end transport carries:

- session metadata
- preview text
- interactive attach control
- terminal snapshots
- live terminal bytes
- input and resize messages

It does not rely on Relay to interpret any of those messages.

## Security Model

The transport uses:

- QUIC
- TLS 1.3
- device-key pinning

The connection is valid only if the remote peer proves possession of the device identity that was pinned during pairing.

### Concrete Cert Pinning Mechanism

The phase-1 cert pinning mechanism is fixed so that daemon and Android implementations cannot drift apart.

Each side at startup constructs one self-signed X.509 certificate whose `SubjectPublicKeyInfo` is the Ed25519 device public key established by pairing. The TLS algorithm for both signature and SPKI is `Ed25519` (RFC 8032 / RFC 8446 §4.2.3).

During the QUIC/TLS 1.3 handshake:

- both peers present their self-signed Ed25519 certificate
- both peers require client certificates (`tls.Config.ClientAuth = tls.RequireAnyClientCert` semantics on the server side)
- standard PKIX chain validation is bypassed (`InsecureSkipVerify = true` in Go terms; equivalent custom verifier in quiche)
- a custom `VerifyPeerCertificate`-style callback parses the leaf certificate and compares its SPKI bytes byte-for-byte against the pinned Ed25519 public key persisted from pairing
- certificate `NotBefore` and `NotAfter` are intentionally ignored; trust is rooted in the public key, not certificate metadata
- if the parsed SPKI does not match, the connection is closed with a defined error

This produces the security properties claimed in `docs/connectivity/architecture.md`:

- the peer is authenticated as the pre-paired device, not as the holder of any CA-issued certificate
- TLS 1.3 ECDHE (typically `X25519`) derives fresh symmetric session keys for this connection only
- compromising one side's long-lived Ed25519 key does not retroactively decrypt past sessions

### ALPN

The QUIC connection MUST be negotiated with the ALPN identifier:

- `tunnel-conn/1`

Connections that do not negotiate this ALPN value MUST be rejected. This prevents accidental interop with HTTP/3 or other QUIC services on shared infrastructure.

### QUIC Transport Parameters

Phase-1 recommended transport parameters:

| Parameter | Value |
|---|---|
| ALPN | `tunnel-conn/1` |
| `max_idle_timeout` | `30s` |
| keep-alive interval | `15s` |
| `max_incoming_streams` (server) | `64` |
| `max_data` (connection) | `16 MB` |
| `max_stream_data` (per stream) | `1 MB` |
| 0-RTT | **DISABLED** |

These are conservative defaults. They may be tuned later, but daemon and Android implementations MUST advertise consistent values until a new phase explicitly raises them.

### 0-RTT Is Disabled

QUIC 0-RTT allows clients to send application data before the handshake completes. The standard tradeoff is that 0-RTT data is replayable.

In this product, replay would be catastrophic: a captured `input_text` frame could re-execute a destructive shell command. Phase 1 therefore disables 0-RTT on both server (daemon) and client (Android). Implementations MUST NOT advertise or accept 0-RTT, even at the cost of one extra round-trip on connection setup.

### Key Layers

Two different key layers are involved:

- pairing identity keys
- transport session keys

Pairing identity keys are long-lived and identify the daemon and Android installation.

Transport session keys are fresh per connection and come from the TLS 1.3 handshake.

The architecture must not blur those together.

### Conceptual Algorithm Split

The current preferred conceptual split is:

- long-lived device identity and pairing signatures: `Ed25519`
- ephemeral TLS key exchange: `X25519` or another TLS 1.3 ECDHE group supported by the transport libraries
- traffic key derivation: `HKDF`
- packet protection: TLS 1.3 AEAD such as `AES-GCM` or `ChaCha20-Poly1305`

The exact transport implementation may negotiate different TLS 1.3 details, but it must preserve the same model:

- pinned peer identity for authentication
- fresh symmetric keys for each connection

This design intentionally does not add:

- WebRTC
- DTLS/SCTP/DataChannels
- TURN
- a second application-layer encryption layer in phase 1

## Path Modes

### Direct

- QUIC over UDP
- peers discover candidate addresses through STUN and Relay rendezvous hints
- direct is preferred when it succeeds quickly

### Relay Fallback

- QUIC packets tunneled through Relay over WebSocket-over-HTTPS
- used when direct setup fails or times out
- Relay forwards opaque encrypted packets only

The app should treat `direct` and `relay` as path badges over the same higher-level protocol.

### Direct Attempt Deadline

Phase 1 chooses a sequential model rather than a happy-eyeballs race:

- the direct attempt has `3s` to complete the QUIC/TLS handshake (the **direct attempt deadline**)
- if the deadline expires, the connection manager opens the fallback relay tunnel and starts a fresh QUIC handshake over it
- the loser of the race (in case direct unexpectedly completes during fallback setup) is canceled and its transport state is discarded

Sequential keeps the state machine and the transport-state badge unambiguous. The deadline value is configurable but its default is fixed at `3s` so that field measurements from different deployments remain comparable.

### Reconnect Backoff

After any transport drop, the connection manager reconnects with exponential backoff:

- base delay: `1s`
- cap: `60s`
- full jitter (uniform random in `[0, current]`)
- reset to base on a successful new connection

Each daemon connection manager backs off independently. Multiple daemons going offline at the same time MUST NOT synchronize their retries.

## Carrier Model

The clean mental model is:

- QUIC/TLS logic stays the same
- only the underlying packet carrier changes

Direct carrier:

- UDP socket directly addressing the remote peer

Fallback carrier:

- Relay WebSocket tunnel that forwards encrypted QUIC packets

The session protocol above QUIC should therefore remain path-agnostic.

## NAT Traversal Model

Direct connection attempts use a minimal NAT traversal stack:

1. each peer learns its observed public UDP address via STUN
2. Relay carries rendezvous hints between Android and daemon
3. both peers send UDP traffic toward the candidate addresses at roughly the same time
4. if NAT state permits it, a direct path opens

STUN is used only for candidate discovery. It does not solve the full transport problem by itself.

The product should self-host this STUN capability as part of its own edge infrastructure instead of relying on public third-party STUN services.

### STUN Retry Policy

STUN Binding Requests are unreliable UDP datagrams. Implementations MUST retry on timeout:

- attempt 1: 500ms timeout
- attempt 2: 1s timeout
- attempt 3: 2s timeout
- after 3 failed attempts: treat the peer's public address as undiscoverable for this attempt

If STUN cannot return a public address within this budget (typically because UDP/3478 is blocked outbound), the connection manager MUST fall back to the relay tunnel directly without trying direct UDP. There is no point attempting hole punching when at least one side cannot learn its own mapping.

### NAT Traversal Limitations

The minimal STUN-based scheme works for cone NATs:

- full-cone, restricted-cone, port-restricted-cone

It does NOT traverse symmetric NATs. Symmetric NATs assign different external port mappings per destination, so a STUN-discovered mapping is not the mapping that will be seen by the peer. ICE-style candidate-pair lifecycle, port prediction, and similar techniques would be required to handle symmetric NATs and are intentionally out of scope for phase 1.

When the direct attempt deadline expires under a symmetric NAT, the connection naturally falls back to the relay tunnel. This is expected behavior, not a defect; symmetric NATs are common in carrier-grade NAT environments and on some corporate networks.

## Reconnect Rule

Phase 1 keeps path switching simple:

- direct attempt creates a new QUIC connection
- fallback attempt creates a new QUIC connection
- later retries also create a new QUIC connection

The app and daemon should recover state by resynchronizing session metadata and, if needed, requesting a fresh interactive snapshot.

The design does not require live transport migration between direct and relay within one QUIC connection.

### Why Reconnect Instead Of Migrating

Phase 1 chooses simplicity:

- direct failed -> open relay carrier -> establish a new QUIC connection
- relay failed -> retry again with fresh direct and relay attempts

This keeps the state machine small and works well with the existing session model because daemon state can always be re-sent as:

- fresh `session_index`
- fresh preview snapshots
- fresh interactive snapshot for any still-attached session

## Stream Model

Phase 1 uses one logical control stream plus zero or more interactive streams per daemon connection:

- one long-lived `control` stream — bidirectional, opened by Android immediately after the QUIC connection is ready
- one short-lived `interactive` stream per attached interactive session — server-initiated unidirectional stream (daemon→Android)

### Stream Roles And Direction

- The control stream is bidirectional. Android sends interactive requests, input, and resize on it. The daemon sends session metadata, preview snapshots, interactive grant/deny replies, and path-state advisories on it.
- Each interactive stream is unidirectional, daemon-to-Android. The daemon opens it after granting an interactive request and writes only `snapshot_*` and `live_bytes` frames on it. Android never writes to an interactive stream.
- The QUIC stream id of an interactive stream IS the `interactive_stream_id` advertised in the corresponding `interactive_granted` frame on the control stream. No protocol-level identifier separate from the QUIC stream id is used.

### Why Two Streams

- large snapshot chunks should not block metadata and preview updates
- QUIC stream ordering is enough; no `stream_epoch` protocol is required
- multiple interactive sessions still fit naturally because each session gets its own interactive stream
- one-way interactive streams keep the daemon as the unambiguous writer for terminal byte content; input flows back on the control stream

## Control Stream

The control stream is a typed frame stream:

- `[1-byte type] [varint payload_length] [payload bytes]`

### Control Stream Frame Ordering

The control stream MUST follow this fixed ordering after the QUIC connection becomes ready:

1. `hello` is the first frame on the control stream from each side
2. the daemon sends `session_index` immediately after exchanging `hello`
3. only after `session_index` may the daemon emit `session_upsert`, `session_gone`, `preview_snapshot`, `interactive_granted`, `interactive_denied`, or `path_state`

The daemon implementation MUST serialize these emissions on the control stream so that QUIC's per-stream FIFO ordering is preserved. Specifically: the daemon MUST NOT emit `session_upsert` on the control stream concurrently from a different goroutine before `session_index` has been written. A single writer goroutine per control stream is the recommended implementation pattern.

Android MUST NOT process `session_upsert`, `session_gone`, or `preview_snapshot` until `session_index` has been received and applied.

Phase-1 frame families:

- `hello`
- `session_index`
- `session_upsert`
- `session_gone`
- `session_activate`
- `session_release`
- `preview_snapshot`
- `interactive_request`
- `interactive_granted`
- `interactive_denied`
- `interactive_release`
- `input_text`
- `input_key`
- `resize`
- `path_state`
- `error`

### `hello`

Sent immediately after the QUIC connection is ready.

Purpose:

- negotiate protocol version
- identify actor type
- surface current path mode

Recommended fields:

- `protocol_version`
- `actor_type`
- `device_fingerprint`
- `path_kind`

### `session_index`

Sent by the daemon after `hello`.

Purpose:

- bootstrap the current session list for that daemon
- replace `GET /api/sessions` in the target design

The session payload contract is defined in `docs/connectivity/daemon-session-sync.md`.

### `session_upsert` / `session_gone`

Sent by the daemon whenever session metadata changes or a session disappears.

### `session_activate`

Sent by Android when it wants to activate one session for real content delivery.

Recommended fields:

- `session_id`
- `access_token`

`access_token` is issued by Relay and proves that this Android device is allowed to activate the addressed session under the account's current active-session selection state.

The daemon validates:

- token signature
- token expiry
- token device binding
- token daemon binding
- token session binding
- token selection-epoch binding
- token `jti` not currently marked revoked by a prior daemon-side `access_token_revoked` event from Relay

If validation succeeds, the daemon may begin sending real preview for that session and may later honor `interactive_request` for it.

### `session_release`

Sent by Android when it intentionally stops using the currently active session on this daemon connection.

Release informs the daemon to stop sending real preview and to end any remaining interactive state for that session on this connection immediately. Relay remains the source of truth for account-global selection state; the Android side is still expected to release or replace the corresponding Relay selection through the control plane, after which Relay fans out `access_token_revoked` so that the token's `jti` cannot be reused before expiry.

### `preview_snapshot`

Sent by the daemon whenever a preview changes for a session that is currently selected for the account and has been activated on this device with a valid access token.

Preview is:

- daemon-generated
- pure text
- lightweight
- not terminal emulation

Preview rules are defined in `docs/connectivity/daemon-preview-generation-rules.md`.

### `interactive_request`

Sent by Android when the user enters a session detail view for a session that is already active on this device.

Recommended fields:

- `session_id`
- `cols`
- `rows`

### `interactive_granted`

Sent by the daemon when it grants interactive ownership.

Recommended fields:

- `session_id`
- `interactive_stream_id`
- `cols`
- `rows`

The `interactive_stream_id` is the QUIC stream id of the daemon-initiated unidirectional stream that will carry snapshot and live bytes for this attach lifetime. Android binds the session to that stream id when the stream is observed. No additional epoch or generation field is required because each interactive stream id is unique within the QUIC connection and the connection itself is replaced on every reconnect.

### `interactive_denied`

Sent by the daemon when the request is rejected.

Recommended fields:

- `session_id`
- `reason`

Recommended `reason` enum values:

- `selection_required` — the session is not currently selected for the account and activated on this device
- `not_authorized` — the requesting connection does not have rights to attach this session
- `session_unavailable` — the session no longer exists or is not in an attachable state
- `daemon_busy` — temporary daemon-side rejection, retry allowed
- `unknown` — fallback when no specific reason applies

Android MUST be tolerant of unknown `reason` values and treat them as `unknown`.

### `interactive_release`

Sent by Android when leaving the interactive view or when the app intentionally releases ownership.

### `input_text`, `input_key`, `resize`

Sent by Android for sessions that currently hold interactive ownership on this daemon connection.

Recommended fields:

- `session_id` (mandatory)
- input or resize payload

The `session_id` field is mandatory on every input and resize frame. The daemon MUST drop input or resize frames whose `session_id` does not currently hold an active `interactive_granted` session on this connection. This protects against UI focus-confusion bugs on the Android side, where the wrong session could otherwise receive input. The daemon SHOULD log such drops at debug level but MUST NOT escalate them to a connection-level error.

### `path_state`

Optional daemon-to-app advisory frame used to confirm the current path:

- `direct`
- `relay`

The authoritative source of the path badge is the Android connection manager, which knows whether it opened a direct UDP socket or a fallback relay tunnel for this QUIC connection. The `path_state` frame from the daemon is for cross-validation and post-reconnect re-display only. Android SHOULD treat its own carrier-side knowledge as primary and the daemon advisory as secondary.

### `error`

Sent by either side to signal a recoverable protocol-level error before closing the connection or stream.

Recommended fields:

- `code`
- `message`

Receivers MUST be tolerant of unknown `code` values.

## Protocol Versioning

`hello` carries `protocol_version` as a single integer. Phase 1 is `protocol_version = 1`.

Negotiation rules:

- if the peer's `protocol_version` is the same, proceed normally
- if it differs, the connection is closed with an `error` frame whose `code` is `protocol_version_mismatch`
- there is no in-band negotiation or downgrade in phase 1

Forward compatibility within one major version:

- both sides MUST silently ignore unknown frame `type` bytes on the control stream
- both sides MUST silently ignore unknown fields inside known frames
- breaking wire-format changes (renaming a field, changing a frame's binary layout, repurposing a `type` byte) MUST bump `protocol_version`
- additive changes (new optional fields, new frame types in unused `type` byte ranges) MUST NOT bump `protocol_version`

## Transport State

Both Android and daemon should maintain transport state per daemon connection.

Recommended state families:

- `offline`
- `connecting_direct`
- `connecting_relay`
- `connected_direct`
- `connected_relay`
- `reconnecting`

They should also maintain per-session interactive state separately:

- whether interactive is requested
- whether it was granted
- which interactive stream carries that session

They should not model path state per session. All sessions on one daemon connection share the same current transport path.

## Interactive Stream

When a session becomes interactive, the daemon opens or accepts one dedicated interactive stream for that session attach lifetime.

The interactive stream carries only these frame families:

- `snapshot_begin`
- `snapshot_chunk`
- `snapshot_end`
- `live_bytes`

### `snapshot_begin`

Recommended fields:

- `session_id`
- `cols`
- `rows`

### `snapshot_chunk`

Carries raw PTY snapshot bytes.

### `snapshot_end`

Marks the end of the initial full snapshot.

### `live_bytes`

Carries raw PTY output bytes after snapshot completion.

## Interactive Recovery

After reconnect:

- Android replays `interactive_request` for each session it wants to keep interactive
- the daemon responds with fresh `interactive_granted` results per session
- the daemon starts a new interactive stream for each granted session
- the daemon sends a fresh snapshot, then live bytes, on each granted interactive stream

The protocol does not attempt missed-byte replay.

## Data Flow Summary

Once the QUIC/TLS connection is established:

- the daemon sends `session_index` on the control stream
- the daemon sends `preview_snapshot` on the control stream
- Android sends `interactive_request` on the control stream
- the daemon responds with `interactive_granted` or `interactive_denied`
- if granted, the daemon sends terminal bytes on that session's interactive stream

So:

- session list is daemon-owned
- preview is daemon-generated and control-stream-delivered
- interactive terminal data is daemon-streamed over per-session interactive streams

## Session Authority

The daemon is authoritative for:

- which sessions exist
- what metadata they expose
- which sessions are currently interactive
- whether an interactive request is granted per session

Relay does not participate in those decisions in the target design.

## Android Library Constraint

This protocol assumes Android can open a custom QUIC connection with arbitrary streams. That means the implementation should use a transport library appropriate for a custom QUIC protocol, not an HTTP-only API surface.

## References

- `docs/connectivity/architecture.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/daemon-session-sync.md`
- `docs/connectivity/daemon-preview-generation-rules.md`
- `docs/connectivity/mobile-reference.md`
- `docs/connectivity/sequence-flows.md`
- STUN: `https://datatracker.ietf.org/doc/html/rfc5389`
- NAT traversal background: `https://datatracker.ietf.org/doc/html/rfc5128`
- QUIC transport: `https://www.ietf.org/rfc/rfc9000.html`
- QUIC + TLS integration: `https://datatracker.ietf.org/doc/html/rfc9001`
- TLS 1.3: `https://datatracker.ietf.org/doc/html/rfc8446`
