# Agent Tunnel Relay And Connectivity Implementation Map

## Status

This repository implements the Go tunnel, daemon, Relay, and STUN services.
Cross-repository protocol decisions live in
`../agent-tunnel-protocols`.

Use these SSOT documents for protocol facts:

- `../agent-tunnel-protocols/docs/end-to-end-flows.md`
- `../agent-tunnel-protocols/docs/api.md`
- `../agent-tunnel-protocols/docs/architecture.md`
- `../agent-tunnel-protocols/docs/draws/README.md`
- `../agent-tunnel-protocols/docs/pairing.md`
- `../agent-tunnel-protocols/docs/relay-control-plane.md`
- `../agent-tunnel-protocols/docs/protocol.md`

Endpoint-level request/response examples, auth requirements, public Relay API
error contracts, and system architecture now live in the protocols SSOT. Local
[api.md](api.md) and [architecture.md](architecture.md) are pointers only.

## Current Product Boundary

- `tunnel run` owns the PTY and terminal mirror for one local session.
- The local daemon broker owns live local session metadata, recent preview,
  terminal snapshots, and live output for trusted daemon transports.
- Relay owns auth, account policy, online computer discovery, computer launch
  routing, pairing transport, daemon presence, direct rendezvous, fallback
  setup, and opaque fallback packet forwarding.
- The official mobile companion receives session rows, recent output preview,
  terminal snapshots/live bytes, input, release, and session detail through the
  pinned daemon transport, not Relay session APIs.
- Relay state is live-only except for persisted auth/operator data in
  PostgreSQL.
- Relay fallback carries encrypted QUIC packets only and must not parse
  terminal/session semantics.

## Endpoint Families

App HTTP/API:

- auth: `POST /api/auth/login`, `/api/auth/register`, `/api/auth/refresh`,
  `/api/auth/logout`, `/api/auth/password/change`
- account policy: `GET /api/account/policy`
- agent tokens: `GET /api/agent-tokens`, `POST /api/agent-tokens`,
  `DELETE /api/agent-tokens/:id`
- online computers: `GET /api/computers`
- launch on one online computer: `POST /api/computers/:computerID/sessions`
- pairing response submit: `POST /api/pairing/responses`

Realtime:

- app connectivity: `GET /api/connectivity/ws`
- daemon connectivity: `GET /connectivity/computer/ws`
- agent session ownership and launch readiness: `GET /agent/ws`
- daemon launch routing: `GET /device/ws`
- fallback packet tunnel: `GET /connectivity/tunnel/ws`

Removed from the current product contract:

- Relay-owned session list/stop/attach/frame routes
- `/api/devices` compatibility aliases
- old connectivity realtime aliases
- Relay transcript replay or terminal byte stream APIs

## Implementation Entry Points

- Relay public API pointer: `docs/api.md`
- Connectivity provenance map: `docs/protocols/connectivity.md`
- Connectivity implementation contract: `docs/connectivity/contract.md`
- Pairing pointer: `docs/connectivity/protocol/pairing.md`
- Relay control-plane pointer: `docs/connectivity/protocol/relay.md`
- Daemon transport pointer: `docs/connectivity/protocol/transport.md`
- Local broker mechanics: `docs/connectivity/protocol/local-broker.md`
- Daemon behavior: `docs/daemon.md`
- System architecture pointer: `docs/architecture.md`

Keep this file as an implementation map. Do not reintroduce detailed
cross-repository protocol mirrors here.
