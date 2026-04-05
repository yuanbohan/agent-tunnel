---
title: feat: Add global multiplexed client update stream
type: feat
status: active
date: 2026-04-05
---

# feat: Add global multiplexed client update stream

## Overview

Introduce one app-level client WebSocket that can deliver live updates for all sessions, with each frame carrying `session_id` and `type` so the mobile client can route updates locally. This stream should be sufficient for session list freshness and `action_required` notifications while the app is in the foreground.

Relay must treat session output as opaque transport data now, regardless of whether transport encryption exists yet. This plan therefore removes relay-side content-aware behavior such as preview extraction and preview-derived list fields. Future encryption remains out of scope for this plan, but the architectural invariant applies immediately: relay forwards output and tracks structured metadata, but it does not inspect content semantics.

## Problem Frame

The old client model was split across:

- `GET /api/sessions` for the current session list snapshot
- `GET /api/session-events/ws` for global `normal` / `action_required` transitions
- `GET /api/sessions/:id/ws` for one session's live output

That shape is workable for a single attached session, but it does not align with the mobile requirement that:

- the app can stay on the session list and still receive new updates
- the app can be inside Session A and still learn that Session B became `action_required`
- the app can maintain one foreground long-lived connection instead of one connection per session

The previous idea of merging `session_state` into one session-scoped WebSocket helps only the active-session case. It does not solve list freshness across all sessions. The better model for mobile is one global foreground connection carrying updates for many sessions, distinguished by `session_id`.

The repo currently still contains relay-side content-aware logic:

- relay extracts `last_preview`
- relay exposes `preview_b64`
- relay tracks `preview_seq`

Those behaviors contradict the desired architecture. This refactor should remove them now rather than treating them as temporary compatibility features.

## Requirements Trace

- R1. The mobile app can maintain one foreground app-level WebSocket to receive relay pushes across all live sessions.
- R2. Every pushed frame on that stream includes enough routing metadata for the client to determine which session it belongs to.
- R3. `action_required` and `normal` transitions remain globally visible without opening a per-session socket.
- R4. The client owns preview generation and preview persistence from opaque output payloads it receives; relay does not derive or publish preview text.
- R5. `GET /api/sessions` continues to include `state`, `state_changed_at`, and `action_required_since`; this remains the canonical snapshot API for session list bootstrap and reconnect recovery.
- R6. Relay-owned content-derived fields are removed from the session snapshot and protocol contract, including `last_preview`, `preview_b64`, and `preview_seq`.
- R7. The protocol shape is compatible with future encrypted output payloads because relay does not need to inspect message content.
- R8. `GET /api/sessions/:id/history` remains the reconnect and catch-up API for per-session output.
- R9. The automated test suite, test assertions, and `Makefile` targets are updated so the new relay contract is the one exercised by default.

## Scope Boundaries

- Do not implement encryption in this change.
- Do not redesign Codex `action_required` detection; that remains derived from Codex App Server lifecycle.
- Do not remove `GET /api/sessions/:id/history`.
- Do not preserve relay-side preview extraction for compatibility.
- Do not turn retained history into a mixed event log.

## Context & Research

### Relevant Code and Patterns

- `relay/server.go` currently exposes:
  - `GET /api/sessions`
  - `GET /api/updates/ws`
  - `GET /api/sessions/:id/history`
- `relay/history.go` stores live output frames per session and supports `after`-based replay.
- `relay/history.go` currently also derives `preview_b64`, `preview_seq`, and `last_preview` from session output.
- `relay/preview.go` contains the ANSI-stripping preview extraction logic that this refactor should remove.
- `relay/session_state.go` feeds structured session state into the global client update stream.
- `protocol/message.go` now separates agent-side `Message` frames from client-facing `ClientUpdateMessage` envelopes.
- `protocol/message.go` also currently exposes `LastPreview`, `PreviewSeq`, and `PreviewB64` on `SessionInfo`, which conflicts with the desired content-opaque relay boundary.
- `Makefile` currently exposes only coarse-grained `test` and `test-real-hitl` targets, so this refactor should ensure those targets and any newly added focused targets reflect the new protocol boundary.
- `relay/server_test.go` already verifies:
  - session list API includes `state`
  - live agent `session_state` updates appear in the list snapshot

### Institutional Learnings

- `docs/solutions/best-practices/use-app-server-lifecycle-for-codex-hitl-state-2026-04-05.md`
  - `action_required` must remain structured session metadata, not PTY heuristics.
