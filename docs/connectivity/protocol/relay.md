# Connectivity Relay Control Plane

## Status

The cross-repository source of truth for Relay connectivity realtime,
rendezvous, fallback token setup, and opaque packet forwarding is:

- `../agent-tunnel-protocols/docs/relay-control-plane.md`
- `../agent-tunnel-protocols/docs/end-to-end-flows.md`

This file is only the `agent-tunnel` implementation pointer. Do not add
cross-repository protocol details here; update the SSOT repository first.

## Local Implementation

Relay protocol types:

- `internal/protocol/connectivity.go`

Relay handlers and registry:

- `internal/relay/handler/connectivity/app_ws.go`
- `internal/relay/handler/connectivity/daemon_ws.go`
- `internal/relay/handler/connectivity/tunnel_ws.go`
- `internal/relay/handler/api/pairing.go`
- `internal/relay/connectivity`

Daemon-side connector:

- `internal/tunnel/daemon/connectivity_connector.go`
- `internal/tunnel/daemon/connectivity_direct.go`

## Local Scope

This repository owns the Go implementation of:

- app realtime registration at `GET /api/connectivity/ws`
- daemon realtime registration at `GET /connectivity/computer/ws`
- live trusted-computer visibility derived from daemon trusted rosters
- pairing response forwarding
- direct rendezvous hint routing
- fallback tunnel token issuance
- `GET /connectivity/tunnel/ws` binary packet forwarding
- cleanup on logout, token revocation, user deletion, daemon disconnect,
  app disconnect, and trusted-client revocation

Relay remains content-opaque for daemon transport payloads. Session roster,
recent output preview, terminal snapshots/live bytes, input, resize, and
interactive authorization belong to the pinned daemon transport.
