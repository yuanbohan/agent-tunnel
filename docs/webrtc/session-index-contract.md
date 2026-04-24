# WebRTC Session Index Contract Direction

## Status

This document captures the recommended boundary between:

- Relay-owned session index metadata
- daemon-owned session content and richer live state

It exists to keep `GET /api/sessions`, the realtime control plane, and the WebRTC data plane aligned as the system migrates away from Relay-terminated attach transport.

This is still a planning document, not yet the final API contract.

## Core Rule

The architecture uses a split contract:

- Relay owns the minimal session index needed for discovery, routing, authorization, and fast list rendering
- daemon owns session content and content-derived live state

This means:

- Relay may know metadata
- Relay must not know terminal payload content

## Why This Split Exists

The product needs both:

- fast global list rendering across multiple daemons
- zero-trust handling for actual terminal content

If Relay owns too little, Android must connect to every daemon before it can show a useful list.

If Relay owns too much, Relay stops being zero-trust for payload-adjacent data.

The chosen compromise is:

- Relay as the source of truth for the session index
- daemon as the source of truth for session content

## Relay-Owned Session Index

`GET /api/sessions` and the Relay realtime control plane should expose a stable index that answers:

- what sessions exist
- which daemon owns each session
- whether the current account and paired device may discover them
- enough metadata to make the list useful before WebRTC content arrives

## Recommended Relay Index Fields

Recommended first-phase fields:

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

These fields are currently treated as metadata rather than payload.

## Daemon-Owned Content Fields

The following should remain outside Relay plaintext handling and belong to the daemon-to-Android WebRTC data plane:

- preview text
- preview versioning
- terminal snapshot bytes
- live terminal bytes
- interactive input payloads
- any future transcript-like or buffer-derived content views

These fields are content-bearing and should not become part of the Relay discovery contract.

Phase 1 does not define daemon-authored replacements for Relay metadata fields such as `command_preview`, `cwd`, or `git_branch`. Those fields remain Relay-index metadata, not content-plane metadata.

## HTTP vs Realtime vs Data Plane

### HTTP Discovery

`GET /api/sessions` should provide:

- the current Relay-owned session index snapshot
- enough data to render a useful list skeleton

It should not attempt to provide preview content.

For the Android app, phase 1 should not require both HTTP bootstrap and realtime bootstrap on every startup. The preferred mobile path is:

- realtime startup snapshots as the authoritative bootstrap once the realtime WebSocket is connected
- `GET /api/sessions` as a compatibility surface, manual refresh surface, and non-realtime consumer surface

### Realtime WebSocket

The realtime control plane should provide:

- `session_index_snapshot`
- `session_upsert`
- `session_remove`

These should mirror the Relay-owned session index contract and keep clients current between HTTP refreshes.

### WebRTC Data Plane

The daemon connection should provide:

- daemon-authored preview content
- daemon-authored session content
- daemon-authored interactive content state

This is where the list moves from "metadata only" to "live preview", and where detail view becomes full interactive terminal rendering.

## Metadata Authority

Phase-1 authority is intentionally simple:

- Relay is authoritative for session-index metadata fields
- daemon is authoritative for preview and interactive content fields

Clients should not expect `label`, `command_preview`, `cwd`, or `git_branch` to be corrected by daemon-side metadata in phase 1.

## Freshness Model

Recommended first-phase freshness rules:

- Relay index is the authoritative discovery list
- Relay `updated_at` is sufficient for sorting and list ordering
- daemon preview content may arrive after the Relay list is already visible
- Android may temporarily render metadata without preview until the daemon connection is ready

This preserves good UX without pulling preview plaintext into Relay.

## Security Boundary

The contract intentionally draws the confidentiality line at real session content, not at all metadata.

That means:

- metadata like `label`, `command_preview`, `cwd`, and `git_branch` may remain Relay-visible
- preview text and terminal content must remain outside Relay plaintext handling

This matches the current product decision to optimize for simple UX and low maintenance while still making Relay zero-trust for the most important data.

Because these metadata fields remain Relay-visible long-term, they must be sanitized before exposure:

- `command_preview` must be a display-safe projection, not a raw argv dump
- obvious secret-like argv tokens must be redacted before Relay exposure
- `cwd` should be normalized for display before Relay exposure
- metadata logging and retention should not exceed the live session-index contract

## Recommended Client Behavior

Android should build each session row from two layers:

1. Relay metadata layer
2. daemon preview layer

Practical behavior:

- render the row immediately from Relay metadata
- fill preview once WebRTC content arrives
- update preview independently of the Relay index
- keep detail view entirely driven by interactive daemon content

This produces fast first paint without requiring terminal emulation for every session.

## Best-Practice Defaults Chosen Here

This document recommends:

- keep `GET /api/sessions` as the discovery anchor
- keep Relay metadata reasonably rich
- keep `command_preview`, `cwd`, and `git_branch` in the Relay index long-term unless a future security boundary changes explicitly
- keep preview text out of Relay
- keep terminal content out of Relay
- keep Relay authoritative for session-index metadata fields in phase 1
- sanitize Relay-visible metadata before exposure or local caching

## Open Decisions For Later Discussion

These areas may still need explicit product review:
- whether a later phase should move any currently Relay-visible metadata behind daemon-authored views

## Related Documents

- `docs/webrtc/architecture.md`
- `docs/webrtc/realtime-protocol.md`
- `docs/webrtc/datachannel-protocol.md`
