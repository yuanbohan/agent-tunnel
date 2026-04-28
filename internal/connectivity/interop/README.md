# Connectivity Interop Spike

This directory records Step 1 interop evidence for the QUIC connectivity
program. The repository-owned automated tests cover the Go side:

- pinned Ed25519 SPKI mutual TLS over `quic-go`
- required ALPN `tunnel-conn/1`
- no 0-RTT API usage in the harness
- bidirectional control stream exchange
- daemon-initiated unidirectional stream exchange
- QUIC over a WebSocket-like packet carrier

The Android `quiche` JNI/device run is a manual hard gate for this step because
the Android production repository is not present in this workspace. Before Step
2 starts, record the Android build target, emulator/device API level, `quiche`
version, pinned TLS result, stream exchange result, and any packaging blockers in
`docs/connectivity/implementation/step-01-interop-spike.md`.
