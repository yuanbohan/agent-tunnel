# Connectivity Error Codes

## Status

This document is the single source of truth for error codes used across the connectivity stack. Each code has a stable name, a defined trigger, the side that emits it, the receiver's expected handling, and a recommended user-facing string template.

When adding a new error condition, register it here first, then reference this document from the protocol or implementation doc that triggers it. Do not mint codes ad hoc.

## Conventions

- codes are lowercase ASCII with underscores
- codes are grouped by domain in this document; domain-prefixed names are preferred for new cross-plane codes, but existing short transport reason enums such as `selection_required` and `not_authorized` are stable and valid
- receivers MUST be tolerant of unknown codes and treat them as `*_unknown_error` (see Forward Compatibility)
- the same code string MUST NOT be reused with different meanings across domains; e.g. there is exactly one `selection_required`

## Pairing

Defined in `pairing-protocol.md`. Emitted by daemon to Android (over the pairing response routing path through Relay) or surfaced locally by either side.

| Code | Emitted By | Trigger | Recommended User-Facing String |
|---|---|---|---|
| `pairing_invitation_expired` | daemon | invitation `expires_at` is in the past | "This pairing code has expired. Run `tunnel daemon pair` again." |
| `pairing_invitation_invalid` | Android (local) or daemon | QR could not be parsed; daemon signature verify failed | "Pairing code is invalid. Re-scan or check the daemon CLI." |
| `pairing_invitation_consumed` | daemon | invitation already completed once | "This pairing code has already been used. Mint a new one." |
| `pairing_account_mismatch` | daemon | Android's account does not match invitation's `account_id` | "You're signed in to a different account. Sign in with the matching account." |
| `pairing_relay_unreachable` | Android (local) | Relay could not be reached for response transport | "Could not reach our servers. Check network and retry." |
| `pairing_signature_failed` | daemon | daemon could not verify the Android response signature | "Pairing failed verification. This may indicate tampering. Abort and re-pair." |
| `pairing_sas_mismatch` | local on either side | user reported SAS digits did not match | "Codes did not match. Pairing was aborted for safety." |
| `pairing_unknown_error` | either | catch-all | "Pairing failed. Try again; if it persists, capture diagnostics." |

## Transport (QUIC Control Stream)

Defined in `transport-protocol.md`. Carried in the `error` frame on the control stream, or as `reason` in `interactive_denied`.

| Code | Emitted By | Trigger | Recommended User-Facing String |
|---|---|---|---|
| `protocol_version_mismatch` | either | `hello.protocol_version` differs from local supported version | "App and computer versions are incompatible. Update one or both." |
| `selection_required` | daemon | `interactive_request` or implicit content emit on a session that is not currently selected for the account and activated on this device | "Activate this session first." |
| `not_authorized` | daemon | requesting connection lacks rights to attach this session | "You're not authorized to attach this session." |
| `session_unavailable` | daemon | session no longer exists or is not in an attachable state | "This session is not available." |
| `daemon_busy` | daemon | temporary daemon-side rejection | "Computer is busy. Try again shortly." |
| `transport_unknown_error` | either | catch-all | "Connection error. Try again." |

## Access Token / Selection (Relay-issued, daemon-validated)

Defined in `subscription-model.md`. Returned by Relay over its REST/WebSocket API for lease acquisition or surfaced by the daemon on token validation.

| Code | Emitted By | Trigger | Recommended User-Facing String |
|---|---|---|---|
| `session_limit_reached` | Relay | account has no remaining active-session selection capacity | "You're using the maximum number of sessions on your plan." |
| `selection_required` | daemon | token missing / expired / wrong selection state for this session | "Activate this session first." |
| `access_token_invalid_signature` | daemon | JWT signature verification failed | "Could not verify this session token. Reactivate the session." |
| `access_token_unknown_kid` | daemon | JWT `kid` not in daemon's persisted keyset | "Could not verify this session token. Reactivate the session." |
| `access_token_wrong_audience` | daemon | JWT `aud` does not match this daemon's id | "This session token is not for this computer." |
| `access_token_wrong_device` | daemon | JWT `device` does not match QUIC peer's pinned fingerprint | "This session token is not for this device." |
| `access_token_wrong_session` | daemon | JWT `session` does not match the activating `session_id` | "Wrong session token." |
| `access_token_revoked` | Relay or daemon | token was explicitly invalidated before expiry | "This session is no longer active. Reactivate it if needed." |

## Relay (Control Plane)

Defined in `relay-protocol.md`. Returned by Relay over the realtime WebSocket or its REST surface.

| Code | Emitted By | Trigger | Recommended User-Facing String |
|---|---|---|---|
| `relay_auth_failed` | Relay | account token invalid or expired | "Please sign in again." |
| `relay_account_mismatch` | Relay | actor identity does not match expected | "Account mismatch. Sign out and sign in again." |
| `relay_rate_limited` | Relay | per-account or per-device rate limit exceeded | "Too many requests. Try again in a moment." Includes `retry_after_seconds`. |
| `relay_daemon_offline` | Relay | requested daemon is not currently registered | "Computer is offline." |
| `relay_invalid_payload` | Relay | malformed event payload | (internal; surface as generic error) |
| `relay_unknown_error` | Relay | catch-all | "Server error. Try again." |

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

```
{
  "code": "<error_code>",
  "message": "<short technical description>",
  "retry_after_seconds": <int, optional>,
  "details": { <code-specific fields> }
}
```

For lease limit errors specifically, `details` includes:

```
{
  "current": <int>,
  "max": <int>,
  "active_session_ids": ["<string>"],
  "tier_required": "<string>",
  "upgrade_url": ""
}
```

`upgrade_url` is an empty string in phase 1 because the payment flow is deferred. See `subscription-model.md` § Payment Flow Deferred.

## Related Documents

- `docs/connectivity/pairing-protocol.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/subscription-model.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/state-machines.md`