- `docs/solutions/best-practices/after-only-terminal-output-sync-2026-04-04.md`
  - output replay should stay output-only and keyed by a session-specific `seq`.

### External References

- None. The repo already has strong local patterns for relay session metadata and output replay.

## Key Technical Decisions

- Use one global client WebSocket for multiplexed live updates.
  Rationale: the list page and cross-session notifications need one connection that spans many sessions, not one connection per session.

- Carry `session_id` on client-visible pushed frames on the global stream.
  Rationale: the client must be able to route each update to the correct local session model.

- Keep `type`-based dispatch for frame semantics.
  Rationale: the client already has a natural discriminator for `output`, `session_state`, and later session-lifecycle events.

- Make relay content-opaque now, not later.
  Rationale: the architectural boundary should not depend on whether payload encryption has shipped. Relay can forward opaque bytes and track structured metadata without deriving preview text or other content semantics.

- Remove relay-generated preview fields from the session snapshot contract.
  Rationale: `last_preview`, `preview_b64`, and `preview_seq` are content-derived relay outputs. Keeping them would preserve the exact coupling this refactor is supposed to remove.

- Keep output replay session-scoped and history-based.
  Rationale: one global live stream does not replace the need for per-session catch-up via `GET /api/sessions/:id/history?after=<seq>`, especially after reconnect.

- Update test entrypoints and repository defaults as part of the protocol change.
  Rationale: this refactor changes the relay contract materially enough that stale test targets, fixtures, or docs would leave the repo validating the wrong behavior.

## Open Questions

### Resolved During Planning

- Should the app keep one WebSocket per session?
  No. That scales poorly and does not match the mobile requirement. One app-level foreground connection is the preferred model for receiving relay pushes.

- Does `GET /api/sessions` already include `action_required` state?
  Yes. `protocol.SessionInfo` already includes `State`, `StateChangedAt`, and `ActionRequiredSince`, and relay tests already assert those fields appear in the session list API.

- Should this plan preserve relay-side preview generation until encryption arrives?
  No. The relay/content separation is a current invariant, not a future optimization. Content-derived preview logic should be removed as part of this refactor.

- What should the new global WebSocket endpoint be called?
  Use `GET /api/updates/ws`. It is client-facing and global in scope.

- Should the global stream reuse `protocol.Message`?
  No. Define a dedicated client-facing update envelope so agent-side relay frames and client-side multiplexed frames do not drift into one overloaded type.

- What should `GET /api/sessions` look like after preview removal?
  Keep it as a pure session metadata snapshot with no content-derived preview fields. It should continue to expose session identity, launcher metadata, sequence/unread metadata, and structured session state.

### Deferred to Implementation

- Whether `session_removed` should be its own frame type or a stateful lifecycle event shape.
  Both can work. The implementation should choose the simpler contract that keeps list bookkeeping explicit.

- No additional live detail socket is planned.
  Live detail rendering should consume the same global `/api/updates/ws` stream and use history only for reconnect or background catch-up.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```text
foreground mobile app
  ├─ GET /api/sessions                  -> snapshot bootstrap
  ├─ GET /api/updates/ws                -> global live stream for all sessions
  └─ GET /api/sessions/:id/history      -> per-session catch-up when needed

global client stream frames
  - {session_id, type:"output", ...opaque output payload...}
  - {session_id, type:"session_state", state, action_required_since, ...}
  - {session_id, type:"session_removed", ...}

client behavior
  - route by session_id
  - update session list model
  - locally compute preview from output payloads
  - on reconnect: refetch /api/sessions, compare seq watermarks, fetch history per session as needed
```

The important architectural separation is:

- relay may forward and optionally buffer opaque output bytes for replay
- relay may safely know structured session metadata such as `action_required`
- clients, not relay, own preview rendering and preview persistence

Current status:

- `GET /api/updates/ws` is the only client WebSocket.
- `GET /api/session-events/ws` has been removed.
- `GET /api/sessions/:id/ws` has been removed.

## Selected API Shape

### Global Live Stream

Endpoint:

```text
GET /api/updates/ws
Authorization: Basic base64(username:password)
```

Client-facing envelope:

```json
{
  "session_id": "sess-1",
  "type": "output",
  "seq": 42,
  "data": "SGVsbG8=",
  "cols": 132,
  "rows": 43
}
```

The plan should introduce a dedicated client-facing type, for example `ClientUpdateMessage`, with:

- `session_id` for routing
- `type` for dispatch
- `seq`, `data`, `cols`, `rows` for output messages
- `state`, `changed_at`, `action_required_since` for session-state messages
- optional lifecycle fields such as `reason` for session removal/disconnect messages

