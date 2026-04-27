# Phase-1 Subscription Model

## Status

Phase-1 subscription rule for the QUIC session-connectivity architecture. Per `../contract.md` D3, this is the **sticky first-attach** rule. Older drafts based on "oldest live session" or "active-session lease tokens" are superseded.

## Product Direction

Phase 1 has two product tiers:

- `free`
- `pro`

There are no other paid packages in phase 1.

The product promise:

- transport security is identical on free and pro
- `tunnel run` on the computer is not subscription-limited
- the only difference is how much of the daemon-owned session list the official mobile app lets the user actively use

## Core Rule

- **Free may actively use only one session per connected daemon card at a time**
- **Pro is not app-limited by this rule in phase 1**

Relay tells the app the current tier. The app applies the rule against the daemon-provided session roster. The daemon does not know whether the account is free or pro.

A "connected daemon card" is any daemon card the user has explicitly opened and for which the app currently has a live daemon transport.

## What "Actively Use" Means

For phase 1, a session is actively usable in the official app when the app may:

- subscribe to real preview updates for it
- open its interactive detail view
- send input and resize for it

This does not affect:

- whether the session exists on the daemon
- whether the session appears in `session_index`
- whether the local `tunnel run` process continues running

## Free Rule: Sticky First-Attach

For each connected daemon card, the official app maintains one piece of state:

```
unlocked_session_id?  // optional; empty = no session has been chosen yet
```

Transitions:

1. **Initial bootstrap**: `unlocked_session_id` is empty. All session rows render as locked. Tapping a locked row shows the lock dialog (see Locked UI).
2. **First attach**: when the user explicitly invokes interactive on any visible session — i.e., the moment the app sends its first `interactive_request` for that daemon card — that `session_id` becomes `unlocked_session_id`.
3. **Sticky while alive**: as long as the unlocked session is alive in the daemon roster, `unlocked_session_id` does not change. New sessions appearing in the roster do not displace it. Other sessions remain locked.
4. **End of life**: when the unlocked session disappears (`session_gone` for that `session_id`), `unlocked_session_id` clears.
5. **Next attach picks again**: with `unlocked_session_id` cleared, the next user-initiated interactive_request becomes the new sticky unlocked session.

There is no auto-rollover. There is no manual switch UI. There is no scheduled or oldest-first selection.

### Why Sticky First-Attach

- Matches user intent: "the one I clicked is the one I want."
- No surprise rollovers when an unrelated session ends.
- Trivial to implement: one nullable `session_id` per daemon card.
- Predictable across reconnects and account switches.

## Pro Rule

On pro tier, the official app is not restricted by the free rule.

Phase-1 pro behavior:

- after roster bootstrap, automatically subscribe to preview for every live session in the opened daemon card
- allow interactive attach on any session
- no per-card or per-account session count cap in phase 1

Future product phases may add cap-or-throttle UX, but those are not part of the phase-1 contract.

## Locked Session UI

Locked sessions render with:

- a lock icon
- light grey styling
- metadata readable (label, command_preview, cwd, git_branch, online state)
- no real preview text

When the user taps a locked session on free tier, the app should explain:

- "Free can only run 1 session per computer at a time."
- if `unlocked_session_id` is empty: "Tap again to start using this session."
- if `unlocked_session_id` is set: the currently unlocked session's `label`, and "Wait for `<label>` to finish, or upgrade Pro."

Do not silently switch. Do not offer manual override.

If the payment flow is still deferred (`../contract.md` open TODOs), the upgrade affordance is informational only — no clickable link.

## Pre-Attach State

Before the user has attached to anything in a daemon card, every row is locked. UI guidance:

- show all rows with the lock icon
- show a friendly hint like "Tap any session to start using it" or similar
- do not auto-pick one for the user

This avoids the bootstrap ambiguity of the prior "oldest" rule and keeps the UX deterministic.

## Daemon Responsibilities

The daemon stays subscription-unaware.

It must:

- expose the full session roster to paired devices
- honor preview and interactive requests from paired devices regardless of the client's free / pro state
- avoid any free / pro branching

The free-tier restriction is therefore an official-app product behavior, not a daemon-side hard authorization boundary.

## Relay Responsibilities

Relay owns only the subscription tier surface.

Phase-1 expectations:

- Relay exposes the current tier to the app via authenticated app APIs
- Relay does not maintain any chosen session row
- Relay does not issue per-session access tokens
- Relay does not fan out per-session subscription decisions to daemons
- daemon sockets do not receive subscription-policy updates

This keeps Relay simple and avoids turning phase 1 into a distributed entitlement-control system.

## Security Boundary

The official app enforces the rule; a modified client could ignore it. Phase 1 accepts that explicitly.

What subscription state MUST NOT influence:

- pairing trust validation
- SAS confirmation
- the QUIC handshake
- transport keys
- direct vs relay path selection
- daemon-local session existence

If stronger daemon-side enforcement becomes necessary later, it should be added explicitly as a future capability system, not implied by phase-1 docs.

## Cross-Device Behavior

Two phones logged into the same account looking at the same daemon card may make different first-attach choices, so they may show different unlocked sessions. This is intentional: each device picks what its user clicks first.

This is acceptable for phase 1 because:

- the rule is enforced per device and per daemon card
- there is no Relay-owned "chosen session" state to conflict
- both devices remain free to attach to the unlocked session they each picked

## Account Switch / Reconnect

- account switch on Android closes the Relay and daemon transports and clears `unlocked_session_id` for every daemon card
- reconnect to the same account on the same daemon: `unlocked_session_id` is restored from local app state if the session is still in the roster; otherwise cleared
- daemon-side pairing trust survives account switches; only the in-app `unlocked_session_id` resets

## What Subscription Does Not Touch

Subscription state does not change which sessions exist, which devices are paired, or how the QUIC handshake authenticates peers. It only changes which sessions the official app permits real preview / interactive on.

## References

- `../architecture.md`
- `../contract.md`
- `../protocol/relay.md`
- `../protocol/transport.md`
- `android.md`
