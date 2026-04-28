# Connectivity Interop Spike

This directory records Step 1 interop evidence for the QUIC connectivity
program. Step 1 uses a Go mobile simulator instead of a real Android client, so
the repository-owned automated tests cover protocol and data-format behavior
without requiring the Android repository, `quiche` JNI packaging, an emulator,
or a physical device:

- pinned Ed25519 SPKI mutual TLS over `quic-go`
- required ALPN `tunnel-conn/1`
- no 0-RTT API usage in the harness
- bidirectional control stream exchange with JSON `hello`, `session_index`,
  `interactive_request`, and `interactive_granted`
- daemon-initiated unidirectional stream exchange with JSON `snapshot_begin`,
  raw `snapshot_chunk`, JSON `snapshot_end`, and raw `live_bytes`
- direct UDP and Relay-like packet-carrier paths
- QUIC over a WebSocket-like packet carrier

FIXME(Android): This directory does not prove that the production Android app
can package `quiche`, install the required certificate verification callback, or
exchange streams on emulator/device. Before claiming Android compatibility,
record the Android build target, emulator/device API level, `quiche` version,
pinned TLS result, stream exchange result, and any packaging blockers in
`docs/connectivity/implementation/step-01-interop-spike.md`.

Run the Step 1 simulator checks with:

```bash
go test ./internal/connectivity/interop
```