Supported `type` values in the first pass:

- `output`
- `session_state`
- `session_removed`

### Session Snapshot API

Endpoint remains:

```text
GET /api/sessions
Authorization: Basic base64(username:password)
```

After this refactor, the response should remain a pure metadata snapshot. A representative shape is:

```json
[
  {
    "session_id": "sess-1",
    "launcher": "codex",
    "label": "api-fix",
    "cwd": "/repo",
    "command_preview": "codex --profile prod",
    "started_at": "2026-04-05T08:00:00Z",
    "last_active_at": "2026-04-05T08:03:00Z",
    "latest_seq": 42,
    "last_read_seq": 40,
    "unread_count": 2,
    "state": "action_required",
    "state_changed_at": "2026-04-05T08:02:00Z",
    "action_required_since": "2026-04-05T08:02:00Z"
  }
]
```

Fields to keep:

- `session_id`
- `launcher`
- `label`
- `cwd`
- `command_preview`
- `started_at`
- `last_active_at`
- `latest_seq`
- `last_read_seq`
- `unread_count`
- `state`
- `state_changed_at`
- `action_required_since`

Fields to remove:

- `last_preview`
- `preview_seq`
- `preview_b64`

## Implementation Units

- [ ] **Unit 1: Define a global multiplexed client-stream protocol**

**Goal:** Specify the frame contract for app-level live updates that span many sessions.

**Requirements:** R1, R2, R3, R7, R9

**Dependencies:** None

**Files:**
- Modify: `protocol/message.go`
- Modify: `docs/protocol.md`
- Test: `protocol/relay_types_test.go`
- Modify: `Makefile`
- Modify: `README.md`
- Modify: `CLAUDE.md`

**Approach:**
- Add a dedicated client-facing update type, for example `ClientUpdateMessage`, instead of overloading the agent-side `protocol.Message`.
- Define `GET /api/updates/ws` as the authenticated endpoint for multiplexed client updates.
- Ensure the contract can represent at least:
  - output for a session
  - session-state transitions for a session
  - removal/disconnect of a session
- Keep the design compatible with opaque or encrypted output payloads so the relay does not need to interpret content.
- Explicitly document that current plaintext transport still exists today, but relay must still treat output as opaque payload data rather than something to parse.

**Patterns to follow:**
- `protocol.Message`
- current `SessionInfo` state fields in `protocol/message.go`

**Test scenarios:**
- Happy path: a `ClientUpdateMessage` output frame can be encoded and decoded with `session_id` and `type`.
- Happy path: a `ClientUpdateMessage` session-state frame can be encoded and decoded with `session_id`, `state`, and `action_required_since`.
- Edge case: session-scoped and global client-visible frames remain distinguishable without breaking agent-side relay message handling.
- Integration: the default repo test target continues to exercise the updated protocol package after the new client-facing types are added.

**Verification:**
- The protocol docs and tests make it clear how a client routes a pushed frame to one session.

- [ ] **Unit 2: Add a global client WebSocket endpoint for cross-session live updates**

**Goal:** Give the client one foreground connection that can receive pushes for every live session.

**Requirements:** R1, R2, R3, R8, R9

**Dependencies:** Unit 1

**Files:**
- Modify: `relay/server.go`
- Modify: `relay/registry.go`
- Modify: `relay/session_state.go`
- Test: `relay/server_test.go`
- Test: `relay/registry_test.go`
- Modify: `Makefile`

**Approach:**
- Use `GET /api/updates/ws` as the authenticated client WebSocket endpoint.
- Maintain a registry of global client sinks that should receive multiplexed updates for all sessions.
- Broadcast `ClientUpdateMessage` frames for session output and session-state updates onto that global stream with `session_id`.
- Include a clear session-removal event so the client can evict dead sessions from its local live model.

**Patterns to follow:**
- `relay/server.go` WebSocket handler structure
- `relay/session_state.go` global state broadcast pattern
- existing sink backpressure handling in `relay/server.go`

**Test scenarios:**
- Happy path: one global client WebSocket receives output from Session A and Session B with distinct `session_id` values.
- Happy path: the same global client WebSocket receives a `session_state` transition for Session B while Session A is still producing output.
- Edge case: when a session is removed, the global client receives a removal event for the correct `session_id`.
- Error path: one backpressured global client socket disconnects without corrupting relay session state or other clients.
- Integration: a focused relay test target or package-targeted `go test` invocation in `Makefile` covers the new `/api/updates/ws` behavior.

