# WebRTC Realtime Protocol Direction

## Status

This document captures the recommended shape of the new Relay realtime WebSocket layer that supports session discovery, pairing state, and WebRTC signaling for the WebRTC session-connectivity architecture described in `docs/webrtc/architecture.md`.

This is still a planning document. It defines recommended protocol direction and state-model boundaries, not yet final wire-format specification.

## Purpose

The realtime WebSocket layer exists to carry:

- authenticated control-plane state
- session and daemon discovery deltas
- pairing and revoke state
- WebRTC signaling
- session-control intent
- user-visible connection state

It must not carry terminal payload content.

## Role In The System

The new architecture separates concerns into:

- `GET /api/sessions`: discovery snapshot over HTTP
- realtime WebSockets: control plane and signaling
- WebRTC DataChannels: preview and interactive content

The realtime WebSocket is therefore the Relay-owned control-plane stream. It is not the replacement for terminal attach bytes.

## Recommended Endpoints

Recommended endpoint split:

- app-side realtime WebSocket
- daemon-side realtime WebSocket

The current direction is to keep them as separate routes with the same protocol envelope and the same event-family style, while authorizing them as different actors.

The exact route names are still deferred, but the architectural intent is:

- Android connects as an app actor
- daemon connects as a daemon actor
- both speak one shared protocol family

## Core Design Principles

### 1. One Shared Envelope

All realtime messages should use one envelope shape.

Recommended fields:

- `type`
- `seq`
- `ts`
- `body`

Meaning:

- `type`: event kind
- `seq`: per-WebSocket monotonic sequence number
- `ts`: server-generated timestamp
- `body`: event payload

This keeps logging, debugging, and client state reconciliation simple.

### 2. One Shared Event Family Style

The app-side and daemon-side realtime WebSockets should use the same event naming model where their domains overlap.

They may still differ in:

- which events each side may emit
- which events each side may receive
- authorization checks per event

But they should not become two unrelated protocol languages.

### 3. Snapshot Then Delta

On initial connection, Relay should push startup snapshots first, then only deltas.

This avoids forcing clients to bootstrap by piecing together arbitrary event races.

### 4. Control Plane Only

Realtime WebSockets must not carry:

- preview text
- terminal snapshot bytes
- live terminal bytes
- interactive input plaintext

Those belong to the WebRTC data plane.

## Recommended Startup Sequence

Startup sequence is actor-specific.

### App-Side Startup Sequence

After app authentication and connection establishment, Relay should send startup events in this fixed order:

1. `daemon_index_snapshot`
2. `session_index_snapshot`
3. `pairing_state_snapshot`
4. `connection_state_snapshot`
5. `realtime_ready`

### Daemon-Side Startup Sequence

After daemon authentication and connection establishment, Relay should send only daemon-relevant startup state, then `realtime_ready`.

The daemon-side startup sequence must not be described as receiving the app discovery snapshots by default.

### Why Fixed Ordering

Fixed ordering keeps client initialization deterministic.

Without a fixed order, clients must infer whether:

- a snapshot type has not arrived yet
- a snapshot type is empty
- startup is complete

That adds avoidable complexity.

### `realtime_ready`

`realtime_ready` should be a lightweight end-of-bootstrap marker.

Recommended purpose:

- mark initial state completion
- provide a stable logging checkpoint
- allow clients to transition from bootstrap mode to steady-state delta mode

Possible fields:

- `connection_id`
- `server_time`
- `actor_type`
- `actor_id`

The exact field set can stay minimal.

## Recommended Snapshot Families

### `daemon_index_snapshot`

Purpose:

- initial daemon list visible to the current actor
- daemon routing and list skeleton state

Recommended fields per daemon:

- `daemon_id`
- `daemon_display_name`
- `online`
- `last_seen_at`
- `platform_family`
- `platform_id`

### `session_index_snapshot`

Purpose:

- initial session index visible to the current actor
- list skeleton and routing state

Recommended fields per session:

- `session_id`
- `daemon_id`
- `label`
- `command_preview`
- `cwd`
- `git_branch`
- `started_at`
- `updated_at`
- `online`
- `interactive_capable`

This follows the current planning boundary that treats these as metadata rather than terminal payload.

### `pairing_state_snapshot`

Purpose:

- describe the current app actor's daemon pairing visibility

Recommended scope:

- only the current Android device's pairing relationship to daemons
- not the daemon's full trusted-mobile roster

Recommended fields:

- `daemon_id`
- `paired`
- `paired_at`
- `paired_device_display_name`

This keeps app state focused on "can this device access this daemon" and leaves full device roster management to daemon-local management surfaces such as `tunnel daemon devices`.

### `connection_state_snapshot`

