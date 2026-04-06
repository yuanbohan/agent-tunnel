---
title: feat: Clarify best-effort remote output contract
type: feat
status: completed
date: 2026-04-06
origin: docs/brainstorms/2026-04-06-relay-output-best-effort-requirements.md
---

# feat: Clarify best-effort remote output contract

## Overview

Align the repository's user-facing and protocol-facing documentation with the current implementation of remote output delivery. The plan does not add delivery guarantees, gap signaling, durable history, or reconnect epoch semantics. It makes the present contract explicit: remote interaction is supported for real use, `GET /api/updates/ws` is a best-effort live channel, relay `seq` is relay-local ordering metadata, and `GET /api/sessions/:id/frames` is the standard relay-side recovery path for recently retained output.

## Problem Frame

The project already behaves like a minimal relay plus local CLI wrapper, but the docs still under-specify the remote output contract. Today the local terminal is the most reliable view of the PTY session, while the relay path is intentionally lighter weight. The implementation already shows why:

- `agentunnel` may drop relay-bound output locally when the connector outbound queue is full
- relay `seq` is assigned only after output reaches relay-retained history
- retained replay is bounded, in-memory, and tied to the life of the live session

That means remote/mobile clients can observe and interact with real sessions, but they are not yet promised a lossless transcript. The plan's job is to make that boundary explicit and consistent across `README.md`, `docs/protocol.md`, `docs/architecture.md`, and `CLAUDE.md` / `AGENTS.md` without changing runtime behavior (see origin: `docs/brainstorms/2026-04-06-relay-output-best-effort-requirements.md`).

## Requirements Trace

- R1-R4. Define the best-effort live contract, remote-work usability, the local terminal as the primary complete view, and the exact meaning of relay `seq`.
- R5-R8. Define `/api/sessions/:id/frames` as the standard relay-side recovery path while preserving its live-only, bounded-history limits.
- R9-R12. Align the four core docs, avoid implying lossless delivery, and note stronger guarantees only as future work.

## Scope Boundaries

- No runtime code changes to connector buffering, relay history retention, websocket behavior, or session lifecycle.
- No new protocol messages, degraded-stream events, gap signaling, or delivery acknowledgements.
- No durable relay-side persistence, archive API, or reconnect epoch semantics.
- No changes to the content-opaque relay boundary.
- No new frontend or mobile client work in this phase.

## Context & Research

### Relevant Code and Patterns

- `connector/connector.go` is the key source for the current best-effort claim: `WriteOutput` drops relay-bound output when the outbound queue is full rather than blocking local PTY flow.
- `relay/history.go` defines retained-frame semantics: `seq` is assigned when relay appends a frame, history is in-memory, and retention is byte-bounded.
- `relay/server.go` defines the client-facing contract surfaces this plan must describe: `GET /api/updates/ws`, `GET /api/sessions`, and `GET /api/sessions/:id/frames`, plus same-origin checking for browser websocket clients when `Origin` is present.
- `protocol/message.go` defines the wire fields whose semantics need to be clarified, especially `SessionInfo.latest_seq`, output frame `seq`, `cols`, `rows`, and `ts`.
- `README.md`, `docs/architecture.md`, and `docs/protocol.md` already describe the relay as live-only and content-opaque; this change should tighten those descriptions rather than inventing a new product shape.
- `AGENTS.md` is a symlink to `CLAUDE.md` in this repository, so updating `CLAUDE.md` updates both instruction surfaces.

### Institutional Learnings

- No `docs/solutions/` entries exist in this repository today.

### External References

- None. The codebase already contains the relevant behavior and contract surfaces, and this phase is documentation alignment rather than a standards-driven implementation change.

## Key Technical Decisions

- Treat this as a documentation-contract change, not an implementation project. The plan should not smuggle in new runtime semantics.
- Define `GET /api/updates/ws` as best-effort live output plus structured input, with language that supports real remote work but does not imply lossless delivery.
- Define relay `seq` strictly as ordering metadata for frames the relay accepted and retained. Do not describe it as end-to-end integrity or completeness metadata.
- Define `/api/sessions/:id/frames` as the standard relay-side recovery path after reconnect, but state clearly that it only replays currently retained frames for the still-live session.
- Keep future stronger-delivery work in the docs as a brief forward-looking note only. Do not turn this phase into a design commitment for durable history or lossless transport.
- Use `docs/protocol.md` for the precise contract, `README.md` for operator-level expectations, `docs/architecture.md` for system boundary framing, and `CLAUDE.md` for repository guardrails and contributor expectations.

