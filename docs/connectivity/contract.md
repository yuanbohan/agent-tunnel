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

### D2 — Required Local Daemon

- `tunnel run` checks for the local daemon socket on startup.
- If the daemon is not running, `tunnel run` forks one as a detached child process before opening its PTY.
- `tunnel run` waits for the local broker to accept its session id before opening its PTY.
- The daemon process is parent-detached: it survives `tunnel run` exit.
- The default user path does not require managing daemon lifecycle as an explicit step.
- There is no public `tunnel run --daemon` bypass in this revision.

The check-and-fork path is internal to `tunnel run`; it is not a public daemon subcommand.

**Why**: keeps the existing single-command UX. Without this, the connectivity rewrite would be a UX regression versus the old direct-relay-attach flow.

**Phase-2 TODO**

- Provide `install.sh` users an opt-in launchd / systemd unit for explicit lifecycle management. Auto-start remains the default fallback.

Detail: `ux/android.md` § Daemon Lifecycle Expectation, `protocol/local-broker.md`.

---

### D3 — Tier Rule: Computer Count Only

- Free may keep at most one active trusted computer.
- Pro may keep up to ten trusted computers.
- Inside an active trusted computer, Free and Pro have identical session behavior: rows, previews, detail attach, reconnect, and path badges are all available.
- There is no session-level tier gating in phase 1. Do not implement Free-only session row states, preview restrictions, or per-session entitlement leases.
- Free computer changes use transactional Replace Computer: the old trust stays active until the new pairing SAS succeeds. On success, Android locally deletes the old trust and marks the new computer active.
- Pro downgrade to Free requires a resolution UI when multiple trusted computers exist. Until the user chooses one active computer, the app must not auto-connect multiple computers.

This rule is enforced by the official app's local trusted-computer state. Daemon and Relay remain tier-unaware for session transport. Relay exposes only the account `tier`.

**Phase-2 TODO**: Replace Computer currently removes the old trust locally on Android only. Add daemon-side old-trust revoke later so the replaced computer removes that Android fingerprint from its daemon trust store.

Detail: `ux/subscription.md`.

---

### D4 — App Identity: Opaque App Session With Client-Supplied `client_fingerprint`

- Android first-install generates a long-lived device key in Android Keystore. `client_fingerprint = sha256(public_key_raw)` is cached locally.
- Login request body: `{ username, password, client_fingerprint }`.
- Relay validates credentials and returns the existing opaque app access/refresh tokens.
- Relay persists `(account_id, app_session_id, client_fingerprint)` server-side.
- Token refresh requires the same `client_fingerprint`; mismatch is rejected.
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
- `tunnel pair` reserves Relay correlation, prints a terminal QR code, waits for the client response, and prompts for the 6-digit SAS; `--json` prints the signed invitation payload.
- Test client (Go-only, no Android required) completes a full pair end-to-end through Relay.
- `tunnel run` daemon auto-start path works from cold.
- Local broker socket: `tunnel run` registers, pushes preview, daemon caches latest preview.

**Exit criterion**: golden test (`tunnel pair` + test client) passes; daemon-state sweep tests pass; local broker roundtrip < 5ms.

### 1.2 — Relay Control Plane + Fallback Transport

- Relay WSS for app + daemon, app-session auth with `client_fingerprint`.
- `computer_register`, `app_register`, `computer_snapshot`, presence churn.
- REST `POST /api/pairing/responses` / daemon `pair_response_forward`.
- Fallback WSS-QUIC tunnel (no direct path yet).
- Go mobile-simulator client connects via fallback only, runs the client-facing
  session list, preview, interactive request, input, and reconnect flow.
- Android companion implementation validates the same contract in Step 6 once
  the production Android repository is in scope.

**Exit criterion**: repository tests prove the fallback-only transport contract
with a Go daemon side and Go mobile-simulator side, including Relay-opaque
packet forwarding. Android production runtime compatibility remains Step 6.

### 1.3 — Direct Path + STUN + Degradation

- Self-hosted STUN listener in the Relay binary and Compose deployment.
- `rendezvous_open` / `rendezvous_hint` / `rendezvous_close` live-only Relay control-plane events.
- Go simulator and daemon-side direct UDP QUIC path with a 3s direct attempt default.
- On direct timeout/failure, transition to the fallback path from 1.2 using a fresh relay QUIC handshake and the same session protocol.
- Path badge data via `hello.path_kind` and `path_state`.

**Repository exit criterion**: repository tests prove controlled local direct success and direct-timeout fallback with a Go daemon side and Go mobile-simulator side, while Relay remains content-opaque.

**Production measurement gate**: before claiming production Android direct-path completeness, cone-NAT deployments should achieve ≥ 80% direct success on a measurement panel of ≥ 20 test pairings; symmetric NATs must cleanly fall back. That production validation is Step 6/Step 7 work.

---

## What Is Explicitly Out Of Phase 1

- UDP relay (deferred per D1 TODO).
- Daemon-side per-session ACL (single-user assumption).
- Session-level tier gates and per-session entitlement tokens / leases (replaced by D3).
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
- `ux/subscription.md` — Free / Pro computer-count product rule.
- `ux/android.md` — Android client behavior reference.
- `reference/decision-record.md` — historical decision rationale.
- `_archive/2026-04-26-architect-review.md` — earlier review record.
