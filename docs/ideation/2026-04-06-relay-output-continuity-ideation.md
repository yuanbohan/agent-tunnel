---
date: 2026-04-06
topic: relay-output-continuity
focus: relay output continuity, best-effort live channel contract, docs/implementation alignment
---

# Ideation: Relay Output Continuity

## Codebase Context

The current codebase is already strongly aligned around a minimal product shape: `agentunnel` owns the local PTY and keeps local work primary, while the relay is a live-only, content-opaque broker for remote observation and input. `README.md`, `docs/architecture.md`, `docs/protocol.md`, and `CLAUDE.md` broadly match the implementation on startup gating, background reconnect, structured remote input, global live updates over `GET /api/updates/ws`, and retained in-memory frame replay through `GET /api/sessions/:id/frames`.

The highest-value gap is not a broad product mismatch but a stability-contract mismatch. In `connector/connector.go`, relay-bound output is currently dropped silently when the outbound queue is full. That preserves local interactivity, but it means the remote/mobile view can lose output without any explicit protocol signal. At the same time, the relay already treats slow clients as best-effort live consumers and disconnects them under backpressure, which is a good fit for a stable minimal relay if the contract is made explicit and the recovery path is documented. A second protocol ambiguity remains around reconnects: the same `session_id` can reappear after reconnect, but the protocol does not explicitly define whether that is continuity of the same live stream or a new live epoch.

## Ranked Ideas

### 1. Loss-Aware Output Continuity for a Best-Effort Live Channel
**Description:** Preserve the current product stance that `/api/updates/ws` is a best-effort live stream, but make output loss and recovery semantics explicit. The system should either recover bounded missed output after reconnect or explicitly signal to clients when live continuity was broken, instead of silently dropping remote-visible output.
**Rationale:** This is the strongest improvement because it directly sharpens the product into what the user wants: a minimal but highly stable relay and wrapper. The local terminal remains primary and non-blocking, while remote clients get a trustworthy contract rather than an implicitly lossy one.
**Downsides:** This introduces protocol and product semantics that must be documented carefully. Even the minimal version adds some state and recovery rules.
**Confidence:** 96%
**Complexity:** Medium
**Status:** Explored

### 2. Session Epoch / Incarnation Semantics
**Description:** Add an explicit per-registration epoch so clients can tell whether a reappearing `session_id` is continuity of the same live stream or a newly registered live incarnation after disconnect.
**Rationale:** This removes ambiguity for mobile replay and reconnect logic, especially when retained history is live-only and disappears with the owning websocket.
**Downsides:** It expands the relay-facing contract and client state model.
**Confidence:** 92%
**Complexity:** Medium
**Status:** Unexplored

## Rejection Summary

| # | Idea | Reason Rejected |
|---|------|-----------------|
| 1 | Standalone slow-client recovery contract doc update | Merged into Idea 1 because the documentation contract is part of the value, not a separate product direction |
| 2 | Single active writer lease for remote input | Explicitly deprioritized by the user for this ideation pass |
| 3 | Public deployment boundary and TLS/reverse-proxy guidance | Explicitly deprioritized by the user for this ideation pass |
| 4 | Durable relay-side session persistence | Exceeds the current live-only, minimal relay boundary |
| 5 | Content-derived previews or semantic terminal parsing | Violates the content-opaque relay boundary |
| 6 | Bundled web frontend | Violates the API-only relay boundary and adds carrying cost unrelated to core reliability |

## Session Log
- 2026-04-06: Initial ideation from current code and docs audit; identified strong implementation/document alignment overall, with output continuity and reconnect semantics as the main remaining leverage points
- 2026-04-06: User selected Idea 1 for brainstorming, explicitly deprioritized remote writer leases and deployment-boundary work, and folded the best-effort live channel contract into the selected idea
- 2026-04-06: Brainstormed current-state best-effort remote output contract; decided not to add gap signaling, delivery guarantees, or reconnect epoch semantics in this phase