**Verification:**
- A foreground mobile client can stay on one WebSocket and still observe live changes across multiple sessions.

- [ ] **Unit 3: Remove relay-side content-aware preview logic**

**Goal:** Eliminate relay logic and protocol fields that inspect or expose message content semantics.

**Requirements:** R4, R6, R7, R9

**Dependencies:** Unit 1

**Files:**
- Modify: `protocol/message.go`
- Modify: `relay/history.go`
- Delete: `relay/preview.go`
- Delete: `relay/preview_test.go`
- Modify: `relay/registry.go`
- Modify: `protocol/relay_types_test.go`
- Modify: `relay/registry_test.go`
- Modify: `relay/server_test.go`
- Modify: `docs/protocol.md`
- Modify: `docs/architecture.md`
- Modify: `Makefile`
- Modify: `README.md`
- Modify: `CLAUDE.md`

**Approach:**
- Remove `LastPreview`, `PreviewSeq`, and `PreviewB64` from `protocol.SessionInfo`.
- Remove preview extraction and preview bookkeeping from relay session state.
- Update `GET /api/sessions` examples and tests so they no longer imply any preview-bearing fields.
- Keep `latest_seq`, unread counters, and replay behavior where they are purely transport/session metadata rather than content semantics.
- Update docs so `GET /api/sessions` is documented as a metadata snapshot, not a preview-bearing content summary.

**Patterns to follow:**
- `relay/history.go` output replay bookkeeping
- `protocol/message.go` session snapshot contract

**Test scenarios:**
- Happy path: session list responses still include `state`, `latest_seq`, unread counters, and timing metadata after preview fields are removed.
- Edge case: relay output replay remains intact after preview-related state is removed from session bookkeeping.
- Integration: protocol and relay tests no longer expect `last_preview`, `preview_b64`, or `preview_seq` anywhere in session list responses.
- Integration: repository-level test targets no longer reference preview-specific behavior as part of the expected contract.

**Verification:**
- No relay code path derives text preview or exposes content-derived preview fields.

- [ ] **Unit 4: Make list freshness and local preview recovery explicit**

**Goal:** Ensure the client can keep the session list fresh without relying on relay-owned plaintext preview as the long-term contract.

**Requirements:** R4, R5, R7, R8, R9

**Dependencies:** Unit 2, Unit 3

**Files:**
- Modify: `docs/protocol.md`
- Modify: `relay/server_test.go`
- Modify: `relay/history.go`
- Test: `relay/server_test.go`
- Modify: `Makefile`
- Modify: `README.md`
- Modify: `CLAUDE.md`

**Approach:**
- Keep `GET /api/sessions` as the canonical session snapshot API for:
  - `state`
  - `state_changed_at`
  - `action_required_since`
  - `latest_seq`
  - unread-related counters
- Document the reconnect strategy:
  - fetch `/api/sessions`
  - compare local per-session seq watermarks
  - fetch `GET /api/sessions/:id/history?after=<seq>` for sessions that need catch-up
  - locally update preview from the fetched or streamed output payloads
- Document that if the client wants a preview on list load, it must persist local preview state itself or reconstruct it from locally available output history.

**Patterns to follow:**
- `relay/history.go` `after`-based replay
- `relay/server_test.go` list API coverage
- `docs/solutions/best-practices/after-only-terminal-output-sync-2026-04-04.md`

**Test scenarios:**
- Happy path: `GET /api/sessions` continues to return `state`, `state_changed_at`, and `action_required_since` for `action_required` sessions.
- Happy path: list snapshot and global stream together give the client enough metadata to know which sessions need catch-up.
- Edge case: after reconnect, a client can identify that Session B advanced its `latest_seq` even if no live global frames were received while disconnected.
- Integration: history replay remains output-only and does not require session-state frames to rebuild local previews.
- Integration: default and focused test targets document and exercise the reconnect story built around snapshot metadata plus opaque output catch-up.

**Verification:**
- The protocol documents a full reconnect story that does not assume relay can render preview forever.

- [ ] **Unit 5: Make the single-socket model explicit across docs and tests**

**Goal:** Make the one-socket client model explicit and remove references to transitional receive paths.

**Requirements:** R1, R3, R8, R9

**Dependencies:** Unit 4

**Files:**
- Modify: `docs/protocol.md`
- Modify: `relay/server_test.go`
- Modify: `docs/architecture.md`
- Modify: `docs/codex-action-required.md`
- Modify: `Makefile`
- Modify: `README.md`
- Modify: `CLAUDE.md`

