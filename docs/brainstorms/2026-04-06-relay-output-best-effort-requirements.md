---
date: 2026-04-06
topic: relay-output-best-effort
---

# Best-Effort Remote Output Contract

## Problem Frame

`agentunnel` is intended to let a mobile or other external client observe and interact with a locally running terminal agent through the relay. The current implementation already supports that workflow, but the product contract is still underspecified in one important area: remote output continuity.

Today, the local terminal is the primary and most reliable view of the session. The remote path is intentionally lighter-weight. `GET /api/updates/ws` acts as a live stream, and `GET /api/sessions/:id/frames` exposes retained relay-side frames, but neither surface is currently defined as a lossless end-to-end transcript. In particular, relay sequence numbers describe the order of output the relay has accepted and retained; they do not prove that the remote client saw every byte the local PTY produced.

This creates a documentation and product-contract gap:

- the project wants to support real remote interaction and remote work
- the current implementation is best-effort on the remote output path
- the docs do not yet state that limit precisely enough for client authors or operators

This brainstorm defines the current contract clearly without expanding scope into guaranteed delivery, durable history, or new reconnect semantics.

## Requirements

**Remote Output Contract**
- R1. The product definition must explicitly describe `GET /api/updates/ws` as a best-effort live channel for remote session output and interaction.
- R2. The docs must state that remote/mobile clients can observe and interact with a live session and can be used for remote work, but the live remote output path is not yet guaranteed lossless.
- R3. The docs must state that the local terminal remains the primary source of truth for complete session output in the current product revision.
- R4. The docs must state that relay `seq` values describe the order of output frames recorded by the relay, not proof of complete end-to-end delivery from the local PTY to a remote client.

**Replay and Recovery**
- R5. The docs must define `GET /api/sessions/:id/frames` as the standard recovery path for a client that reconnects and wants to recover recent relay-retained output.
- R6. The docs must state that retained frames are live-only, in-memory, bounded relay-side history rather than a durable or complete transcript.
- R7. The docs must state that replay through `/api/sessions/:id/frames` can recover only frames that the relay still retains for that live session.
- R8. The docs must describe the intended client pattern as: reconnect to the live stream, then use retained frames as the standard way to catch up on recent relay-retained output when needed.

**Documentation Alignment**
- R9. `README.md`, `docs/protocol.md`, `docs/architecture.md`, and `CLAUDE.md` / `AGENTS.md` must align on the same remote-output contract and avoid implying lossless live delivery.
- R10. `docs/protocol.md` must define `seq` and retained-frame semantics in a way that matches the current implementation precisely enough for client authors to reason about reconnect and recovery.
- R11. Operator-facing docs must present current best-effort behavior as an intentional present-day boundary, not as an accidental bug or an unstated caveat.
- R12. The docs may note that stronger delivery guarantees are a future direction, but they must not imply that such guarantees already exist.

## Success Criteria

- A reader of `README.md` can understand that remote interaction is supported for real use, but remote live output is currently best-effort.
- A client author reading `docs/protocol.md` can understand what relay `seq` does and does not mean.
- A client author can identify `/api/sessions/:id/frames` as the standard relay-side recovery path after reconnect, while also understanding its limits.
- The core docs no longer suggest that the remote view is a complete or lossless transcript when the implementation does not guarantee that.

## Scope Boundaries

- This work does not add end-to-end lossless delivery guarantees.
- This work does not add gap signaling or degraded-stream events.
- This work does not add durable relay-side history.
- This work does not define new reconnect epoch or session-incarnation semantics.
- This work does not change the live-only, content-opaque shape of the relay.

## Key Decisions

- Best-effort is the current product contract: The remote live channel is intentionally acceptable for real remote work, but it is not yet promised as lossless.
- Relay `seq` is relay-local ordering metadata: It is useful for replay and client catch-up, but it is not an integrity proof for all PTY output.
- Retained frames are the standard recovery path: Clients should treat `/api/sessions/:id/frames` as the normal relay-side catch-up mechanism after reconnect.
- Stronger continuity guarantees are deferred: Future work may improve delivery guarantees, but this phase only clarifies the current contract and aligns the docs with reality.

## Dependencies / Assumptions

- The current implementation continues to keep local work primary and relay state live-only.
- Current client behavior is expected to tolerate best-effort live streaming and reconnect-driven recovery from retained frames.

## Outstanding Questions

### Deferred to Planning
- [Affects R8][Technical] What exact client-facing wording best communicates the standard reconnect and replay flow without over-promising continuity?
- [Affects R9][Technical] How should the same contract be distributed across `README.md`, `docs/protocol.md`, `docs/architecture.md`, and `CLAUDE.md` to avoid duplication while keeping each document useful?
- [Affects R12][Technical] Where should future stronger-delivery work be noted so readers can distinguish current guarantees from future goals?

## Next Steps

→ `/prompts:ce-plan` for structured implementation planning
