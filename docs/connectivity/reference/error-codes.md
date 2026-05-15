# Connectivity Error Codes

## Status

This document is this repository's structured error-code catalog for the connectivity stack. Each code has a stable name, a defined trigger, the side that emits it, and a recommended user-facing string template.

When adding a new error condition, register it here first, then reference this document from the protocol or implementation doc that triggers it. Do not mint codes ad hoc.

## Conventions

- codes are lowercase ASCII with underscores
- codes are grouped by domain in this document
- receivers MUST be tolerant of unknown codes and treat them as `*_unknown_error` (see Forward Compatibility)
- the same code string MUST NOT be reused with different meanings across domains

## Pairing

Defined in `pairing-protocol.md`. Emitted by daemon to Android (over the pairing response routing path through Relay) or surfaced locally by either side.

| Code | Emitted By | Trigger | Recommended User-Facing String |
|---|---|---|---|
| `pairing_invitation_expired` | daemon | invitation `expires_at` is in the past | "This pairing code has expired. Run `tunnel pair` again." |
| `pairing_invitation_invalid` | Android (local) or daemon | invitation payload could not be parsed; daemon signature verify failed | "Pairing code is invalid. Re-import or check the daemon CLI." |
| `pairing_invitation_consumed` | daemon | invitation already completed once | "This pairing code has already been used. Mint a new one." |
| `pairing_account_mismatch` | daemon | Relay-asserted Android account does not match invitation `account_id` | "You're signed in to a different account. Sign in with the matching account." |
| `pairing_relay_unreachable` | Android (local) | Relay could not be reached for response transport | "Could not reach our servers. Check network and retry." |
| `pairing_signature_failed` | daemon | daemon could not verify the Android response signature | "Pairing failed verification. This may indicate tampering. Abort and re-pair." |
| `pairing_sas_mismatch` | local on either side | user reported SAS digits did not match | "Codes did not match. Pairing was aborted for safety." |
| `pairing_unknown_error` | either | catch-all | "Pairing failed. Try again; if it persists, capture diagnostics." |

## Transport (QUIC Control Stream)

Defined in `transport-protocol.md`. Carried in the `error` frame on the control stream, or as `reason` in `interactive_denied`.

| Code | Emitted By | Trigger | Recommended User-Facing String |
|---|---|---|---|
| `protocol_version_mismatch` | either | `hello.protocol_version` differs from local supported version | "App and computer versions are incompatible. Update one or both." |
| `invalid_frame` | either | frame type, length, or stream placement is invalid for the current connection state | "Connection protocol error. Try again." |
| `invalid_payload` | either | JSON payload cannot be decoded for the frame type, or required fields are missing | "Connection protocol error. Try again." |
| `device_not_trusted` | daemon | requesting device is no longer paired / trusted by the daemon | "This device is no longer trusted by the computer." |
| `session_unavailable` | daemon | session no longer exists or is not in an attachable state | "This session is not available." |
| `daemon_busy` | daemon | temporary daemon-side rejection | "Computer is busy. Try again shortly." |
| `transport_unknown_error` | either | catch-all | "Connection error. Try again." |

## Relay (Control Plane)

Defined in `relay-protocol.md`. Returned by Relay over the realtime WebSocket or its REST surface.

| Code | Emitted By | Trigger | Recommended User-Facing String |
|---|---|---|---|
| `relay_auth_failed` | Relay | account token invalid or expired | "Please sign in again." |
| `relay_account_mismatch` | Relay | actor identity does not match expected | "Account mismatch. Sign out and sign in again." |
| `invalid_client_fingerprint` | Relay | app login/refresh or connectivity websocket used a missing or malformed required client fingerprint | "Client identity is invalid. Sign in again." |
| `pairing_correlation_not_found` | Relay | app submitted a pairing response for an unknown, expired, or cross-account correlation | "Pairing code expired. Run pairing again." |
| `rendezvous_unavailable` | Relay | direct rendezvous requested an unpaired, offline, expired, superseded, malformed, wrong-account, or otherwise unavailable daemon/attempt | "Could not try direct connection. Falling back." |
| `relay_tunnel_unavailable` | Relay | fallback tunnel setup requested an unpaired, offline, wrong-account, or otherwise unavailable daemon/attempt | "Could not open relay fallback. Try again." |
| `relay_tunnel_token_invalid` | Relay | fallback tunnel websocket used a missing, expired, reused, or actor-mismatched one-time token | "Could not open relay fallback. Try again." |
| `interactive_not_granted` | daemon transport | input or resize arrived before an `interactive_granted` lifetime for that session on the current QUIC connection | "Reconnect to the session and try again." |
| `relay_rate_limited` | Relay | per-account or per-device rate limit exceeded | "Too many requests. Try again in a moment." Includes `retry_after_seconds`. |
| `relay_daemon_offline` | Relay | requested daemon is not currently registered | "Computer is offline." |
| `relay_invalid_payload` | Relay | malformed event payload | (internal; surface as generic error) |
| `relay_unknown_error` | Relay | catch-all | "Server error. Try again." |

## Official-App Policy (Local Only)

These are not daemon or Relay protocol errors. They are local product-rule reasons surfaced by the official app.

| Code | Emitted By | Trigger | Recommended User-Facing String |
|---|---|---|---|
| `policy_computer_limit_reached` | official app | Pro user already has 10 trusted computers and attempts to pair another | "Remove a computer before pairing a new one." |
| `policy_replace_computer_failed` | official app | Free Replace Computer pairing failed, was canceled, or SAS mismatched | "Computer replacement was canceled. Your previous computer is still active." |
| `policy_downgrade_resolution_required` | official app | account tier is Free while local state has multiple trusted computers | "Choose one computer to keep using on Free." |

## QUIC / TLS Layer

These codes are emitted by the QUIC stack itself and propagated by the connection manager. Most users will not see them; they are logged for diagnostics.

| Code | Emitted By | Trigger |
|---|---|---|
| `quic_handshake_timeout` | either | QUIC/TLS handshake did not complete within deadline |
| `quic_alpn_mismatch` | either | peer did not advertise `tunnel-conn/1` ALPN |
| `quic_cert_pinning_failed` | either | peer cert SPKI did not match the pinned device fingerprint |
| `quic_idle_timeout` | either | no traffic within `max_idle_timeout` |

User-facing strings for QUIC errors typically collapse to "Could not connect securely. Try again." with diagnostic details only in app logs.

## Forward Compatibility

Phase-1 rule for unknown codes:

- receivers MUST accept and process the rest of the message even if the `code` value is unknown
- receivers SHOULD log the unknown code at info level
- receivers SHOULD render a generic "Something went wrong" string rather than crashing or displaying the raw code

This allows new codes to be added in the same major protocol version without breaking older clients or daemons.

## Error Envelope Shape

Where structured errors are returned (e.g., Relay REST or realtime responses), the shape is:

```json
{
  "code": "<error_code>",
  "message": "<short technical description>",
  "retry_after_seconds": 0,
  "details": {}
}
```

`retry_after_seconds` and `details` are optional and appear only when relevant to the specific error.

## Related Documents

- `../protocol/pairing.md`
- `../protocol/transport.md`
- `../protocol/relay.md`
- `../ux/subscription.md`
- `state-machines.md`
