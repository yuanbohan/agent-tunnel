# Connectivity Pairing Protocol Direction

## Status

This document captures the target pairing contract for the QUIC session-connectivity architecture.

## Pairing Goals

- bind one client device to one computer daemon
- keep the daemon as the trust root
- let Relay transport pairing messages without becoming a trust authority
- produce pinned device identities usable by future QUIC connections

## Preconditions

- client app is already logged in
- daemon belongs to the same account
- user runs `tunnel daemon pair`
- Relay is reachable for pairing response transport in phase 1

## Device Identity Model

Each side owns one persistent device key pair.

Recommended phase-1 model:

- daemon key pair stored in daemon-local state as `connectivity_identity.json` with mode `0600`
- client key pair generated on first authenticated app setup
- client private key stored with platform secure storage where available

The long-term device identifier is:

- public-key fingerprint

Display names are not trust identities.

### Why Device Identity Keys Exist

These long-lived device keys exist to answer:

- is this really the same daemon as before
- is this really the same client installation as before

They are not used as long-lived transport session keys.

Their main jobs are:

- sign pairing messages
- anchor the SAS confirmation
- serve as the identity that later QUIC/TLS connections pin against

## Invitation Model

`tunnel daemon pair` creates one short-lived, one-time invitation.

Recommended invitation payload. The current payload version is `2`.

- `version`
- `account_id`
- `computer_id`
- `computer_display_name`
- `computer_public_key`
- `invitation_id`
- `correlation_id`
- `nonce`
- `expires_at`
- `relay_base_url`
- `signature`

The daemon signs the invitation so the client can verify that the invitation payload originated from the computer identity it is about to trust.

## Pairing Flow

1. daemon reserves a short-lived `correlation_id` with Relay
2. Relay replies with the authenticated `account_id`
3. daemon creates the signed invitation locally
4. CLI prints a terminal QR code and pairing metadata; `--json` prints the machine-readable invitation payload
5. client imports the invitation payload through real QR scanning
6. client verifies:
   - invitation signature
   - invitation `account_id` matches the currently authenticated Relay account
   - expiry
7. client signs:
   - `account_id`
   - `invitation_id`
   - `correlation_id`
   - `client_fingerprint`
   - `client_public_key`
   - `client_display_name`
8. client sends pairing response to Relay with `POST /api/pairing/responses`
9. Relay forwards that response to the addressed daemon
10. daemon verifies the response locally and stores it as pending
11. daemon and client both display the same 6-digit SAS
12. user confirms the numbers match
13. daemon stores client trust locally
14. client stores computer trust locally
15. daemon informs Relay that pairing is now valid for presence visibility

Relay participates in message transport only. It does not decide trust.

## Account Binding Trust Boundary

The trust boundary is intentionally split:

- computer identity is daemon-verifiable
- account identity is Relay-asserted and transcript-bound

The `account_id` the client signs comes from the authenticated account associated with the app's opaque Relay app session. It is not a value Relay later inserts into `pair_response_forward` to the daemon.

Pairing transcript rules:

- client signs `(account_id || invitation_id || correlation_id || client_fingerprint || client_public_key || client_display_name)` with its device key, using the length-prefixed canonical transcript implemented by the pairing verifier
- the signature plus `account_id` travels to Relay through `POST /api/pairing/responses`
- daemon receives the response via `pair_response_forward`, verifies the client's signature over those exact fields, and compares `account_id` against `invitation.account_id`

This closes the account-substitution attack: even if Relay tries to hand the daemon a different account assertion, the value covered by the client's signature is the one the client believed during login. Relay cannot rewrite that field without breaking the signature.

Daemon still relies on Relay to have authenticated the app's login in the first place. Phase 1 does not attempt to make account identity independently daemon-verifiable without Relay participation, but the signature transcript prevents Relay from steering pairing across accounts at this step.

## Why SAS Prevents MITM

SAS is a `Short Authentication String`.

Both sides derive the same short code from the same pairing inputs:

- daemon public key
- client public key
- invitation id
- invitation nonce

If Relay or another network intermediary substitutes either side's public key during pairing, client and daemon no longer see the same input set, so they derive different SAS values.

That gives the user one simple check:

- if both screens show the same code, pairing may continue
- if the codes differ, abort because the pairing transcript was not identical on both ends

