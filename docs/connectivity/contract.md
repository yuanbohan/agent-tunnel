# Phase-1 Connectivity Contract

## Status

This document is the canonical phase-1 must-ship contract. When two docs disagree, this one wins. Decisions here were confirmed on 2026-04-26 and should not be changed without a new architecture review.

This is intentionally short. Detail lives in the linked specs. Use this file to scope work.

## Decisions

### D1 — Fallback Carrier: WSS-Tunneled QUIC

- Direct path: QUIC over UDP, peers discover candidates via STUN + Relay rendezvous.
- Fallback path: QUIC packets encapsulated as WebSocket-over-HTTPS frames through a Relay-hosted tunnel.
- Both paths terminate the same QUIC/TLS handshake at daemon and Android with pinned device identities.

**Why this and not UDP relay**

- WSS reuses 443, existing nginx, existing certs. UDP relay needs a new public port plus DDoS / amplification mitigations.
- Both options are security-equivalent: Relay sees only encrypted QUIC packets either way.

**Acknowledged tradeoff**

- WSS-tunneled QUIC is a known antipattern (TCP HoL + double congestion control). Acceptable on the fallback path because that path is the degraded mode.

**Phase-2 TODO**

- Add a fallback latency SLO: `p95(input round-trip on relay path) < 500ms`.
- If production data exceeds the SLO for two consecutive weeks, escalate to "switch fallback to UDP relay" and re-spec.

Detail: `protocol/transport.md`, `protocol/relay.md`.

---

### D2 — Daemon Auto-Start

- `tunnel run` checks for the local daemon socket on startup.
- If the daemon is not running, `tunnel run` forks one as a detached child process before opening its PTY.
- The daemon process is parent-detached: it survives `tunnel run` exit.
- The user does not manage daemon lifecycle as an explicit step.

`tunnel daemon ensure` is the new internal command that performs the check-and-fork. `tunnel run` calls it on every start.

**Why**: keeps the existing single-command UX. Without this, the connectivity rewrite would be a UX regression versus the old direct-relay-attach flow.

**Phase-2 TODO**

- Provide `install.sh` users an opt-in launchd / systemd unit for explicit lifecycle management. Auto-start remains the default fallback.

Detail: `ux/android.md` § Daemon Lifecycle Expectation, `protocol/local-broker.md`.

---

### D3 — Free Unlock Rule: Sticky First-Attach

- Each connected daemon card has at most one unlocked session for the official app on free tier.
- The first session the user explicitly attaches (i.e., the first `interactive_request` Android sends for that daemon card) becomes that card's unlocked session.
- Once chosen, the unlocked session does not change as long as it is alive.
- When the unlocked session ends, the next user-attach picks a new one. **No auto-rollover**.
- All other rows render as locked. Tapping a locked row shows: "Free can only run 1 session per computer at a time. Wait for `<unlocked label>` to finish, or upgrade Pro."

This rule is enforced **only by the official app**. Daemon and Relay do not know about it.

**Why this and not "oldest"**: aligns with user intent ("the one I clicked is the one I want to use"). Eliminates the rollover surprise. Trivial to implement: Android stores `(daemon_id → unlocked_session_id?)`.

Detail: `ux/subscription.md`.

---

### D4 — App Identity: Opaque App Session With Client-Supplied `device_fingerprint`

- Android first-install generates a long-lived device key in Android Keystore. `device_fingerprint = sha256(public_key_raw)` is cached locally.
- Login request body: `{ username, password, device_fingerprint }`.
- Relay validates credentials and returns the existing opaque app access/refresh tokens.
- Relay persists `(account_id, app_session_id, device_fingerprint)` server-side.
- Token refresh requires the same `device_fingerprint`; mismatch is rejected.
- Phase 1 does **not** require a cryptographic proof that the app-session holder owns the device private key. Daemon-side security relies on pairing-pinned device keys, not app-session token format.

**Phase-2 TODO**

- Add `/auth/register-device` flow that requires the client to sign a Relay challenge with the device key, upgrading the app session to a proof-of-possession model.

Detail: `protocol/relay.md` § App Authentication Model.

---

### D5 — Stream Model: Bidirectional Control + Unidirectional Interactive

- One **bidirectional control stream** per daemon connection. Carries all typed control frames including `input_text`, `input_key`, `resize`. Single writer per side.
- One **unidirectional interactive stream** per attached session. Daemon-initiated, daemon→Android only. Carries `snapshot_begin`, `snapshot_chunk`, `snapshot_end`, `live_bytes`.
- Input never travels on an interactive stream.

**Why**

- Each interactive stream has its own QUIC flow window — slow consumer on session A cannot block session B.
- Control stream stays single-writer per side, simple to reason about.
- Daemon can stream PTY output without reading concerns; input handling is centralized on the control stream.

Detail: `protocol/transport.md` § Stream Model.

---

### D6 — Frame Encoding: JSON Payloads, Length-Framed

- Outer frame: `[1 byte type] [varint length] [payload bytes]`.
- Payload encoding for typed control frames: **JSON**.
- Exception: `live_bytes` and `snapshot_chunk` carry raw PTY bytes, no JSON wrap. The outer length frame is sufficient.
- Forward compatibility: receivers MUST ignore unknown JSON fields and unknown frame types. Protocol-breaking changes bump `protocol_version` in `hello`.

**Why JSON**

