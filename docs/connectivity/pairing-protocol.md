# Connectivity Pairing Protocol Direction

## Status

This document captures the target pairing contract for the QUIC session-connectivity architecture.

## Pairing Goals

- bind one Android device to one daemon
- keep the daemon as the trust root
- let Relay transport pairing messages without becoming a trust authority
- produce pinned device identities usable by future QUIC connections

## Preconditions

- Android app is already logged in
- daemon belongs to the same account
- user runs `tunnel daemon pair`
- Relay is reachable for pairing response transport in phase 1

## Device Identity Model

Each side owns one persistent device key pair.

Recommended phase-1 model:

- daemon key pair stored in daemon-local state
- Android key pair generated on first authenticated app setup
- Android private key stored with Android Keystore where available

The long-term device identifier is:

- public-key fingerprint

Display names are not trust identities.

### Why Device Identity Keys Exist

These long-lived device keys exist to answer:

- is this really the same daemon as before
- is this really the same Android installation as before

They are not used as long-lived transport session keys.

Their main jobs are:

- sign pairing messages
- anchor the SAS confirmation
- serve as the identity that later QUIC/TLS connections pin against

## Invitation Model

`tunnel daemon pair` creates one short-lived, one-time invitation.

Recommended invitation payload:

- `version`
- `account_id`
- `daemon_id`
- `daemon_display_name`
- `daemon_pubkey`
- `invitation_id`
- `correlation_id`
- `nonce`
- `expires_at`
- `relay_base_url`
- `signature`

The daemon signs the invitation so Android can verify that the QR payload originated from the daemon identity it is about to trust.

## Pairing Flow

1. daemon creates the invitation locally
2. daemon requests or reserves a short-lived `correlation_id` with Relay
3. CLI renders the invitation as a QR code
4. Android scans the QR
5. Android verifies:
   - invitation signature
   - account binding
   - expiry
6. Android signs:
   - `invitation_id`
   - `nonce`
   - `android_pubkey`
7. Android sends pairing response to Relay with the `correlation_id`
8. Relay forwards that response to the addressed daemon
9. daemon verifies the response locally
10. daemon and Android both display the same 6-digit SAS
11. user confirms the numbers match
12. daemon stores Android trust locally
13. Android stores daemon trust locally
14. daemon optionally informs Relay that pairing is now valid for presence visibility

Relay participates in message transport only. It does not decide trust.

## Why SAS Prevents MITM

SAS is a `Short Authentication String`.

Both sides derive the same short code from the same pairing inputs:

- daemon public key
- Android public key
- invitation id
- invitation nonce

If Relay or another network intermediary substitutes either side's public key during pairing, Android and daemon no longer see the same input set, so they derive different SAS values.

That gives the user one simple check:

- if both screens show the same code, pairing may continue
- if the codes differ, abort because the pairing transcript was not identical on both ends

SAS is therefore not "extra decoration". It is the human-verifiable safeguard that turns a Relay-assisted pairing transport into a zero-trust pairing workflow.

## SAS Confirmation

Phase 1 uses a fixed 6-digit decimal SAS.

### Inputs

The SAS is computed from exactly these inputs, in this order, length-prefixed to prevent boundary ambiguity:

1. `daemon_pubkey` — 32 raw Ed25519 public-key bytes
2. `android_pubkey` — 32 raw Ed25519 public-key bytes
3. `invitation_id` — UTF-8 bytes of the invitation_id string
4. `nonce` — raw bytes of the invitation nonce

Each input is encoded as `len_u16_be || bytes`, where `len_u16_be` is the length of the input encoded as a 2-byte big-endian unsigned integer. The four encoded inputs are concatenated.

### Algorithm

```
canonical = u16be(len(daemon_pubkey))   || daemon_pubkey
         || u16be(len(android_pubkey))  || android_pubkey
         || u16be(len(invitation_id))   || invitation_id_utf8
         || u16be(len(nonce))           || nonce

digest   = SHA-256(canonical)              # 32 bytes
short    = first 4 bytes of digest, big-endian uint32
sas      = short mod 1_000_000              # in [0, 999999]
display  = zero-padded 6 decimal digits, e.g. "012345"
```

`SHA-256` is chosen for ubiquity in standard libraries on both Go and Android.

The output gives approximately `log2(1_000_000) ≈ 20` bits of MITM resistance, on par with ZRTP, Signal safety numbers, and Matrix Olm key verification.

### Test Vectors

Implementations MUST agree on output for the following input. Daemon and Android implementations should each persist these test vectors in their own test suites and recompute on every build.

Phase-1 implementations should add at least one shared golden vector to both code bases before the first end-to-end pairing is attempted. The exact byte values will be filled in once daemon and Android implementations exist; until then, the algorithm above is the contract.

### UX Rules

The SAS is only as strong as the user's willingness to actually compare the codes. To avoid click-fatigue, phase 1 mandates **active confirmation**:

- both daemon CLI and Android display the 6 digits in a clearly legible font
- Android requires the user to explicitly press a confirmation control after at least `1s` has elapsed since display
- the Android confirmation screen MUST NOT prefocus or auto-confirm the match button
- the daemon CLI prompts the user to type the matching code or press Enter only after the operator has read it
- a mismatch path is available on both ends and aborts pairing with `pairing_sas_mismatch`

Implementations SHOULD add a small "compare both screens" instructional line so first-time users do not skip the check.

### Why The Code Cannot Be Auto-Compared

Auto-comparison through Relay would defeat the purpose: the SAS exists precisely to detect a Relay that may have substituted keys during pairing transport. A network-side compare is the attacker's natural place to lie. Only the human operator's eyes can compare the two screens out-of-band.

### Pairing Is Not Complete Until SAS Matches

- daemon does not persist Android trust until the operator confirms SAS match locally
- Android does not persist daemon trust until the user confirms SAS match locally
- if either side aborts at the SAS step, the invitation is consumed and may not be reused

## After Pairing

Once pairing succeeds:

- Android persists the daemon fingerprint
- daemon persists the Android fingerprint
- future QUIC/TLS connections accept only peers that prove possession of those pinned identities

This is why later transport authentication no longer needs repeated human confirmation: the trust anchor was already established during pairing.

## Local Persistence

### Daemon Stores

- Android device fingerprint
- Android display name
- paired_at
- trust status

### Android Stores

- daemon fingerprint
- daemon display name
- paired_at
- trust status

### Relay Stores

Relay should only store:

- short-lived pairing transport state
- the minimum authorization result needed to expose daemon presence to that Android device after pairing succeeds

Relay should not become the durable trust database.

## Transport Consequence

Pairing does not create one long-lived shared symmetric secret.

Instead, it creates:

- a pinned peer identity
- local trust state on each endpoint

Later transport connections use those pinned identities to authenticate a fresh TLS 1.3 handshake, which then derives new per-connection symmetric keys.

## Revocation

The daemon remains authoritative for revoke.

Phase-1 management surface:

- `tunnel daemon devices`
- `tunnel daemon revoke <device>`

After revoke:

- daemon removes local trust
- daemon immediately closes any active QUIC connections authenticated as that Android fingerprint
- daemon immediately closes any interactive streams owned by that Android fingerprint
- daemon rejects all future handshakes from that Android fingerprint
- daemon notifies Relay to remove the derived visibility grant
- Android must stop treating the daemon as trusted

## Failure Rules

- expired invitation: fail closed
- reused invitation: fail closed
- account mismatch: fail closed
- invalid daemon signature: fail closed
- invalid Android signature: fail closed
- SAS mismatch: fail closed

### Pairing Error Codes

To support actionable UI, both daemon and Android implementations SHOULD surface failures with stable error codes. The phase-1 enum:

| Code | Meaning | User-facing recovery |
|---|---|---|
| `pairing_invitation_expired` | invitation `expires_at` is in the past | run `tunnel daemon pair` again to mint a new invitation |
| `pairing_invitation_invalid` | QR could not be parsed or its signature did not verify | re-scan or check the daemon CLI output |
| `pairing_invitation_consumed` | invitation has already completed once | mint a new invitation |
| `pairing_account_mismatch` | Android is logged into a different account than the invitation binds | log in with the matching account |
| `pairing_relay_unreachable` | Relay could not be reached for response transport | check network and retry |
| `pairing_signature_failed` | daemon could not verify the Android response signature | likely tampering; abort and re-pair |
| `pairing_sas_mismatch` | user reported the two screens did not show the same SAS | abort; never proceed with mismatched SAS |
| `pairing_unknown_error` | catch-all | retry; if persistent, capture diagnostics |

Android UI MUST surface a human-readable explanation for each code rather than displaying the code itself.

## Daemon Key Compromise Recovery

The architecture cannot in-band revoke a daemon device key. Recovery is operational:

- generate a new daemon device key pair (typically by reinstalling daemon state or running an explicit `tunnel daemon reset-identity` command in a future phase)
- run `tunnel daemon pair` and re-pair every Android device against the new identity
- the old fingerprint is overwritten in each Android device's local trust store after the new pair completes successfully

Phase 1 does not provide automatic key rotation. Operators should treat daemon device keys with the same care as SSH host keys.

## Why This Is Simpler Than The WebRTC Pairing Draft

- no TURN credential tie-in
- no transport-specific signaling state
- no interactive lease coupling
- pairing produces one thing only: pinned device trust

## References

- `docs/connectivity/architecture.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/mobile-reference.md`
- Ed25519: `https://datatracker.ietf.org/doc/html/rfc8032`
- Android Keystore: `https://developer.android.com/privacy-and-security/keystore`
