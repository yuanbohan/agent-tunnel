# Connectivity Pairing Protocol

## Status

The cross-repository source of truth for pairing is:

- `../agent-tunnel-protocols/docs/pairing.md`
- `../agent-tunnel-protocols/docs/end-to-end-flows.md`

This file is only the `agent-tunnel` implementation pointer. Do not add
cross-repository protocol details here; update the SSOT repository first.

## Local Implementation

Go implementation entry points:

- `internal/connectivity/pairing`
- `internal/tunnel/daemon/pairing_state.go`
- `internal/tunnel/daemon/connectivity_connector.go`
- `internal/relay/handler/api/pairing.go`
- `internal/relay/handler/connectivity/daemon_ws.go`
- `internal/relay/connectivity`

CLI surfaces:

- `tunnel pair`
- `tunnel pair --json`
- `tunnel pair devices`
- `tunnel pair revoke <fingerprint>`

Local daemon state:

- daemon Ed25519 connectivity identity
- invitation records
- pending pairing responses
- trusted Android client roster
- revoked Android clients

## Compatibility Notes

Current implementation mirrors protocol version `2`:

- Ed25519 invitation and Android response signatures
- `SHA-256(raw_ed25519_public_key)` fingerprints
- 6-digit SAS from the canonical transcript
- Relay-assisted response forwarding without Relay becoming a trust root
- daemon-local trust completion after SAS confirmation

Any change to canonical fields, signature domains, SAS inputs, invitation TTL,
trust completion, or revocation semantics must update
`agent-tunnel-protocols/docs/pairing.md` in the same cross-repo change.
