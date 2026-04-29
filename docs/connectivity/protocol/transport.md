# QUIC Transport Protocol Direction

## Status

This document captures the target daemon-to-Android transport for session connectivity. It replaces the old WebRTC/DataChannel direction with a simpler QUIC-based transport.

## Transport Responsibilities

The end-to-end transport carries:

- session metadata
- preview subscribe / unsubscribe control
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

Each side at startup constructs one self-signed X.509 certificate whose `SubjectPublicKeyInfo` is the Ed25519 device public key established by pairing.

During the QUIC/TLS 1.3 handshake:

- both peers present their self-signed Ed25519 certificate
- both peers require client certificates
- standard PKIX chain validation is bypassed
- a custom peer-verification callback compares the peer certificate SPKI bytes byte-for-byte against the pinned Ed25519 public key persisted from pairing
- certificate validity dates are ignored; trust is rooted in the pinned public key, not certificate metadata

This preserves the intended model:

- pairing establishes who the peer is
- TLS 1.3 establishes fresh symmetric keys for this connection
- terminal payload is encrypted only with the per-connection session keys

### ALPN

The QUIC connection MUST negotiate:

- `tunnel-conn/1`

Connections that do not negotiate this ALPN value MUST be rejected.

### 0-RTT Is Disabled

Phase 1 disables QUIC 0-RTT on both sides.

Replayable early data is not acceptable for terminal input.

### Key Layers

Two different key layers are involved:

- pairing identity keys
- transport session keys

Pairing identity keys are long-lived and identify the daemon and Android installation.

Transport session keys are fresh per connection and come from the TLS 1.3 handshake.

### Conceptual Algorithm Split

The current preferred conceptual split is:

- long-lived device identity and pairing signatures: `Ed25519`
- ephemeral TLS key exchange: `X25519` or another TLS 1.3 ECDHE group supported by the transport libraries
- traffic key derivation: `HKDF`
- packet protection: TLS 1.3 AEAD such as `AES-GCM` or `ChaCha20-Poly1305`

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

Phase 1 uses a sequential direct-first model rather than a happy-eyeballs race:

- the direct attempt gets a short handshake deadline
- if that deadline expires, the connection manager opens the fallback relay tunnel and starts a fresh QUIC handshake over it

The exact timeout is an implementation default, not a wire-level contract.

### Reconnect Backoff

After any transport drop, the connection manager reconnects with exponential backoff and jitter.

Exact timing values are implementation defaults, not protocol contract. Multiple daemon connection managers MUST NOT synchronize their retries.

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

STUN Binding Requests are unreliable UDP datagrams. Implementations should retry on timeout a small number of times before giving up and falling back.

If STUN cannot return a public address within that budget, the connection manager should skip direct UDP and move to relay fallback.

### NAT Traversal Limitations

The minimal STUN-based scheme works for cone NATs.

It does not traverse symmetric NATs. When the direct attempt deadline expires under a symmetric NAT, the connection naturally falls back to the relay tunnel.

## Reconnect Rule

Phase 1 keeps path switching simple:

- direct attempt creates a new QUIC connection
- fallback attempt creates a new QUIC connection
- later retries also create a new QUIC connection

The app and daemon recover state by resynchronizing session metadata and, if needed, requesting a fresh interactive snapshot.

## Stream Model

Phase 1 uses one logical control stream plus zero or more interactive streams per daemon connection (`../contract.md` D5):

- one long-lived `control` stream — **bidirectional**, opened by Android immediately after the QUIC connection is ready. Carries every typed control frame including `input_text`, `input_key`, and `resize`.
- one short-lived `interactive` stream per attached interactive session — **daemon-initiated unidirectional (UNI)**, daemon → Android only. Carries `snapshot_begin`, `snapshot_chunk`, `snapshot_end`, and `live_bytes`. Input never travels on an interactive stream.

### Why Two Streams (And This Direction Split)

- large snapshot chunks must not block metadata and preview updates — each interactive stream gets its own QUIC flow window
- a slow Android consumer on session A cannot stall daemon writes for session B or for the control stream
- QUIC stream ordering is enough; no `stream_epoch` protocol is required
- single-writer-per-side on the control stream simplifies daemon implementation
- input handling stays centralized on one stream rather than fragmenting across multiple bidirectional streams

Phase-1 simplification:

- multiple interactive streams may coexist across different sessions on the same daemon connection
- the same session does not support multiple concurrent interactive attach lifetimes in phase 1

## Control Stream

The control stream is a typed frame stream.

### Frame Wire Format

