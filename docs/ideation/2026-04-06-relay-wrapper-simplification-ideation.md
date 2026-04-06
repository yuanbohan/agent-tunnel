---
date: 2026-04-06
topic: relay-wrapper-simplification
focus: review current implementation against the product goal "relay only does relay, agentunnel is a wrapper for mobile interaction"
---

# Ideation: Relay / Wrapper Simplification

## Codebase Context

- The repo is a small Go codebase with a clean package split: `cmd/agentunnel`, `cmd/relay`, `connector/`, `session/`, `relay/`, `protocol/`, and `launcher/`.
- The intended product boundary is documented clearly in [README.md](../../README.md) and [CLAUDE.md](../../CLAUDE.md): relay is content-opaque and live-only; `agentunnel` owns the PTY and translates remote interaction into PTY input.
- Tests are healthy: `go test ./...` passed during this ideation pass.
- There is no prior ideation artifact in `docs/ideation/` and no institutional learnings in `docs/solutions/`, so this review is grounded directly in current code and docs.
- The current architecture is mostly coherent. The main drift is not “too much code everywhere”; it is concentrated in a few seams:
  - protocol migration is incomplete: legacy raw `input` still exists in [protocol/message.go](../../protocol/message.go), [relay/server.go](../../relay/server.go), and [connector/connector.go](../../connector/connector.go)
  - `agentunnel` claims a mandatory relay dependency in docs, but startup does not wait for successful relay registration in [cmd/agentunnel/main.go](../../cmd/agentunnel/main.go)
  - transport loss/backpressure is partly silent in [connector/connector.go](../../connector/connector.go) and [relay/client_update_ws.go](../../relay/client_update_ws.go)
  - mobile-facing interaction capability is implicit in [session/remote_input.go](../../session/remote_input.go) rather than explicit in session metadata or a protocol contract
  - some naming still reflects an older browser-centric mental model, such as `BrowserUser` / `BrowserPassword` in [cmd/relay/main.go](../../cmd/relay/main.go)
- Highest-leverage improvement areas:
  - finish the protocol simplification
  - make relay/session lifecycle semantics honest
  - make mobile interaction capabilities explicit
  - remove observability blind spots around dropped or ignored traffic

## Ranked Ideas

