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
- the difference is how many sessions the mobile app may actively use at the same time

## Core Rule

Subscription is enforced through one resource only:

- **active mobile session leases**

Relay is the source of truth for these leases.

The daemon does not know whether the account is free or pro. It only knows whether the Android device has presented a valid Relay-issued lease token for a specific session.

## What Counts As An Active Session

An active session is a session for which the mobile app is currently allowed to receive real session content.

That includes:

- daemon-generated preview content
- interactive terminal snapshots
- live terminal bytes
- input / resize authority for that session

That does not include:

- the session merely existing on the daemon
- the session appearing in `session_index`
- daemon presence
- the local `tunnel run` process continuing to execute

## Tier Limits

Phase-1 entitlement limits:

| Limit | Free | Pro |
|---|---|---|
| `max_active_mobile_sessions` | 1 | more than 1 |

The architectural contract only requires that:

- free has exactly one slot
- pro has multiple slots

Phase-1 recommended operational values:

- free: `1`
- pro hard cap: `10`

The `10` limit exists as an abuse-protection ceiling, not as a user-facing product message. For normal individual paid usage it should feel effectively unlimited.

Direct and relay consume the same slot. Subscription does not care which path a session currently uses.

## What Free Users Can See

Free users may still see all discovered sessions in the mobile UI.

For every visible session, the app may render:

- `label`
- `command_preview`
- `cwd`
- `git_branch`
- basic online / offline metadata

But only the leased session may render or receive:

- real daemon preview text
- interactive attach grant
- terminal snapshots
- live bytes
- input / resize authority

This preserves a clear product difference:

- free users can understand what exists
- only one session is truly usable at a time

## Locked Session UI

Sessions that are visible but not currently usable under the account's subscription should be rendered as locked.

Phase-1 UI guidance:

- use a lock icon on the session row
- apply light greyed-out styling to distinguish it from the active leased session
- keep metadata readable; do not hide the row entirely
- do not show real preview content for locked sessions

When the user taps a locked session, the app should explain:

- free allows only one active session at a time
- which session is currently active
- upgrading unlocks more simultaneous sessions

Phase 1 should prefer a simple modal or bottom sheet with:

- `OK`
- `Upgrade`

It should not silently switch the active session on free.

## Lease Ownership And Lifecycle

An active session lease is owned by Relay and bound to:

- `account_id`
- `device_fingerprint`
- `daemon_id`
- `session_id`
- `leased_at`
- `expires_at`

Relay also issues a short-lived signed lease token that Android presents to the daemon.

The daemon validates:

- signature
- session binding
- device binding
- expiry

The daemon does not need to know:

- free vs pro
- billing state
- why the token was granted

This keeps subscription logic off the daemon while still making the limit enforceable on direct connections.

## Lease Acquisition

The intended phase-1 flow is:

1. Android receives `session_index` from the daemon
2. user selects one session to actually use
3. Android asks Relay for an active-session lease for that `session_id`
4. Relay checks whether the account still has a free slot
5. if allowed, Relay records the lease and returns a signed lease token
6. Android presents that token to the daemon
7. daemon starts sending real preview and may grant interactive attach for that session

If Relay denies the lease, the daemon must not begin real content delivery for that session.

## Lease Renewal And App Kill Behavior

To prevent easy abuse, a free-session lease is not tied directly to the Android app process lifetime.

Phase-1 rules:

- lease TTL default: `3 minutes`
- renewal interval while actively in use: every `45 seconds`
- Android renews the lease while the session remains actively in use
- if the app is backgrounded or killed, the lease remains valid until its short TTL expires
- if the same device returns before expiry, it may resume that lease
- a different session may not claim the free slot until the existing lease is explicitly released or expires

This prevents the simplest "kill app and immediately grab another session" loophole.

The TTL should be long enough not to punish normal short app switches, network changes, or process restarts, but short enough that a free user cannot cheaply cycle through sessions by repeatedly killing the app. The default `3 minutes` is the recommended phase-1 balance.

## Lease Release

On free tier, the user does not switch sessions by simply tapping another locked row.

To use a different session, the current lease must first be released.

Release may happen by:

- explicit user action
- the leased session ending
- lease TTL expiry without renewal
- logout or account switch

The active slot then becomes available for another session.

## What Subscription Does Not Touch

Subscription state MUST NOT influence:

- pairing trust validation
- SAS confirmation
- the QUIC handshake
- transport keys
- direct vs relay path selection
- daemon-local session existence

The only thing subscription changes is:

- whether Relay issues or renews an active-session lease

## Lapsed Subscription Behavior

If a pro subscription lapses, the account degrades to free-tier lease behavior.

That means:

- existing pairing trust remains intact
- daemon presence remains visible
- direct and relay transport still follow the same security model
- the account may keep at most one active session lease after degradation
- existing extra leased sessions may drain until their current lease expires, but Relay MUST refuse renewal beyond the new free-tier limit

This avoids abrupt breakage while keeping the free/pro boundary clear.

## Error Contract

When Relay refuses an active-session lease, it should return a structured error envelope:

```
{
  "code": "session_limit_reached",
  "current": 1,
  "max": 1,
  "active_session_id": "sess_123",
  "tier_required": "pro",
  "upgrade_url": "https://..."
}
```

Android should render a human-readable explanation rather than showing raw enum values.

## Abuse Protection

The active-session lease limit is the user-facing product control, not the only operational defense.

Relay should also apply internal rate limits that are not surfaced as subscription plan rules, for example:

- lease request rate per account
- lease request rate per device fingerprint
- fallback tunnel open rate per account
- reconnect burst limits per device fingerprint

These controls exist to limit abuse and accidental retry storms. They should not complicate the free/pro product explanation.

## Why This Model Was Chosen

This is intentionally simpler than earlier drafts that considered:

- paired-daemon count limits
- paired-device count limits
- relay-minute quotas
- separate direct and relay policies

Those models were rejected for phase 1 because they increase user confusion and spread entitlement logic across too many parts of the system.

The chosen model maps cleanly to the actual product story:

- free can fully use one session
- pro can fully use more sessions
- security does not change

## References

- `docs/connectivity/architecture.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/daemon-session-sync.md`
- `docs/connectivity/mobile-reference.md`
- `docs/connectivity/android-client-behavior.md`
