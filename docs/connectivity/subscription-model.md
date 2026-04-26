# Connectivity Subscription Model

## Status

This document captures the target phase-1 subscription and entitlement model for the QUIC session-connectivity architecture.

It exists to keep subscription enforcement simple, Relay-owned, and independent from pairing and transport security.

## Product Direction

Phase 1 has only two product tiers:

- `free`
- `pro`

There are no multiple paid packages in phase 1.

The product promise is:

- security is identical on free and pro
- `tunnel` on the computer is not limited by subscription
- the difference is how many sessions the mobile app account may actively use at the same time

## Core Rule

Subscription is enforced through one resource only:

- **account-global active session selections**

Relay is the source of truth for these selections.

The daemon does not know whether the account is free or pro. It only knows whether the Android device has presented a valid Relay-issued access token for a specific session that is currently selected for that account.

## What Counts As An Active Session

An active session is a session that the account has currently selected for real content delivery.

That means:

- the account may receive daemon-generated preview content for it
- the account may request interactive terminal access for it
- one or more devices on the same account may receive real content for it after presenting device-scoped access tokens

That does not include:

- the session merely existing on the daemon
- the session appearing in `session_index`
- daemon presence
- the local `tunnel run` process continuing to execute

## Tier Limits

Phase-1 entitlement limits:

| Limit | Free | Pro |
|---|---|---|
| `max_active_selected_sessions` | 1 | more than 1 |

The architectural contract only requires that:

- free has exactly one selected session at a time
- pro has multiple selected sessions

Phase-1 recommended operational values:

- free: `1`
- pro hard cap: `10`

The `10` limit exists as an abuse-protection ceiling, not as a user-facing product message. For normal individual paid usage it should feel effectively unlimited.

Direct and relay do not change this count. Subscription does not care which path a selected session currently uses.

## What Free Users Can See

Free users may still see all discovered sessions in the mobile UI.

For every visible session, the app may render:

- `label`
- `command_preview`
- `cwd`
- `git_branch`
- basic online / offline metadata

But only the currently selected session may render or receive:

- real daemon preview text
- interactive attach grant
- terminal snapshots
- live bytes
- input / resize authority for that session

This preserves a clear product difference:

- free users can understand what exists
- only one session is truly usable at a time across the whole account

## Locked Session UI

Sessions that are visible but not currently selected under the account's subscription should be rendered as locked.

Phase-1 UI guidance:

- use a lock icon on the session row
- apply light greyed-out styling to distinguish it from the selected session
- keep metadata readable; do not hide the row entirely
- do not show real preview content for locked sessions

When the user taps a locked session, the app should explain:

- free allows only one active session at a time
- which session is currently active
- upgrading unlocks more simultaneous sessions

Phase 1 should prefer a simple modal or bottom sheet that:

- always explains which session is currently active
- may offer `Replace current session` as an explicit destructive action
- may offer `Upgrade`
- never silently switches the selected session

## Account-Global Selection And Device-Scoped Access Tokens

The architecture intentionally separates:

1. **selection**
2. **access**

Selection is account-global and Relay-owned.

Access is device-scoped and daemon-enforced.

Relay persists the currently selected session set as:

- `account_id`
- `daemon_id`
- `session_id`
- `selection_epoch`
- `changed_by_device_fingerprint`
- `updated_at`

Relay also issues a short-lived signed access token to Android devices for selected sessions.

The daemon validates:

- signature
- session binding
- daemon binding
- device binding
- selection epoch binding
- revocation state for this `jti`
- expiry

The daemon does not need to know:

- free vs pro
- billing state
- why the token was granted

This keeps subscription logic off the daemon while still making the limit enforceable on direct connections.

## Access Token Format

Phase 1 fixes the access-token wire format so that daemon (Go) and Relay implementations cannot drift.

- container: **JWT** (RFC 7519 Compact Serialization)
- signature algorithm: **EdDSA** with **Ed25519** keys
- header: `{ "alg": "EdDSA", "kid": "<key_id>", "typ": "JWT" }`
- payload (claims):

| Claim | Type | Meaning |
|---|---|---|
| `iss` | string | issuer, fixed to `"relay"` |
| `sub` | string | account id |
| `aud` | string | daemon id this token targets |
| `device` | string | base64 of the Android device public-key fingerprint |
| `session` | string | session id this token activates |
| `selection_epoch` | integer | current Relay-owned selection epoch for this account/session |
| `iat` | integer | Unix seconds, issued at |
| `exp` | integer | Unix seconds, expires at |
| `jti` | string | unique token id; renewals reuse the same `jti` for this `(device, session, selection_epoch)` access lineage |