### 1. Make relay connectivity a true startup invariant, or make degraded mode explicit
**Description:** Today `agentunnel` starts the PTY process immediately and only then launches the connector loop in the background. The docs say relay connectivity is mandatory, but the code only requires relay config, not a successful relay registration. Tighten that contract in one of two ways: either block startup until `/agent/ws` registration succeeds, or add an explicit `--degraded-local-only` mode and treat the current behavior as opt-in rather than silent fallback.
**Rationale:** This is the cleanest alignment with your stated goal. If the wrapper exists so mobile can interact through the relay, then “agent started but invisible remotely” is a broken primary path, not a harmless edge case. The drift is visible between [CLAUDE.md](../../CLAUDE.md) and [cmd/agentunnel/main.go](../../cmd/agentunnel/main.go#L95).
**Downsides:** You need a connection timeout, retry UX, and a decision about what to do when the relay drops after successful startup.
**Confidence:** 92%
**Complexity:** Medium
**Status:** Explored

### 2. Remove the legacy raw `input` path end-to-end
**Description:** Finish the migration to `input_text` and `input_key` by deleting the raw base64 `input` path from the client websocket contract, the forwarded agent message path, and the corresponding compatibility helpers. The recent structured-input plan already moved the design in this direction; this is the cleanup step that finishes it.
**Rationale:** This is the clearest legacy residue in the repo. It keeps protocol branching alive in [protocol/message.go](../../protocol/message.go#L103), keeps extra validation branches in [relay/server.go](../../relay/server.go#L187), and keeps dual handling in [connector/connector.go](../../connector/connector.go#L152). For a mobile-facing wrapper, the structured path is the product.
**Downsides:** It is a compatibility break and requires coordination with any existing client still sending raw PTY bytes.
**Confidence:** 90%
**Complexity:** Medium
**Status:** Unexplored

### 3. Publish interaction capabilities as part of the live session contract
**Description:** Expose what the wrapper actually supports for remote control, instead of forcing clients to infer it from docs or code. At minimum this can include supported key vocabulary, whether structured text input is supported, and possibly whether the local terminal is also attached. The current truth lives implicitly in [session/remote_input.go](../../session/remote_input.go#L9), where unsupported keys are just ignored.
**Rationale:** This is the most product-facing simplification for mobile. Right now the relay says “send `input_key`”, but the supported vocabulary is effectively hidden in implementation. A capability contract would let the mobile UI render only valid controls and avoid silent no-ops.
**Downsides:** It expands the public protocol surface and needs documentation/versioning discipline.
**Confidence:** 85%
**Complexity:** Medium
**Status:** Unexplored

### 4. Make output loss and backpressure visible instead of silent
**Description:** Decide on an explicit transport policy for “relay is slower than PTY output” and “client websocket is slower than relay fanout”. Right now `Connector.WriteOutput` silently drops output when the outbound buffer is full, and the client update sink closes slow readers under backpressure. Keep the policy simple, but make it observable: counters, logs, or an explicit loss event are enough to start.
**Rationale:** If the mobile app is meant to observe and interact with the wrapper, silent output loss is one of the few things that can make the system feel flaky even when tests pass. The risk is grounded in [connector/connector.go](../../connector/connector.go#L68) and [relay/client_update_ws.go](../../relay/client_update_ws.go#L34).
**Downsides:** The wrong fix can harm local responsiveness, so the policy needs to protect the PTY owner first and the remote viewer second.
**Confidence:** 88%
**Complexity:** Medium
**Status:** Unexplored

### 5. Clean up protocol boundaries and stale naming
**Description:** Separate agent-side frames, client-to-relay input frames, relay-to-client update frames, and session metadata more cleanly in the `protocol/` package, and rename stale “browser” terminology to “client” or “viewer”. This is a targeted clarity refactor, not a new abstraction layer.
**Rationale:** The codebase is small enough that naming drift matters. `Message`, `AgentFrame`, `ClientInputMessage`, and `ClientUpdateMessage` are all reasonable individually, but together they still carry migration residue and old mental models. `BrowserUser` / `BrowserPassword` in [cmd/relay/main.go](../../cmd/relay/main.go#L15) is a concrete example of legacy vocabulary that no longer matches “mobile client”.
**Downsides:** Mostly internal cleanup. Useful, but not as directly valuable as fixing product-semantics drift.
**Confidence:** 84%
**Complexity:** Low
**Status:** Unexplored

### 6. Add a short reconnect grace window for live-session continuity
**Description:** Preserve a live session entry and its in-memory frames for a short grace period after the agent websocket drops, so brief relay-network blips do not cause the mobile client to see the session disappear and reappear. The connector already retries every second, but the registry currently drops the session immediately on disconnect.
**Rationale:** This is the strongest “UX leverage” idea that still fits your simple goal. The relay remains a relay, but becomes less brittle for real mobile usage. The current behavior is grounded in [connector/connector.go](../../connector/connector.go#L76) and [relay/registry.go](../../relay/registry.go#L103).
**Downsides:** It weakens the current “socket gone => session gone immediately” invariant and adds a slightly subtler lifecycle model.
**Confidence:** 72%
**Complexity:** Medium
**Status:** Unexplored

## Rejection Summary

| # | Idea | Reason Rejected |
|---|------|-----------------|
| 1 | Add durable persistent history to the relay | Conflicts with the repo’s documented live-only boundary and would widen scope materially. |
| 2 | Add a bundled web frontend to the relay repo | Directly conflicts with the current boundary that the relay is API-only and clients live elsewhere. |
| 3 | Support arbitrary shell commands instead of `claude` / `codex` / `gemini` | Broadens product scope before the current relay-wrapper contract is fully stabilized. |
| 4 | Add a headless or detached `agentunnel` mode now | Plausibly useful, but it changes the product shape more than it simplifies the current implementation. |
| 5 | Replace Basic Auth immediately with a larger auth system | Important eventually, but not the highest-leverage simplification for the current single-user/mobile wrapper goal. |
| 6 | Add session locking or lease-based single-controller control | More policy than necessity right now; no evidence in the codebase that controller contention is today’s pain. |
| 7 | Add a dedicated per-session live websocket instead of the global update stream | Possible refinement, but the current multiplexed stream is coherent and not obviously the main source of complexity. |
| 8 | Break `relay/` into more packages right now | Too abstraction-driven. The package is still small enough that protocol and lifecycle cleanup matter more. |
| 9 | Make retained history size configurable first | Operationally nice, but secondary compared with lifecycle correctness and protocol cleanup. |

## Session Log

- 2026-04-06: Initial ideation — 15 candidates generated, 6 survived.
- 2026-04-06: Brainstormed idea 1 ("Make relay connectivity a true startup invariant, or make degraded mode explicit") and selected a bounded-startup-wait plus background reconnect direction.
