# WebRTC Session Connectivity Architecture

## Status

This document captures the architecture direction agreed during the April 24, 2026 planning discussion. It is a product-and-system direction document, not yet a wire-level implementation contract.

The goal is to keep one stable place for the major decisions before protocol details are split into follow-up docs under `docs/webrtc/`.

## Goals

- Move session traffic off the Relay-centric attach WebSocket path.
- Prefer direct device-to-device connectivity for session data.
- Fall back automatically through relay-assisted traversal when direct connectivity fails.
- Keep Relay zero-trust for terminal payloads.
- Keep user operation simple.
- Preserve a server-side account model for subscription and entitlement.

## Non-Goals

- No device-level VPN or general network overlay.
- No requirement for users to install extra networking software.
- No attempt to hide all discovery metadata from Relay.
- No attempt to remove the account system.
- No automatic trust inheritance across all devices in the same account.

## High-Level Shape

The system is split into three layers:

1. Account and entitlement layer
2. Device trust and pairing layer
3. Session transport layer

### 1. Account And Entitlement Layer

The account system remains.

It exists to support:

- subscriptions and feature entitlements
- multiple phones and multiple computers under one paid account
- login-required mobile usage
- Relay authorization for discovery, signaling, and TURN usage

The account layer is not the payload trust model.

### 2. Device Trust And Pairing Layer

Device trust is separate from account ownership.

Rules:

- Android app must be logged in before it can pair or use sessions.
- Same-account devices are not automatically trusted.
- Each daemon must be paired explicitly.
- Pairing is daemon-scoped and long-lived until revoked.
- Pairing trust is stored locally on the daemon and Android device.
- Pairing trust is not cloud-restored through Relay.
- The daemon is the trust root for pairing grants.
- Relay stores a derived authorization copy that must be synchronized from daemon-approved pairing state.

The primary pairing flow is daemon-initiated:

1. User runs `tunnel daemon pair` on the computer.
2. The daemon starts if needed.
3. The daemon creates a short-lived, one-time, account-bound pairing invitation.
4. The CLI shows a QR code.
5. The logged-in Android app scans the QR code.
6. Android sends a pairing response back through Relay.
7. The daemon validates the invitation and response locally.
8. The daemon records the Android device as trusted.

Relay may transport the pairing response, but Relay is not the trust root.

## Relay Role

Relay remains the control plane.

Relay is responsible for:

- account authentication
- subscription and entitlement checks
- device ownership and pairing graph
- daemon online presence
- minimal session discovery index
- realtime signaling
- TURN credential and relay authorization
- revocation fanout for active app and daemon connections

Relay is not responsible for:

- terminal-state authority
- preview generation
- terminal payload decryption
- device trust recovery

## Source Of Truth Boundaries

The architecture intentionally uses split truth sources.

### Relay Is The Source Of Truth For

- account ownership
- entitlement state
- device ownership
- daemon presence
- daemon-synchronized paired-device authorization copy
- minimal session index

### Daemon Is The Source Of Truth For

- session content
- preview content
- interactive terminal state
- pairing grants
- terminal snapshots
- live terminal bytes

## Discovery Boundary

`GET /api/sessions` remains in the product.

Its role changes to: discovery snapshot and list skeleton, not session-content delivery.

Relay may keep plaintext metadata that is operationally useful and not terminal-content itself, including:

- `session_id`
- `daemon_id`
- `daemon_display_name`
- `label`
- `command_preview`
- `cwd`
- `git_branch`
- `started_at`
- `updated_at`
- `online`
- `interactive_capable`

Relay must not hold terminal-content-bearing fields such as:

- preview text
- terminal snapshot bytes
- live terminal bytes
- interactive input payloads

This project treats terminal content as the primary confidential payload. Session metadata is allowed to remain in Relay when that keeps the system simpler and more usable.

These Relay-visible metadata fields must still be treated as sanitized display metadata rather than raw process state. In particular:

- `command_preview` must be a redacted display string, not an unfiltered raw argv dump
- `cwd` should be normalized for display before Relay exposure
- metadata must not be logged or retained more broadly than the live session-index contract requires

## Transport Architecture

### Control Plane

Relay exposes:

- `GET /api/sessions` for discovery snapshot
- a new app realtime WebSocket
- a new daemon realtime WebSocket

The realtime WebSockets use:

- one shared envelope shape
- one shared event family and naming style
- server-pushed startup snapshots
- delta events after snapshot

These realtime WebSockets carry:

