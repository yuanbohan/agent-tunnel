# Daemon Transport Protocol

## Status

The cross-repository source of truth for the daemon-to-mobile QUIC session
transport is:

- `../agent-tunnel-protocols/docs/protocol.md`
- `../agent-tunnel-protocols/docs/end-to-end-flows.md`

This file is only the `agent-tunnel` implementation pointer. Do not add
cross-repository frame registry, payload, stream, direct/relay carrier, or
transport security details here; update the SSOT repository first.

## Local Implementation

Frame and payload mirrors:

- `internal/connectivity/frame`
- `internal/connectivity/sessionproto`

QUIC/TLS and peer pinning:

- `internal/connectivity/transport`
- `internal/connectivity/identity`

Daemon session transport:

- `internal/tunnel/daemon/connectivity_transport.go`
- `internal/tunnel/daemon/connectivity_transport_test.go`

Direct and fallback packet carriers:

- `internal/tunnel/daemon/connectivity_direct.go`
- `internal/connectivity/direct`
- `internal/connectivity/carrier`

## Compatibility Notes

Current implementation mirrors daemon transport protocol version `2`:

- QUIC with TLS 1.3
- self-signed Ed25519 endpoint certificates
- paired public-key pinning
- ALPN `tunnel-conn/1`
- 0-RTT disabled
- one mobile-opened bidirectional control stream
- daemon-initiated unidirectional interactive streams
- `[1-byte type][QUIC varint payload_length][payload bytes]`
- JSON typed payloads and raw terminal byte frames
- 1 MiB frame payload cap

Any change to protocol version, frame type values, payload fields, stream
roles, transport security invariants, path/carrier semantics, or session
metadata boundaries must update `agent-tunnel-protocols/docs/protocol.md` in
the same cross-repo change.