## Open Questions

### Resolved During Planning

- Should this phase introduce new protocol events or reconnect semantics? No. The current phase is limited to clarifying the current contract.
- Should the docs still say remote work is supported? Yes, but with an explicit best-effort output caveat.
- Should `/api/sessions/:id/frames` be positioned as an optional helper or the normal recovery path? It should be the standard relay-side recovery path for recently retained output.
- Should the future stronger-delivery idea appear in the docs? Yes, but only as a brief future-direction note that does not blur current guarantees.

### Deferred to Implementation

- The exact sentence-level wording for "best-effort remote work" versus "lossless transcript" should be tuned during editing so the README stays concise and the protocol doc stays precise.
- The exact placement of the future-direction note can be finalized during editing as long as it is clearly separated from the current contract.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```text
local PTY output
  -> agentunnel connector outbound queue
       -> if queue has room: frame goes to relay
       -> if queue is full: remote-visible output may be dropped locally
  -> relay receives output
       -> relay assigns seq + ts when appending retained history
       -> live client updates and /frames replay are both derived from this relay-local record

client reconnect pattern
  -> reconnect to /api/updates/ws
  -> use /api/sessions/:id/frames as the standard way to recover recent relay-retained output
  -> do not assume seq proves complete end-to-end delivery from the PTY
```

## Implementation Units

- [x] **Unit 1: Clarify the operator-facing contract in `README.md`**

**Goal:** Make the README describe remote usage the way the product actually behaves today: useful for real remote interaction and work, but with best-effort live output and relay-retained recovery semantics.

**Requirements:** R1-R3, R5-R8, R9, R11-R12

**Dependencies:** None

**Files:**
- Modify: `README.md`

**Approach:**
- Add concise wording near the product overview and client-connection sections that `GET /api/updates/ws` is a best-effort live channel.
- Keep the README product-level, not protocol-heavy: explain that mobile/external clients can observe and interact with real sessions, the local terminal is still the most complete view, and `/api/sessions/:id/frames` is the standard way to recover recent relay-retained output after reconnect.
- Add a brief future-direction note that stronger delivery guarantees may be considered later without implying they already exist.

**Patterns to follow:**
- Existing README style: short operator-facing paragraphs, minimal protocol detail, links out to `docs/protocol.md` for exact wire semantics
- `connector/connector.go`, `relay/server.go`, and `relay/history.go` as the implementation sources that the README wording must stay faithful to

**Test scenarios:**
- Test expectation: none -- documentation-only unit. Validate wording against the current runtime behavior in `connector/connector.go`, `relay/server.go`, and `relay/history.go` instead of adding automated tests.

**Verification:**
- A new reader can understand that remote work is supported, but the live remote output path is best-effort and recovery relies on relay-retained frames rather than a guaranteed transcript.

- [x] **Unit 2: Tighten protocol and architecture semantics for replay, ordering, and recovery**

**Goal:** Make `docs/protocol.md` and `docs/architecture.md` precise enough that client authors can reason correctly about `seq`, retained frames, reconnect recovery, and current relay boundaries.

**Requirements:** R1-R8, R9-R10, R12

**Dependencies:** Unit 1

**Files:**
- Modify: `docs/protocol.md`
- Modify: `docs/architecture.md`

**Approach:**
- In `docs/protocol.md`, explicitly define `seq` as ordering over relay-recorded frames, not proof of complete PTY delivery.
- Document the recommended reconnect flow: reattach to `GET /api/updates/ws`, then use `/api/sessions/:id/frames` to recover recent relay-retained output when needed.
- Clarify that retained frames are live-only, in-memory, bounded history for the current live session only.
- In `docs/architecture.md`, add the missing system-boundary language that the remote path is best-effort while the local PTY path remains primary, and ensure the output/recovery flow description matches the protocol wording.
- Use this pass to add any missing client-surface constraints already implemented, especially the current same-origin check for browser websocket clients when `Origin` is present.