- daemon and session index state
- pairing and revoke events
- WebRTC offer/answer/candidate signaling
- connection-state updates such as direct vs relay
- session-control intent

They do not carry terminal content.

The app-side and daemon-side realtime sockets are actor-specific. Shared taxonomy does not mean identical startup snapshots or identical event subsets.

### Data Plane

Session data moves to WebRTC.

Current direction:

- Android uses WebRTC.
- The daemon uses Pion WebRTC.
- NAT traversal uses STUN/TURN with `coturn`.
- Direct connection is preferred.
- TURN relay fallback is automatic.
- First-phase payload confidentiality relies on WebRTC/DTLS.
- Relay issues ephemeral TURN credentials after account, entitlement, and pairing checks.

The first phase does not add a second application-layer encryption envelope on top of WebRTC payload transport.

## Connection Model

The transport is daemon-scoped, not session-scoped.

Rules:

- Android keeps one `PeerConnection` per online daemon.
- Android keeps all online daemons connected in the first phase.
- Each daemon connection multiplexes that daemon's sessions.
- All online sessions for that daemon are subscribed for preview by default.
- At most one session is interactive globally at a time.
- Interactive control is lease-based and bound to one authenticated app connection at a time.

This keeps the product simple while avoiding one network connection per session.

## Preview And Interactive Model

Preview and interactive are not separate products. They are two consumption modes of the same daemon-side session authority.

### Preview

- Preview is generated on the daemon.
- Preview is a lightweight pure-text projection.
- Preview is not full terminal emulation.
- Preview is sent as snapshot-style updates, not diffs.
- Preview updates are event-driven with light throttling.
- Android may cache recent preview locally to improve perceived startup speed.

The list UI renders preview as ordinary text.

### Interactive

Interactive terminal rendering keeps the existing mental model:

1. full snapshot
2. then live bytes

On reconnect, interactive recovery uses a fresh snapshot rather than replaying missed bytes.

The detail UI renders interactive content through a terminal emulator.

## DataChannel Content Model

The settled direction for interactive content is to keep a mixed protocol:

- control messages stay structured
- terminal stream payload stays binary

The current recommended first-phase default is:

- one reliable ordered `control` DataChannel
- one reliable ordered `stream` DataChannel

In practice this means:

- structured session metadata and preview synchronization use message envelopes
- interactive terminal snapshot/live transport keeps byte-stream semantics with stream-epoch binding

This preserves the current `snapshot + live` rendering model instead of forcing terminal content into base64-heavy JSON.

The exact DataChannel message taxonomy and stream-frame layout will be specified in follow-up protocol docs.

## Security Model

The system is designed as zero-trust for payloads, not zero-knowledge for all metadata.

That means:

- Relay can know accounts, devices, pairing relationships, daemon presence, and the minimal session index.
- Relay cannot read preview content or terminal content.
- TURN fallback does not terminate terminal plaintext at Relay.
- Device trust comes from daemon-approved pairing, not from Relay asserting trust by itself.
- Pairing requires Android device proof-of-possession of a persistent device key.
- Revocation must terminate existing live daemon connections as well as future discovery/signaling authorization.

## User Experience Summary

The intended user flow is:

1. User logs into the Android app.
2. User runs `tunnel auth login` on the computer if needed.
3. User runs `tunnel daemon pair`.
4. The daemon auto-starts if required and shows a QR code.
5. The phone scans the QR code.
6. Pairing completes.
7. The phone sees the daemon's sessions.
8. The phone quickly gets list metadata from Relay and preview content from the daemon.
9. Entering a session upgrades that session to interactive on the existing daemon connection.

## First-Phase CLI Surface

The current agreed minimal CLI surface is:

- `tunnel auth login`
- `tunnel daemon start`
- `tunnel daemon pair`
- `tunnel daemon devices`
- `tunnel daemon revoke <device>`

`tunnel daemon pair` should auto-start the daemon if it is not already running.

## Follow-Up Docs

This file is the architecture anchor. Follow-up docs under `docs/webrtc/` should split out implementation detail without redefining the major decisions above.

Expected follow-ups include:

- realtime WebSocket protocol
- DataChannel protocol
- pairing protocol
- Relay index field contract
- Android connection and caching behavior
- mobile reference
- daemon preview generation rules

## Open Design Areas

These are intentionally still open:

- the exact realtime WebSocket event names and field schema
- the exact DataChannel message taxonomy
- the exact preview projection format and truncation rules
- the exact daemon preview generation contract