SAS is therefore not "extra decoration". It is the human-verifiable safeguard that turns a Relay-assisted pairing transport into a zero-trust pairing workflow.

## SAS Confirmation

Phase 1 uses a fixed 6-digit decimal SAS.

### Inputs

The SAS is computed from exactly these inputs, in this order, length-prefixed to prevent boundary ambiguity:

1. `computer_public_key` — 32 raw Ed25519 public-key bytes
2. `client_public_key` — 32 raw Ed25519 public-key bytes
3. `invitation_id` — UTF-8 bytes of the invitation_id string
4. `nonce` — raw bytes of the invitation nonce

Each input is encoded as `len_u16_be || bytes`, where `len_u16_be` is the length of the input encoded as a 2-byte big-endian unsigned integer. The four encoded inputs are concatenated.

### Algorithm

```
canonical = u16be(len(computer_public_key)) || computer_public_key
         || u16be(len(client_public_key))   || client_public_key
         || u16be(len(invitation_id))   || invitation_id_utf8
         || u16be(len(nonce))           || nonce

digest   = SHA-256(canonical)              # 32 bytes
short    = first 4 bytes of digest, big-endian uint32
sas      = short mod 1_000_000              # in [0, 999999]
display  = zero-padded 6 decimal digits, e.g. "012345"
```

`SHA-256` is chosen for ubiquity in standard libraries on Go, Android, iOS, and other clients.

The output gives approximately `log2(1_000_000) ≈ 20` bits of MITM resistance, on par with ZRTP, Signal safety numbers, and Matrix Olm key verification.

### Test Vectors

Implementations MUST agree on output for the following input. Daemon and client implementations should each persist these test vectors in their own test suites and recompute on every build.

Phase-1 implementations MUST include these shared golden vectors before the
first end-to-end pairing is attempted. Byte fields are hex encoded.

| Case | `computer_public_key` | `client_public_key` | `invitation_id` | `nonce` | `display` |
|---|---|---|---|---|---|
| ascending keys | `000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f` | `202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f` | `invite-0001` | `000102030405060708090a0b0c0d0e0f` | `696700` |
| boundary shaped ids | `fffefdfcfbfaf9f8f7f6f5f4f3f2f1f0efeeedecebeae9e8e7e6e5e4e3e2e1e0` | `000306090c0f1215181b1e2124272a2d303336393c3f4245484b4e5154575a5d` | `edge-boundary` | `101112131415161718191a1b1c1d1e1f` | `626209` |
| high bit keys | `7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f` | `8080808080808080808080808080808080808080808080808080808080808080` | `unicode-safe-ascii` | `ffffffff00000000aaaaaaaa55555555` | `670900` |

### UX Rules

The SAS is only as strong as the user's willingness to actually compare the codes. To avoid click-fatigue, phase 1 mandates **active confirmation**:

- both daemon CLI and client display the 6 digits in a clearly legible font
- client requires the user to explicitly press a confirmation control after at least `1s` has elapsed since display
- the client confirmation screen MUST NOT prefocus or auto-confirm the match button
- the daemon CLI prompts the user to type the matching code or press Enter only after the operator has read it
- a mismatch path is available on both ends and aborts pairing with `pairing_sas_mismatch`

Implementations SHOULD add a small "compare both screens" instructional line so first-time users do not skip the check.

### Why The Code Cannot Be Auto-Compared

Auto-comparison through Relay would defeat the purpose: the SAS exists precisely to detect a Relay that may have substituted keys during pairing transport. A network-side compare is the attacker's natural place to lie. Only the human operator's eyes can compare the two screens out-of-band.

### Pairing Is Not Complete Until SAS Matches

- daemon does not persist client trust until the operator confirms SAS match locally
- client does not persist daemon trust until the user confirms SAS match locally
- if either side aborts at the SAS step, the invitation is consumed and may not be reused

## After Pairing

Once pairing succeeds:

- client persists the computer fingerprint
- daemon persists the client fingerprint
- future QUIC/TLS connections accept only peers that prove possession of those pinned identities

This is why later transport authentication no longer needs repeated human confirmation: the trust anchor was already established during pairing.

## Local Persistence

### Daemon Stores

The daemon persists, with file mode `0600` under `~/.tunnel/`:

- daemon Ed25519 device key pair (`connectivity_identity.json`)
- trusted client roster:
  - client device fingerprint
  - client display name
  - `paired_at`
  - trust status
