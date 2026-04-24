# WebRTC DataChannel Protocol Direction

## Status

This document captures the recommended shape of the daemon-to-Android WebRTC data plane for the session-connectivity architecture described in `docs/webrtc/architecture.md`.

It is still a planning document. It defines the recommended transport model and message families, not yet the final byte-level wire contract.

## Purpose

The WebRTC data plane exists to carry session content that must not transit Relay as plaintext, including:

- daemon-generated preview content
- interactive terminal snapshots
- interactive terminal live bytes
- interactive input

The data plane does not replace discovery, pairing, or signaling. Those remain in the Relay-owned control plane.

## High-Level Design

The current recommended first-phase design is:

- one `PeerConnection` per online daemon
- multiple sessions multiplexed within that daemon connection
- two DataChannels per daemon connection
- one control-oriented channel for structured messages
- one stream-oriented channel for interactive terminal bytes

This preserves the current `snapshot + live` rendering model for terminal content while avoiding one connection per session.

## Why Two DataChannels

Recommended first-phase channels:

- `control`
- `stream`

Both should be reliable and ordered in the first phase.

### `control` Channel

Carries:

- preview snapshots and updates
- interactive state transitions
- interactive lease acknowledgements and state
- resize events
- structured input intent
- daemon-authored state that belongs to the active interactive session

### `stream` Channel

Carries:

- interactive terminal snapshot bytes
- interactive terminal live bytes

### Rationale

Separating control and stream traffic reduces head-of-line blocking between:

- large terminal snapshots
- frequent live terminal bytes
- small preview and control updates

It also keeps the rendering model cleaner:

- list UI consumes structured control data
- detail UI consumes terminal byte stream data

## Mixed-Protocol Direction

The recommended first-phase design is a mixed protocol:

- structured control messages use message envelopes
- terminal byte content uses binary framing

This is intentional.

It avoids forcing terminal content into base64-heavy JSON while keeping preview and metadata synchronization easy to inspect and evolve.

## Session Multiplexing Model

Each message or frame belongs to one `session_id`.

The daemon connection is therefore:

- daemon-scoped at the transport level
- session-scoped at the message level

This allows:

- all sessions on a daemon to provide preview updates
- one session at a time to enter interactive mode
- future extension without changing the per-daemon connection shape

## Recommended Channel Contracts

### 1. `control` DataChannel

Recommended properties:

- reliable
- ordered
- structured message envelope

Recommended envelope fields:

- `type`
- `session_id`
- `seq`
- `body`

Meaning:

- `type`: message kind
- `session_id`: the session this message belongs to
- `seq`: per-daemon-connection monotonic sequence number
- `body`: structured payload

This keeps reducers, logs, and replay debugging straightforward.

### 2. `stream` DataChannel

Recommended properties:

- reliable
- ordered
- binary frames only

The `stream` channel should not try to be self-describing JSON.

The recommended first-phase simplification is:

- no custom binary header per frame
- each DataChannel message is one raw terminal byte chunk
- control-channel state defines what those chunks currently mean

This works because the current product direction allows only one interactive session at a time. The `stream` channel therefore does not need to multiplex multiple concurrent interactive sessions inside the same binary lane.

## Recommended Control Message Families

### Preview

Recommended messages:

- `preview_snapshot`
- `preview_update`

Purpose:

- provide daemon-generated, lightweight, plain-text preview content for list rendering

Recommended payload fields:

- `text`
- `version`
- `updated_at`

Preview rules:

- preview is plain text, not terminal emulation
- preview is snapshot-based, not diff-based
- preview updates are event-driven and lightly throttled
- preview is generated on the daemon, not on Android

### Interactive State

Recommended messages:

- `interactive_state`
- `interactive_attach_granted`
- `interactive_attach_denied`
- `interactive_attach_released`
- `interactive_snapshot_begin`
- `interactive_snapshot_end`
- `interactive_closed`
- `interactive_resize`

Purpose:

- express when a session enters interactive mode
- coordinate the binary snapshot/live stream lifecycle
- make terminal rendering state explicit

Recommended `interactive_state` fields:

- `active`
- `lease_id`
- `updated_at`

Recommended `interactive_attach_granted` fields:

- `lease_id`
- `session_id`
- `cols`
- `rows`

Recommended `interactive_attach_denied` fields:

- `session_id`
- `reason`

Recommended `interactive_attach_released` fields:

- `lease_id`
- `reason`

Recommended `interactive_snapshot_begin` fields:

- `cols`
- `rows`
- `snapshot_id`

Recommended `interactive_snapshot_end` fields:

- `snapshot_id`