Purpose:

- describe Relay-known connection state for each daemon
- support direct vs relay visibility in the Android UI

Recommended fields:

- `daemon_id`
- `transport_state`
- `path_kind`
- `interactive_session_id`
- `control_plane_available`

Recommended `transport_state` values:

- `disconnected`
- `connecting`
- `connected`
- `degraded`

Recommended `path_kind` values:

- `unknown`
- `direct`
- `relay`

This keeps the user-facing state model simple while allowing deeper WebRTC internals to remain in logs or diagnostics rather than hardening them into the app contract.

`path_kind` describes the media path only. It does not claim that Relay-mediated control actions are currently available.

## Recommended Delta Families

The recommended first-phase model is to keep deltas domain-specific rather than abstracting them into generic resource events.

Recommended delta events:

- `daemon_upsert`
- `daemon_remove`
- `session_upsert`
- `session_remove`
- `pairing_upsert`
- `pairing_remove`
- `connection_state_upsert`

## Upsert Semantics

Recommended rule:

- every `upsert` event carries a full object
- do not use partial patch semantics in the first phase

Reasons:

- simpler client reducers
- easier event logging and debugging
- fewer compatibility hazards when fields evolve
- no need to maintain separate patch semantics for each resource family

Recommended `remove` rule:

- `remove` events carry only the minimum stable identifier needed to delete the resource from the local store

## Recommended Signaling Events

The realtime WebSocket is the right place for WebRTC signaling.

Recommended signaling families:

- `webrtc_offer`
- `webrtc_answer`
- `webrtc_ice_candidate`
- `webrtc_closed`
- `peer_connection_state`

Every signaling event should carry:

- `peer_connection_id`
- `negotiation_id`

Recommended first-phase behavior:

- use WebRTC perfect-negotiation semantics rather than inventing a custom glare-resolution protocol
- discard signaling messages whose `peer_connection_id` or `negotiation_id` is stale relative to the current daemon connection state

Rationale:

- signaling is a control-plane concern
- Relay is already the authorization and routing authority
- keeping signaling inside the same authenticated realtime channel simplifies client architecture

## Recommended Session-Control Events

Recommended control-intent families:

- `interactive_attach_request`
- `interactive_attach_release`
- `interactive_attach_granted`
- `interactive_attach_denied`
- `interactive_attach_released`
- `interactive_lease_state`

Recommended direction:

- Android sends session-control intent to Relay
- Relay validates authorization and routes the intent to the daemon
- daemon changes the data-plane behavior on the corresponding WebRTC connection
- interactive ownership is a lease bound to one authenticated app connection

This keeps interactive ownership and state transitions under the same Relay-managed control plane as discovery and signaling.

Recommended lease fields:

- `lease_id`
- `session_id`
- `device_id`
- `app_connection_id`
- `state`
- `reason`

Recommended states:

- `idle`
- `pending`
- `active`
- `denied_busy`
- `released`
- `preempted`
- `expired`

## Relationship To The Data Plane

The realtime WebSocket should coordinate the data plane, not carry it.

Specifically:

- realtime WebSocket tells the system which daemons and sessions exist
- realtime WebSocket tells the system which pairing relationships and connection states exist
- realtime WebSocket tells the system when interactive mode should be entered or released
- WebRTC carries preview and interactive content

This allows the Android app to use:

- Relay metadata for instant list skeleton rendering
- daemon-generated preview content for live list updates
- full terminal rendering only for the active interactive session

## Best-Practice Defaults Chosen Here

The following are the current best-practice defaults recommended by this document:

- use a fixed startup sequence
- use an explicit `realtime_ready` marker
- use actor-specific startup snapshots
- use domain-specific snapshot families
- use domain-specific delta families
- use full-object `upsert` semantics
- keep signaling on the same realtime WebSocket family
- keep session-control intent on the same realtime WebSocket family
- bind interactive ownership to an explicit lease
- keep terminal payload off the realtime WebSocket entirely

## Open Decisions For Later Discussion

These areas still need explicit product or implementation review before the protocol can be finalized:

- the exact route names for app and daemon realtime WebSockets
- the exact envelope field names and timestamp encoding
- whether server-generated `seq` should be global per connection or segmented by event family
- whether `connection_state_upsert` should include additional diagnostic detail beyond `transport_state` and `path_kind`
- whether any daemon-local administrative events should reuse this protocol family or stay outside it

## Follow-Up Documents

This document should be followed by:

- `docs/webrtc/datachannel-protocol.md`
- `docs/webrtc/pairing-protocol.md`
- `docs/webrtc/session-index-contract.md`

Those documents should refine implementation detail without redefining the control-plane boundaries established here.