- in-flight invitation roster (see "Invitation Persistence" below)

The daemon device key is the trust root for every paired client device. It must be treated with care comparable to an SSH host key. Phase 1 stores it as a `0600`-mode JSON file; future phases may move it to OS-native key storage (macOS Keychain, Linux secret-service) without changing the wire protocol.

### Client Stores

- computer fingerprint
- computer display name
- `paired_at`
- trust status

Client device keys SHOULD be stored in the platform's secure key storage when available, such as Android Keystore or iOS Keychain/Secure Enclave. Reinstalling the app deletes the device key; the user must re-pair after reinstall.

### Relay Stores

Relay should only store:

- short-lived pairing transport state
- the minimum authorization result needed to expose daemon presence to that client device after pairing succeeds

Relay should not become the durable trust database.

## Invitation Persistence

The daemon MUST persist invitation state across daemon restarts to prevent reuse-after-restart attacks.

Phase-1 rules:

- when `tunnel daemon pair` mints an invitation, the daemon writes a record to local state containing:
  - `invitation_id`
  - `nonce`
  - `correlation_id`
  - `expires_at`
  - `consumed` (boolean, initially false)
- on restart, the daemon reloads the roster
- a new pairing response is accepted only if the matching `invitation_id` exists, is not expired, and is not yet consumed
- on successful pair, the daemon flips `consumed = true` and persists; the record remains on disk until `expires_at` to defeat replay attempts that arrive after the consumer pair completed
- a background sweep removes records whose `expires_at` is in the past

Default invitation TTL is `5 minutes` in phase 1. The TTL is not user-configurable in phase 1.

Storage form is implementation choice (single JSON file or SQLite); the contract is that the daemon MUST NOT lose this state across normal restarts.

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
- daemon immediately closes any active QUIC connections authenticated as that client fingerprint
- daemon immediately closes any interactive streams owned by that client fingerprint
- daemon rejects all future handshakes from that client fingerprint
- daemon notifies Relay to remove the derived visibility grant
- the client must stop treating the computer as trusted

## Failure Rules

- expired invitation: fail closed
- reused invitation: fail closed
- account mismatch: fail closed
- invalid daemon signature: fail closed
- invalid client signature: fail closed
- SAS mismatch: fail closed

### Pairing Error Codes

To support actionable UI, both daemon and client implementations SHOULD surface failures with stable error codes. The phase-1 enum:

| Code | Meaning | User-facing recovery |
|---|---|---|
| `pairing_invitation_expired` | invitation `expires_at` is in the past | run `tunnel daemon pair` again to mint a new invitation |
| `pairing_invitation_invalid` | invitation payload could not be parsed or its signature did not verify | re-import or check the daemon CLI output |
| `pairing_invitation_consumed` | invitation has already completed once | mint a new invitation |
| `pairing_account_mismatch` | client is logged into a different account than the invitation binds | log in with the matching account |
| `pairing_relay_unreachable` | Relay could not be reached for response transport | check network and retry |
| `pairing_signature_failed` | daemon could not verify the client response signature | likely tampering; abort and re-pair |
| `pairing_sas_mismatch` | user reported the two screens did not show the same SAS | abort; never proceed with mismatched SAS |
| `pairing_unknown_error` | catch-all | retry; if persistent, capture diagnostics |

Client UI MUST surface a human-readable explanation for each code rather than displaying the code itself.

## Daemon Key Compromise Recovery

The architecture cannot in-band revoke a daemon device key. Recovery is operational:

- generate a new daemon device key pair (typically by reinstalling daemon state or running an explicit `tunnel daemon reset-identity` command in a future phase)
- run `tunnel daemon pair` and re-pair every client device against the new identity
- the old fingerprint is overwritten in each client device's local trust store after the new pair completes successfully

Phase 1 does not provide automatic key rotation. Operators should treat daemon device keys with the same care as SSH host keys.

## Why This Is Simpler Than The WebRTC Pairing Draft

- no TURN credential tie-in
- no transport-specific signaling state
- no interactive lease coupling
- pairing produces one thing only: pinned device trust

## References

- `../architecture.md`
- `../contract.md`
- `relay.md`
- `../ux/android.md`
- Ed25519: `https://datatracker.ietf.org/doc/html/rfc8032`
- Android Keystore: `https://developer.android.com/privacy-and-security/keystore`
