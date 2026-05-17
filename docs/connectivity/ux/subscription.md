# Phase-1 Connectivity Tier Model

## Status

Phase 1 uses a computer-count product rule. Free and Pro are identical inside one active trusted computer: session rows, previews, detail attach, reconnect, and path badges use the same daemon transport behavior.

## Product Direction

Phase 1 has two product tiers:

- `free`
- `pro`

The only entitlement difference is trusted computer count:

- `free`: at most 1 active trusted computer.
- `pro`: at most 10 trusted computers.

There is no session-level tier logic in phase 1. Do not implement Free-only session row states, preview restrictions, or per-session entitlement leases.

## Definitions

- **Trusted computer**: a daemon the Android app has paired with and still trusts locally.
- **Active trusted computer**: a trusted computer the official app is allowed to connect to under the current tier.
- **Online trusted computer**: an active trusted computer currently visible through Relay presence.

Relay exposes the account tier. Android owns the local trusted-computer set and the active-computer decision. Daemon and Relay do not know which session rows are "allowed" because there is no per-session allowance.

## Free Rule

Free may keep exactly one active trusted computer.

Startup behavior:

1. If the user has one active trusted computer and it is online, the app auto-connects to it.
2. If the user has no active trusted computer, the app shows pairing.
3. If local state somehow contains multiple active trusted computers, the app enters resolution UI and requires the user to choose one before connecting.

Inside the active computer, all live sessions are usable:

- all rows are visible
- previews may be requested for any row
- detail attach works for any row
- reconnect rebuilds the same full session list
- path badges behave the same as Pro

## Replace Computer

Free users change computers through transactional Replace Computer.

Flow:

1. User starts pairing with the new computer.
2. The app keeps the old trusted computer active during the pairing attempt.
3. Only after the new pairing SAS succeeds does Android mark the new computer active and locally delete the old trust.
4. If pairing fails, is canceled, or the SAS mismatches, the old trust remains active.

TODO: phase 1 only deletes the old trust locally on Android. Add daemon-side old-trust revoke later so the replaced computer also removes the Android fingerprint from its daemon trust store.

## Pro Rule

Pro may keep up to 10 trusted computers.

Startup behavior:

1. Auto-connect every online trusted computer, up to the 10-computer limit.
2. If the user is already at 10 trusted computers, block new pairing and ask the user to remove one computer first.
3. Within each connected computer, session behavior is identical to Free.

There is no Pro-only preview mode. Pro only increases the number of computers the account may actively trust.

## Downgrade From Pro To Free

If a Pro user downgrades to Free while more than one trusted computer exists, the app enters downgrade resolution.

Rules:

- Do not auto-connect multiple computers.
- Show the trusted computers and require the user to choose one to keep active.
- After the user chooses, locally deactivate or delete the others according to the app's trust-management UI.
- Until resolution completes, session transport should stay closed for all but an already selected active computer.

This keeps the Free invariant simple: one active trusted computer.

## Relay Responsibilities

Relay owns only the tier surface:

- `GET /api/account/policy` returns `account_id` and `tier`.
- Relay does not store active computer selection.
- Relay does not store selected session rows.
- Relay does not send per-session policy events.
- Relay does not fan out tier changes to daemon sockets.

## Daemon Responsibilities

The daemon is tier-unaware.

It must:

- expose the full local session roster to every paired Android device
- serve preview subscriptions for any local session
- serve interactive attach for any local session
- avoid Free / Pro branching

Trust remains daemon-local. Pairing and revocation decide which Android device fingerprints the daemon accepts; account tier does not.

## Android Responsibilities

The official Android app owns:

- local trusted-computer inventory
- the Free active-computer invariant
- the Pro 10-computer limit
- Replace Computer flow
- downgrade resolution UI
- which session previews to subscribe to for performance and UX

Android must not hide, lock, or gate individual session rows based on tier once a computer is active.

## Security Boundary

Phase 1 product enforcement is official-client behavior. A modified client could ignore the local computer-count rule if it already holds daemon trust. This is accepted for phase 1.

The tier model must not influence:

- SAS confirmation
- device-key identity
- QUIC/TLS handshake
- direct vs relay path selection
- daemon-local session existence
- input, resize, preview, or attach authorization inside an already trusted daemon connection

## References

- `../contract.md`
- `../architecture.md`
- `../protocol/relay.md`
- `../protocol/transport.md`
- `android.md`