**Approach:**
- Document `GET /api/updates/ws` as the only client WebSocket.
- Keep `GET /api/sessions/:id/history` as the catch-up API after reconnect or app resume.
- Remove transitional language about per-session live sockets or legacy state-only sockets.
- Update architecture docs to distinguish:
  - global app-level live updates
  - session-scoped history catch-up
  - structured state vs opaque output content

**Patterns to follow:**
- current relay endpoint naming and docs layout
- `docs/codex-action-required.md` source-of-truth guidance for `action_required`

**Test scenarios:**
- Happy path: `/api/updates/ws` is the only client WebSocket documented and tested.
- Integration: the new global stream does not remove or rename current list API state fields or per-session history behavior.
- Test expectation: documentation updates describe the final single-socket model accurately.
- Integration: `make test` and `make test-real-hitl` are sufficient for an implementer to validate the single-socket contract.

**Verification:**
- Existing clients can keep working while new mobile clients adopt the global receive model.

## System-Wide Impact

- **Interaction graph:** agent -> relay flow remains mostly unchanged; the main addition is a new relay-to-client fanout path for cross-session updates.
- **Error propagation:** the new global stream concentrates foreground delivery into one socket, so backpressure and disconnect semantics become more important at the app level.
- **State lifecycle risks:** the client must reconcile three realities cleanly:
  - snapshot bootstrap from `GET /api/sessions`
  - live global updates while connected
  - per-session history catch-up after reconnect
- **API surface parity:** list API metadata and global pushed frame semantics must agree on `session_id`, `state`, and sequence-related meanings.
- **Integration coverage:** tests must cover coexistence of:
  - list snapshot API
  - global multiplexed client stream
  - per-session history replay
  - existing per-session WebSocket behavior
- **Validation surface:** repository defaults such as `make test`, `make test-real-hitl`, and any new focused test targets must align with the new relay contract so contributors do not accidentally validate obsolete preview behavior.
- **Unchanged invariants:** `action_required` remains derived from Codex App Server lifecycle; retained history remains output-only; relay does not derive preview or other content semantics; encryption is not yet implemented.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| The implementation leaves hidden content-aware relay logic behind | Explicitly remove preview extraction code, preview snapshot fields, and preview-related tests in the same refactor |
| A global stream without reconnect guidance leaves list previews stale after network loss | Pair the global stream with explicit snapshot + per-session history recovery semantics |
| Adding `session_id` to client-visible frames creates confusion with agent-side relay protocol | Keep the new contract clearly scoped to client-facing streams and cover it with protocol tests |
| The new global stream duplicates too much of current per-session behavior | Keep migration boundaries explicit and defer full detail-flow unification to a later step |
| Makefile and top-level test guidance lag behind the protocol refactor | Update `Makefile` and `README.md` in the same change so the default validation path matches the new architecture |

## Documentation / Operational Notes

- Document the recommended mobile runtime model:
  - one foreground app-level WebSocket
  - `GET /api/updates/ws` for global live updates
  - snapshot bootstrap from `GET /api/sessions`
  - per-session history catch-up on reconnect
- Explicitly document that relay is not a content-aware renderer and does not expose preview-bearing session fields.
- Note in docs that end-to-end or client-held encryption is future work, but relay opacity to content semantics is already a required invariant.
- Update `README.md` and `Makefile` guidance so contributors know which test targets validate:
  - the global update stream
  - the preserved per-session replay path
  - the real Codex HITL path after preview removal
- Update `CLAUDE.md` so the repo-level architecture and workflow guidance matches the new relay boundary:
  - relay is content-opaque
  - preview is client-owned
  - the app-level live stream is `GET /api/updates/ws`

## Sources & References

- Related code: `relay/server.go`
- Related code: `relay/registry.go`
- Related code: `relay/history.go`
- Related code: `relay/session_state.go`
- Related code: `protocol/message.go`
- Related tests: `relay/server_test.go`
- Related tests: `relay/registry_test.go`
- Related docs: `docs/protocol.md`
- Related docs: `docs/architecture.md`
- Related docs: `docs/codex-action-required.md`
- Related docs: `README.md`
- Related docs: `CLAUDE.md`
- Institutional learning: `docs/solutions/best-practices/use-app-server-lifecycle-for-codex-hitl-state-2026-04-05.md`
- Institutional learning: `docs/solutions/best-practices/after-only-terminal-output-sync-2026-04-04.md`