- Human-readable in tcpdump and customer support logs.
- Mature serializers in Go (`encoding/json`) and Kotlin (`kotlinx.serialization.json`).
- Phase 1 is pre-launch; debuggability outweighs bytes saved by CBOR.

**Phase-2 TODO**

- If profiling shows JSON parse cost on Android > 5% of a frame's wall time, define `protocol_version = 2` with CBOR.

Detail: `protocol/transport.md` § Control Stream Encoding.

---

## Phase-1 Implementation Sub-Phases

Each sub-phase has a hard gate: do not start the next until the current one's checklist passes.

### 1.0 — Interop Spike (Protocol/Data Gate)

- `quic-go` listener with ALPN `tunnel-conn/1`.
- Go mobile-simulator client that follows the Android-facing protocol shape.
- Both Go endpoints present self-signed Ed25519 certs; both verify peer SPKI byte-for-byte.
- Exchange JSON `hello`, JSON `session_index`, JSON `interactive_request`, JSON `interactive_granted`, JSON `snapshot_begin`, raw `snapshot_chunk`, JSON `snapshot_end`, and raw `live_bytes`.
- Exercise direct UDP and Relay-like packet-carrier paths.
- Reconnect 10 times in a loop without leaks.
- **Exit criterion**: repository tests pass the above with a Go daemon side and a Go mobile-simulator side. This proves Step 1 protocol/data assumptions, not production Android runtime compatibility.

FIXME(Android): Before claiming Android compatibility, run the same pinned TLS
and stream/data exchange through the Android `quiche` JNI client on emulator and
device. If `quiche` packaging blocks, fall back to `kwik` per
`reference/decision-record.md`.

### 1.1 — Pairing + Local Broker

- daemon identity persistence (`connectivity_identity.json` in the daemon state directory, mode 0600).
- invitation persistence (`~/.tunnel/invitations.json`).
- SAS computation with golden vectors checked in (≥ 3 cases) before any pairing UI is built.
- `tunnel daemon pair` reserves Relay correlation and prints a signed JSON invitation; QR rendering is deferred.
- Test client (Go-only, no Android required) completes a full pair end-to-end through Relay.
- `tunnel daemon ensure` auto-start path works from cold.
- Local broker socket: `tunnel run` registers, pushes preview, daemon mirrors.

**Exit criterion**: golden test (`tunnel daemon pair` + test client) passes; daemon-state sweep tests pass; local broker roundtrip < 5ms.

### 1.2 — Relay Control Plane + Fallback Transport

- Relay WSS for app + daemon, app-session auth with `device_fingerprint`.
- `daemon_register`, `app_register`, `daemon_snapshot`, presence churn.
- `pair_response_submit` / `pair_response_forward`.
- Fallback WSS-QUIC tunnel (no direct path yet).
- Android `quiche` client connects via fallback only, runs full session list + preview + interactive flow.

**Exit criterion**: Android can list sessions, view preview, attach interactive, send input on fallback path against a real daemon. No direct-path code on this milestone.

### 1.3 — Direct Path + STUN + Degradation

- Self-hosted STUN listener.
- `rendezvous_open` / `rendezvous_hint`.
- UDP hole-punch + direct QUIC handshake with a 3s deadline.
- On failure, transition to the fallback path from 1.2 without restarting the connection state machine.
- Path badge in UI.

**Exit criterion**: cone-NAT deployments achieve ≥ 80% direct success on a measurement panel of ≥ 20 test pairings; symmetric NATs cleanly fall back.

---

## What Is Explicitly Out Of Phase 1

- UDP relay (deferred per D1 TODO).
- Daemon-side per-session ACL (single-user assumption).
- Per-session entitlement tokens / leases (replaced by D3).
- Multi-paired-device session handover.
- Cross-device cursor / focus sync.
- Preview cache on Android.
- Missed-byte replay on reconnect (re-snapshot only).
- 0-RTT resume (disabled per `protocol/transport.md`).
- Daemon device-key in OS keyring (deferred to phase 2).
- Pro upgrade payment flow.

---

## Open TODOs Tracked Against The Contract

| ID | Decision | Trigger | Action |
|---|---|---|---|
| T-FB-LATENCY | D1 | p95 fallback input RTT > 500ms two consecutive weeks | Re-spec UDP relay |
| T-DAEMON-SVC | D2 | Auto-start proves flaky in long-running deployments | Ship launchd / systemd units in `install.sh` |
| T-AUTH-POP | D4 | Account-token theft becomes a real abuse vector | Implement `/auth/register-device` proof-of-possession |
| T-ENC-CBOR | D6 | Android JSON parse > 5% frame wall time | Define `protocol_version = 2` with CBOR |
| T-IDENTITY-OS | various | Daemon-key file mode 0600 proves insufficient | Migrate to OS keyring |
| T-OBS | new | First production deploy | Author `observability.md`: attempt_id tracing, daemon status command, Android session-state metrics |

---

## Related Documents

- `architecture.md` — system shape and security model.
- `protocol/transport.md` — QUIC + frames + session sync.
- `protocol/relay.md` — control plane events and rate limits.
- `protocol/pairing.md` — invitation + SAS.
- `protocol/local-broker.md` — daemon ↔ tunnel run.
- `ux/subscription.md` — sticky-first-attach detail.
- `ux/android.md` — Android client behavior reference.
- `reference/decision-record.md` — historical decision rationale.
- `_archive/2026-04-26-architect-review.md` — earlier review record.