```
[1-byte type] [varint payload_length] [payload bytes]
```

### Frame Type Registry

Step 1 pins the initial protocol/data frame type bytes for the Go
mobile-simulator harness. Android should mirror these values unless a later
compatibility-line change updates the registry.

| Type | Name | Payload |
|---|---|---|
| `0x01` | `hello` | JSON |
| `0x02` | `session_index` | JSON |
| `0x03` | `preview_subscribe` | JSON |
| `0x04` | `session_upsert` | JSON |
| `0x05` | `session_gone` | JSON |
| `0x06` | `preview_unsubscribe` | JSON |
| `0x07` | `preview_snapshot` | JSON |
| `0x08` | `interactive_request` | JSON |
| `0x09` | `interactive_granted` | JSON |
| `0x0a` | `interactive_denied` | JSON |
| `0x0b` | `interactive_release` | JSON |
| `0x0c` | `input_text` | JSON |
| `0x0d` | `input_key` | JSON |
| `0x0e` | `resize` | JSON |
| `0x0f` | `path_state` | JSON |
| `0x10` | `snapshot_begin` | JSON |
| `0x11` | `snapshot_chunk` | raw bytes |
| `0x12` | `live_bytes` | raw bytes |
| `0x13` | `snapshot_end` | JSON |
| `0x7f` | `error` | JSON |

Frame families not listed here remain unassigned and should be pinned when their
implementation lands.

### Payload Encoding (`../contract.md` D6)

- typed control frames use **JSON** for `payload bytes`
- `varint payload_length` is the byte length of the JSON payload (UTF-8)
- receivers MUST ignore unknown JSON fields
- receivers MUST tolerate unknown frame `type` values (silently drop, log at info level)
- protocol-breaking changes bump `protocol_version` in `hello`

JSON was chosen over CBOR / protobuf for phase 1 to keep tcpdump output and customer-support log dumps human-readable. Phase-2 may introduce a CBOR profile under `protocol_version = 2` if Android JSON-parse cost becomes load-bearing (see `../contract.md` open TODOs).

### Control Stream Frame Ordering

The control stream MUST follow this fixed ordering after the QUIC connection becomes ready:

1. `hello` is the first frame on the control stream from each side
2. the daemon sends `session_index` immediately after exchanging `hello`
3. only after `session_index` may the daemon emit `session_upsert`, `session_gone`, `preview_snapshot`, `interactive_granted`, `interactive_denied`, or `path_state`

Android MUST NOT process daemon session or preview frames until `session_index` has been received and applied.

Phase-1 frame families:

- `hello`
- `session_index`
- `session_upsert`
- `session_gone`
- `preview_subscribe`
- `preview_unsubscribe`
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

The initial `session_index` MUST contain the full current session set known to the daemon's local broker.

### `session_upsert` / `session_gone`

Sent by the daemon whenever session metadata changes or a session disappears.

Each `session_upsert` carries the full current metadata object for that session — phase 1 prefers full replacement payloads over patch-style deltas.

### Session Metadata Contract

This is the canonical session payload shape used by `session_index`, `session_upsert`, and (where applicable) `session_gone`. In the target design, Relay does not expose sessions; the daemon is the source of truth.

Phase-1 daemon-to-Android session metadata fields:

- `session_id`
- `label`
- `command_preview`
- `cwd`
- `git_branch`
- `started_at`
- `updated_at`
- `online`

Preview text is **not** a session metadata field. It is delivered separately through `preview_snapshot` so the app can update list rows and preview content independently.

#### Sanitization

The daemon broker treats the following as display metadata and SHOULD apply lightweight cleanup before forwarding:

- `label` — bound length to a sane render width
- `command_preview` — prefer a redacted preview to dumping raw `argv`
- `cwd` — normalize for display, expand `$HOME` when local
- `git_branch` — bound length

Daemon does not need to encrypt or hash these fields, but it should not be careless either; they may appear in lock-screen notifications or list views.

#### Subscription Boundary

The daemon is subscription-unaware. It exposes the full session roster to every paired device that holds a valid QUIC connection. The app decides what to render as locked vs. unlocked under its free/pro rule (`ux/subscription.md`).

### `preview_subscribe`

Sent by Android when it wants live preview updates for one session.

Recommended fields:

- `session_id`

### `preview_unsubscribe`

Sent by Android when it no longer wants live preview updates for one session.

Recommended fields:

- `session_id`

### `preview_snapshot`

Sent by the daemon whenever a preview changes for a session Android has subscribed to.

Preview is:

- daemon-sent
- sourced from the owning local `tunnel run`
- pure text
- lightweight
- not terminal emulation

Preview generation rules (length budget, ANSI stripping, throttling) live in `protocol/local-broker.md` § Preview Pipeline. Daemon caches the latest preview pushed by each `tunnel run` and fans it out only to subscribed paired devices.

### `interactive_request`

Sent by Android when the user enters a session detail view for a session it wants to use interactively.

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

The `interactive_stream_id` is the QUIC stream id of the daemon-initiated stream that will carry snapshot and live bytes for this attach lifetime.

In the current Step 4 daemon implementation, a grant opens this stream and
sends `snapshot_begin` followed by `snapshot_end`. Full snapshot chunks and live
PTY bytes are reserved by this contract and are still pending the local broker
bridge work.

### `interactive_denied`

Sent by the daemon when the request is rejected.

Recommended fields:

- `session_id`
- `reason`

Recommended `reason` enum values:

- `device_not_trusted`
- `session_unavailable`
- `daemon_busy`
- `unknown`

Android MUST be tolerant of unknown `reason` values.

### `interactive_release`

Sent by Android when leaving the interactive view or when the app intentionally releases ownership.

### `input_text`, `input_key`, `resize`

Sent by Android for sessions that currently hold interactive ownership on this daemon connection.

Recommended fields:

- `session_id`
- `input_text`: `text`, optional `submit`
- `input_key`: `key`
- `resize`: `cols`, `rows`

The daemon MUST drop input or resize frames whose `session_id` does not currently hold an active `interactive_granted` session on this connection.

### `path_state`

Optional daemon-to-app advisory frame used to confirm the current path:

- `direct`
- `relay`

The authoritative source of the path badge is the Android connection manager.

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
- if it differs, the connection is closed with `protocol_version_mismatch`
- there is no in-band negotiation or downgrade in phase 1

## Transport State

Both Android and daemon should maintain transport state per daemon connection.

Recommended state families:

- `offline`
- `connecting_direct`
- `connecting_relay`
- `connected_direct`
- `connected_relay`
- `reconnecting`

They should also maintain per-session interactive state separately.

## Interactive Stream

When a session becomes interactive, the daemon opens one dedicated server-initiated unidirectional QUIC stream for that session attach lifetime. The QUIC stream id is announced to Android in `interactive_granted.interactive_stream_id`.

The interactive stream carries only:

- `snapshot_begin`
- `snapshot_chunk`
- `snapshot_end`
- `live_bytes`

Frame wire format on the interactive stream:

```
[1-byte type] [varint payload_length] [payload bytes]
```

Payload encoding:

- `snapshot_begin` and `snapshot_end` use JSON (small typed payloads)
- `snapshot_chunk` and `live_bytes` carry **raw PTY bytes only**, not JSON. The outer length-framed envelope is sufficient.

This split keeps PTY byte throughput cheap while still letting the start / end markers carry structured fields like `cols` / `rows`.

An implementation that has no snapshot bytes ready yet may send
`snapshot_begin` followed immediately by `snapshot_end`, but it must not report
`interactive_granted` unless the announced daemon-initiated stream exists.

### `snapshot_begin`

JSON fields:

- `session_id`
- `cols`
- `rows`

### `snapshot_chunk`

Raw PTY snapshot bytes. No JSON wrap.

### `snapshot_end`

JSON; marks the end of the initial full snapshot. Optional fields may be added later for sanity check (e.g. total chunk count).

### `live_bytes`

Raw PTY output bytes after snapshot completion. No JSON wrap.

## Interactive Recovery

After reconnect:

- Android replays `preview_subscribe` for the sessions it still wants live preview for
- Android replays `interactive_request` for the sessions it still wants interactive
- the daemon responds with fresh `interactive_granted` results per session
- the daemon starts a new interactive stream for each granted session
- the daemon sends a fresh snapshot, then live bytes, on each granted interactive stream

The protocol does not attempt missed-byte replay.

## Data Flow Summary

Once the QUIC/TLS connection is established:

- the daemon sends `session_index` on the control stream
- Android subscribes to the sessions it wants preview for
- the daemon sends `preview_snapshot` only for subscribed sessions
- Android sends `interactive_request` for sessions it wants terminal control of
- the daemon opens one interactive stream per granted session
- snapshot and live PTY bytes flow only on those interactive streams

## References

- `../architecture.md`
- `../contract.md`
- `local-broker.md`
- `../ux/android.md`
- `../reference/state-machines.md`
- `../reference/error-codes.md`