Daemon validation order:

1. parse header; reject if `alg != "EdDSA"`
2. look up the public key by `kid`; reject if unknown
3. verify the Ed25519 signature
4. reject if `aud` does not equal this daemon's id
5. reject if `device` does not equal the QUIC-presented Android cert SPKI fingerprint
6. reject if `session` does not match the activating `session_id`
7. reject if this daemon has already recorded the token's `jti` as revoked
8. accept if `now <= exp`; otherwise reject with `selection_required`

The access-token TTL is the phase-1 short outage-tolerance window. There is no daemon-side grace period beyond `exp`.

## Worked Example

Header:

```
{ "alg": "EdDSA", "kid": "k-2026-04", "typ": "JWT" }
```

Payload:

```
{
  "iss": "relay",
  "sub": "acc_01HZX...",
  "aud": "dmn_01HZ...",
  "device": "Hk7zP...",
  "session": "sess_01HZ...",
  "selection_epoch": 42,
  "iat": 1745673600,
  "exp": 1745673780,
  "jti": "tok_01HZX..."
}
```

The signing input and signature follow standard JWT compact serialization: `base64url(header) + "." + base64url(payload) + "." + base64url(signature)`.

## Token Timing

Phase-1 defaults:

- token TTL: `3 minutes`
- renewal cadence while actively in use: every `45 seconds`

This means:

- short Relay hiccups are tolerated as long as the current token has not yet expired
- once `now > exp`, the daemon stops sending real preview, terminates active interactive streams, and emits an `interactive_denied` with `reason = selection_required` for any further attempts
- Relay must successfully renew the token before `exp`; there is no additional daemon-side safety window after expiry

## Renewal Token Overlap

Renewal returns a fresh JWT with a strictly later `exp` and the same `jti`. There is no atomic-swap requirement on either side.

Phase-1 rule:

- the daemon accepts any presented token whose `(jti, signature, device, session, selection_epoch, aud)` validate, whose `jti` is not marked revoked locally, and whose `exp > now`
- this allows the new and old token to be valid for the few seconds during which Android is presenting the new one but the daemon has not seen it yet
- Android SHOULD start presenting the renewed token to the daemon within 1 second of receiving it from Relay

This avoids any "renewal race" where an unlucky packet ordering could blip the access state.

## Selection Acquisition

The intended phase-1 flow is:

1. Android receives `session_index` from the daemon
2. user explicitly chooses one session to use
3. Android asks Relay to select that `session_id` for the account
4. Relay checks whether the account still has capacity for another selected session
5. if allowed, Relay records the new selection state, increments `selection_epoch`, and returns a device-scoped access token for this Android device
6. Relay broadcasts the updated selection state to all online app devices on that account
7. Android presents the token to the daemon via `session_activate`
8. daemon starts sending real preview and may later honor `interactive_request` for that session

If Relay denies the selection, the daemon must not begin real content delivery for that session.

## Selection Persistence And App Kill Behavior

To prevent easy abuse, the selected session is not tied directly to the Android app process lifetime.

Phase-1 rules:

- the selected session remains selected until one of:
  - explicit user release
  - explicit user replacement with another session
  - the selected session ends
  - logout or account switch
- access tokens still use the short-lived `3 minute` TTL and `45 second` renewal cadence
- if the app is backgrounded or killed, the selected session remains selected for the account
- when the same or another device on the same account returns, it may request a fresh token for the already-selected session
- a different session may not become selected on free tier unless the current selection is explicitly released or explicitly replaced through Relay

This prevents the simple "kill app and immediately grab another session" loophole while keeping the product rule easy to explain.

## Selection Change On Free Tier

On free tier, the user does not switch sessions by simply tapping another locked row.

Changing the selected session must be an explicit action.

That action may be:

- explicit release of the current selected session, followed by selection of another
- explicit "replace current session with this one" confirmation in the locked-session modal

Relay is authoritative for the change:

- it updates the account-global selected session
- it increments `selection_epoch`
- it revokes device-scoped tokens for the old selection
- it broadcasts the new selection state to all app devices on the account