**Patterns to follow:**
- Existing protocol style in `docs/protocol.md`: exact field semantics, endpoint-by-endpoint framing, terse notes under examples
- Existing architecture style in `docs/architecture.md`: stable system boundaries and directional data-flow diagrams
- `protocol/message.go`, `relay/history.go`, and `relay/server.go` as the implementation authorities for field meanings and replay behavior

**Test scenarios:**
- Test expectation: none -- documentation-only unit. Verify that every protocol claim is backed by `protocol/message.go`, `relay/history.go`, and `relay/server.go`.

**Verification:**
- A client author can answer three questions from the docs alone: what `seq` means, what `/frames` can recover, and what recovery flow to use after reconnect.

- [x] **Unit 3: Align repository guardrails and perform a cross-document consistency pass**

**Goal:** Ensure the repository's contributor instructions and the four core docs state one consistent contract and do not drift immediately after editing.

**Requirements:** R9-R12

**Dependencies:** Unit 1, Unit 2

**Files:**
- Modify: `CLAUDE.md`

**Approach:**
- Update `CLAUDE.md` so contributor guidance preserves the same best-effort remote-output contract and same-origin constraint that the public docs now describe. Because `AGENTS.md` is a symlink, no separate file edit is required.
- Run a final consistency pass across all four docs so that:
  - `README.md` stays concise and operator-facing
  - `docs/protocol.md` carries the exact semantics
  - `docs/architecture.md` explains the system boundary and data flow
  - `CLAUDE.md` captures the repository invariants contributors must preserve
- During this pass, remove any wording that accidentally implies end-to-end losslessness or a durable transcript.

**Patterns to follow:**
- Documentation alignment rules already stated in `CLAUDE.md`
- Existing repo practice of keeping `README.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md` aligned when behavior or scope changes

**Test scenarios:**
- Test expectation: none -- documentation-only unit. Completion is established by a consistency review against the current implementation and the origin requirements, not by runtime tests.

**Verification:**
- The four core docs can be read together without contradicting each other on remote output guarantees, recovery behavior, or current product boundaries.

## System-Wide Impact

- **Interaction graph:** No runtime interaction graph changes. This work only changes how existing connector, relay, and client surfaces are described.
- **Error propagation:** No runtime error-propagation changes. The only change is making current best-effort behavior explicit to readers and client authors.
- **State lifecycle risks:** The main risk is documentation drift or over-correction that makes the product sound weaker than it is. The plan should preserve "remote work is supported" while clarifying "live output is not guaranteed lossless."
- **API surface parity:** `README.md`, `docs/protocol.md`, `docs/architecture.md`, and `CLAUDE.md` must describe the same contract for `/api/updates/ws`, `/api/sessions/:id/frames`, relay `seq`, and same-origin client attachment behavior.
- **Integration coverage:** Cross-document consistency review is the main integration check because the change spans operator guidance, protocol contract, architecture boundaries, and contributor guardrails.
- **Unchanged invariants:** The relay remains live-only, in-memory, and content-opaque; `agentunnel` remains the PTY owner; `/api/updates/ws` remains the preferred live client channel; and this plan does not change delivery semantics in code.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| README wording drifts into protocol-level detail and becomes too heavy | Keep README focused on operator expectations and link to `docs/protocol.md` for exact semantics |
| Protocol wording still leaves room to misread `seq` as end-to-end completeness | Tie the doc text directly to the current implementation boundary: relay assigns `seq` only after accepting and retaining a frame |
| Architecture doc and contributor guidance diverge again after editing | Use Unit 3 as an explicit final harmonization pass across all four docs |
| The future-direction note accidentally reads like an implied roadmap commitment | Keep the note brief and clearly separated from the current contract |

## Documentation / Operational Notes

- No rollout coordination is needed because runtime behavior is unchanged.
- This work should leave the repository in a state where future delivery-guarantee work can be planned explicitly instead of being inferred from ambiguous prose.
- If later work introduces gap signaling, spool-and-replay, or reconnect epoch semantics, `README.md`, `docs/protocol.md`, `docs/architecture.md`, and `CLAUDE.md` should all be revisited together rather than incrementally.

## Sources & References

- **Origin document:** `docs/brainstorms/2026-04-06-relay-output-best-effort-requirements.md`
- Related code: `connector/connector.go`
- Related code: `relay/history.go`
- Related code: `relay/server.go`
- Related code: `protocol/message.go`