Recommended `interactive_closed` fields:

- `reason`

Recommended `interactive_resize` fields:

- `cols`
- `rows`

`interactive_snapshot_begin` is the daemon telling Android:

- create or reset the terminal emulator for this session
- apply these dimensions
- expect a snapshot byte sequence on the `stream` channel

`interactive_snapshot_end` is the daemon telling Android:

- the snapshot byte sequence is complete
- all subsequent stream frames are live terminal bytes until further notice

This mirrors the current `snapshot + live` product model without making the stream parser guess boundaries.

### Android-To-Daemon Interactive Input

Recommended messages:

- `interactive_input_text`
- `interactive_input_key`
- `interactive_resize_request`

Purpose:

- forward structured input without leaking PTY translation rules into the app
- keep resize changes explicit

Recommended direction:

- interactive attach and release intent is initiated from Relay control plane, not from the DataChannel
- daemon uses DataChannel state updates and acknowledgements only after Relay has granted or released the interactive lease
- structured input itself belongs on the daemon connection once interactive mode is active

This preserves the existing product boundary where PTY-byte translation remains daemon-owned.

## Recommended Stream Channel Framing

The recommended first-phase stream framing is deliberately minimal but still epoch-bound:

- `interactive_snapshot_begin` on the `control` channel declares:
  - which `session_id` is active
  - which `lease_id` is active
  - which `stream_epoch` is active
  - which `snapshot_id` is active
  - the terminal dimensions to apply
- subsequent `stream` channel messages carry raw snapshot byte chunks prefixed by the current `stream_epoch`
- `interactive_snapshot_end` on the `control` channel marks the end of the snapshot phase
- subsequent `stream` channel messages are interpreted as live terminal byte chunks for that same active interactive session and `stream_epoch`
- `interactive_closed` or a new `interactive_snapshot_begin` changes or clears that interpretation

So the `stream` channel itself stays dumb:

- no per-frame session identifier
- no per-frame snapshot marker beyond stream-epoch association
- no full multiplexing header

The state machine lives in the `control` channel.

### Why This Is The Recommended Default

This is the lowest-maintenance design because it matches the current product behavior:

- one interactive session at a time
- one snapshot phase
- then one live-byte phase

It also avoids inventing a binary subprotocol before there is a real need for one.

The only required per-frame binding is a tiny `stream_epoch` header so stale chunks from an old interactive attempt cannot be applied to a newer one if control and stream delivery are observed out of phase.

### When To Revisit This

The project should revisit explicit binary frame headers only if one of these becomes true:

- more than one interactive session must coexist on the same daemon connection
- the stream channel needs independent multiplexing from the control state machine
- a single stream-epoch binding proves insufficient in practice

## Rendering Model

### List Rendering

Android list UI should render from:

- Relay-provided session metadata skeleton
- daemon-provided preview content

It should not create terminal emulators for every session.

### Detail Rendering

Android detail UI should render from:

1. `interactive_snapshot_begin`
2. raw snapshot byte chunks from `stream`, bound to the current `stream_epoch`
3. `interactive_snapshot_end`
4. subsequent raw live byte chunks from `stream`, bound to the same `stream_epoch`

This is intentionally close to the current attach implementation so the mental model stays stable.

## Recovery Model

After reconnect:

- preview resumes from fresh `preview_snapshot` messages
- interactive resumes from a fresh full snapshot sequence
- the system does not attempt missed-byte replay

This matches the current product philosophy and avoids complex gap recovery semantics.

## Recommended Best-Practice Defaults

This document recommends these defaults for the first production slice:

- one `PeerConnection` per daemon
- two DataChannels per daemon: `control` and `stream`
- both channels reliable and ordered
- mixed protocol: structured control plus binary terminal stream
- preview as daemon-generated plain-text snapshots
- interactive content as explicit snapshot sequence plus live stream
- Relay owns interactive attach/release intent
- DataChannel owns only post-lease interactive state and input
- raw stream chunks with a minimal `stream_epoch` binding in phase 1
- no base64 terminal payload in JSON
- no diff protocol for preview
- no missed-byte replay on reconnect

## Open Decisions For Later Discussion

These areas still need explicit review before the protocol is final:

- whether `seq` should be per-channel or per-daemon-connection
- whether interactive input acknowledgements are required or whether state updates are sufficient
- whether control and stream channels should be opened eagerly on connection start or lazily once needed

## Follow-Up Documents

This document should be followed by:

- `docs/webrtc/pairing-protocol.md`
- `docs/webrtc/session-index-contract.md`

Those documents should refine the trust bootstrap and Relay metadata contract without redefining the content-plane boundaries established here.
