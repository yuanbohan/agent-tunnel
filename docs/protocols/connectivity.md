# Connectivity Protocol Provenance

## Status

This document maps this repository's connectivity protocol mirrors to the
cross-repository source of truth in
[yuanbohan/agent-tunnel-protocols](https://github.com/yuanbohan/agent-tunnel-protocols).
It is a provenance map, not a replacement protocol specification.

Use this file when changing Go protocol constants, payload structs, local
protocol docs, or compatibility tests so reviewers can see which external
Markdown document owns the protocol decision.

## Active SSOT Mappings

| Surface | Cross-repo SSOT | Local docs | Local Go mirrors/tests | Local scope |
|---|---|---|---|---|
| Daemon-to-mobile QUIC transport, stream model, frame registry, JSON payload families, `ProtocolVersion`, transport security invariants | `agent-tunnel-protocols:docs/protocol.md` | `docs/connectivity/protocol/transport.md`, `docs/connectivity/contract.md` | `internal/connectivity/frame`, `internal/connectivity/sessionproto`, `internal/connectivity/transport`, `internal/connectivity/interop`, `internal/tunnel/daemon/connectivity_transport.go`, `internal/tunnel/daemon/connectivity_transport_test.go` | Implementation mirror and Go compatibility tests |
| Mobile-visible session metadata and launch convergence semantics | `agent-tunnel-protocols:docs/protocol.md` | `docs/connectivity/protocol/transport.md`, selected mobile-visible parts of `docs/connectivity/protocol/local-broker.md` | `internal/connectivity/sessionproto`, daemon broker/session registration tests | Mirror only the mobile-visible metadata contract; keep broker mechanics repo-local |

## Gated Or Deferred Surfaces

| Surface | Status | Local docs/tests | Notes |
|---|---|---|---|
| Relay connectivity realtime control plane | Gated | `docs/connectivity/protocol/relay.md`, `internal/protocol/connectivity.go`, `internal/protocol/connectivity_test.go`, `internal/relay/connectivity`, `internal/relay/handler/connectivity_ws_test.go` | Include in issue #134 only if the protocols PR explicitly adds `agent-tunnel-protocols:docs/connectivity/relay.md`. Relay remains auth, pairing transport, presence, rendezvous, fallback setup, and opaque packet forwarding only. |
| Pairing protocol | Deferred | `docs/connectivity/protocol/pairing.md`, `internal/connectivity/pairing`, `internal/connectivity/pairtest` | Pairing is security-sensitive and should get a focused SSOT slice rather than being folded into daemon transport mirror work. |
| Local daemon broker mechanics | Repo-local | `docs/connectivity/protocol/local-broker.md`, `internal/tunnel/daemon/broker.go`, `internal/tunnel/daemon/broker_test.go` | Cross-repo SSOT owns only mobile-visible metadata/convergence semantics. Local socket lifecycle, broker ownership, and process mechanics remain implementation details of this repository. |

## Compatibility Rules Mirrored Locally

- `sessionproto.ProtocolVersion` mirrors the daemon transport protocol version
  in `agent-tunnel-protocols:docs/protocol.md`.
- Protocol version `2` means JSON payloads for typed control frames. CBOR or
  another non-JSON encoding requires a new protocol version or compatibility
  line decision.
- `frame.Type*` constants mirror the daemon transport frame type registry.
- `payload_length` uses QUIC variable-length integer encoding, and each frame
  payload is capped at 1 MiB (`1 << 20`).
- Known JSON payloads ignore unknown fields for forward compatibility.
- Production receive loops tolerate unknown frame types by dropping or ignoring
  them.
- `snapshot_begin`, `snapshot_chunk`, `snapshot_end`, and `live_bytes` travel
  on the daemon-initiated interactive stream announced by
  `interactive_granted.interactive_stream_id`.
- `SessionMetadata` must stay content-light: no terminal bytes, no preview text
  payloads, no account policy fields, no Relay-only launch correlation fields,
  and no transport path authority fields.

## Fixture Hygiene

If this repository later consumes or mirrors fixtures from
`agent-tunnel-protocols`, those fixtures must be synthetic and non-secret. Do
not commit real credentials, private keys, tunnel tokens, device fingerprints,
terminal captures, private paths, or user input.