This ensures that all phones converge to the same answer about which session is currently usable.

## Selection Propagation

Selection changes must take effect immediately, including on direct connections where the daemon is not consulting Relay on every frame.

Phase-1 rule:

- Relay is the source of truth for selection changes
- when the selected session is released, replaced, invalidated by logout or account switch, or displaced by a later Relay decision, Relay emits:
  - an app-side `active_session_selection_changed` event carrying:
    - `account_id`
    - `old_session_id`
    - `new_session_id`
    - `daemon_id`
    - `selection_epoch`
    - `changed_by_device_fingerprint`
    - `changed_at`
  - a daemon-side `access_token_revoked` event carrying:
    - `jti`
    - `device_fingerprint`
    - `daemon_id`
    - `session_id`
    - `reason`
    - `revoked_at`
- the daemon records that `jti` in a local revocation cache until the token's original `exp`
- once a `jti` is in that revocation cache, the daemon MUST:
  - stop sending real preview for that device/session token lineage
  - terminate any interactive streams associated with that token lineage
  - reject any future reuse of that token, even if `exp` has not yet passed

This is what makes account-global selection authoritative instead of "best effort".

## What Subscription Does Not Touch

Subscription state MUST NOT influence:

- pairing trust validation
- SAS confirmation
- the QUIC handshake
- transport keys
- direct vs relay path selection
- daemon-local session existence

The only things subscription changes are:

- which sessions are currently selected for the account
- whether Relay issues or renews device-scoped access tokens for those selected sessions

## Lapsed Subscription Behavior

If a pro subscription lapses, the account degrades to free-tier selection behavior.

That means:

- existing pairing trust remains intact
- daemon presence remains visible
- direct and relay transport still follow the same security model
- the account may keep at most one selected session after degradation
- existing extra selected sessions may drain only until their current tokens expire; Relay MUST refuse further token renewal beyond the new free-tier limit and converge the account back to one selected session

This avoids abrupt breakage while keeping the free/pro boundary clear.

## Error Contract

When Relay refuses a selection or token issuance request, it should return a structured error envelope:

```
{
  "code": "session_limit_reached",
  "current": 1,
  "max": 1,
  "active_session_ids": ["sess_123"],
  "tier_required": "pro",
  "upgrade_url": ""
}
```

Android should render a human-readable explanation rather than showing raw enum values.

In phase 1, `upgrade_url` is intentionally an empty string. The payment flow is deferred. The app should still render the modal explaining the limit and naming the required tier; it just does not yet expose a working upgrade link.

## Abuse Protection

The active-session selection limit is the user-facing product control, not the only operational defense.

Relay should also apply internal rate limits that are not surfaced as subscription plan rules, for example:

- selection change rate per account
- access-token request rate per account
- access-token request rate per device fingerprint
- fallback tunnel open rate per account
- reconnect burst limits per device fingerprint

These controls exist to limit abuse and accidental retry storms. They should not complicate the free/pro product explanation.

## Payment Flow Deferred

Phase 1 ships without a working payment / upgrade flow.

Open decisions:

- whether to use Stripe (web-only), Google Play Billing (mandatory for in-app digital goods on Android per Play policy), or both
- where the upgrade UI lives — entirely on a web property, in the Android app, or both
- which renewal cadence and pricing structure ship first

Phase-1 expectation:

- `tier` defaults to `free` for every account
- `upgrade_url` is an empty string in error envelopes
- Android UI surfaces the upgrade prompt with text only, no clickable link
- Relay code paths for entitlement enforcement are still wired up so they can be enabled by configuration later

This avoids regulatory and Play-policy entanglement before product-market fit. When the payment flow is added, only Relay configuration and Android UI need to change; protocol does not.

## Why This Model Was Chosen

This is intentionally simpler than earlier drafts that considered:

- device-owned leases
- paired-daemon count limits
- paired-device count limits
- relay-minute quotas
- separate direct and relay policies

Those models were rejected for phase 1 because they increase user confusion and spread entitlement logic across too many parts of the system.

The chosen model maps cleanly to the actual product story:

- free can fully use one session across the whole account
- all devices on that account see the same active session choice
- pro can fully use more sessions
- security does not change

## References

- `docs/connectivity/architecture.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/daemon-session-sync.md`
- `docs/connectivity/mobile-reference.md`
- `docs/connectivity/android-client-behavior.md`
