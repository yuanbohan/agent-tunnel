# Architect Review and Adjustments — 2026-04-26

## Status

This document is a point-in-time architectural review of the QUIC session-connectivity design under `docs/connectivity/` performed on 2026-04-26.

Its purpose is to make the review reproducible: every issue identified is paired with the change applied and the file/section that absorbed it.

It does not redefine any decision. It is a record, not a contract.

**This file is historical context only.** Do not use it as the current contract. The canonical phase-1 contract is `docs/connectivity/contract.md`.

Conclusions in this review were superseded multiple times:

- The account-global selection/token model was replaced by an oldest-live-session app-side rule, then replaced again by **sticky first-attach** (see `contract.md` D3 and `ux/subscription.md`).
- References below to leases, selection epochs, signing keys, access tokens, or `session_activate` are historical only.
- Path references using the old flat layout (e.g. `docs/connectivity/transport-protocol.md`) refer to files that now live under `protocol/` and `ux/` per the README index.

## Scope Of Review

The review evaluated the following documents from five angles: best practices, maintainability, user experience, security, and implementation robustness.

Documents in scope:

- `docs/connectivity/architecture.md`
- `docs/connectivity/decision-record.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/pairing-protocol.md`
- `docs/connectivity/daemon-session-sync.md`
- `docs/connectivity/daemon-preview-generation-rules.md`
- `docs/connectivity/mobile-reference.md`
- `docs/connectivity/android-client-behavior.md`
- `docs/connectivity/sequence-flows.md`
- `docs/brainstorms/2026-04-23-direct-attach-control-plane-requirements.md` (origin)

Adjacent reference checks: previous WebRTC-direction plan (now superseded), CLAUDE.md docs expectations.

## Overall Assessment

The QUIC + pinned-pubkey-TLS architecture is sound. Compared with the previous WebRTC direction, this revision closes the previously-identified critical issues:

- WebRTC signaling-server MITM (DTLS fingerprint substitution) is closed by pairing-pinned TLS.
- 6-digit SAS confirmation closes the pairing-transport-trust gap.
- Two-DataChannel + `stream_epoch` complexity is replaced by one control stream plus per-session interactive QUIC streams.
- Relay-owned session metadata is replaced by daemon-pushed metadata over the control stream.
- Relay-mediated interactive lease state machine is removed; daemon decides locally.
- Cronet (HTTP-only) is correctly identified as not the right Android transport anchor.

All seventeen brainstorm requirements (R1–R17) are satisfied by design.

The remaining issues at review time were not architectural; they were specification-precision gaps and one missing product surface (subscription model). Those gaps were the focus of the adjustments below.

## Method

For each finding I record:

- **ID** — short identifier used in the review summary message
- **Issue** — what was missing or ambiguous
- **Decision** — what specific resolution was chosen
- **Change** — which file and section was modified, or which file was created

Findings are grouped by the dimension they primarily affect.

---

## Security Findings

### S1 — TLS Pinning Mechanism Was Underspecified

**Issue:** The original docs said "QUIC with TLS 1.3 + peer verification by pinned device public-key fingerprint" but did not specify how pinning is implemented. TLS 1.3 has no built-in public-key pinning mode, and there are at least two valid implementations. Independent daemon (Go) and Android (Rust JNI) implementations could end up incompatible.

**Decision:** Phase 1 fixes the mechanism: each side presents a self-signed Ed25519 X.509 certificate whose `SubjectPublicKeyInfo` is its pairing-established Ed25519 device public key. Standard chain validation is bypassed; a custom verifier compares peer cert SPKI bytes against the pinned fingerprint. `NotBefore`/`NotAfter` are intentionally ignored.

**Change:** `transport-protocol.md` → new section "Concrete Cert Pinning Mechanism" under Security Model.

### S2 — SAS Algorithm And Inputs Were Unspecified

**Issue:** SAS inputs disagreed across docs (`pairing-protocol.md` did not include nonce; `sequence-flows.md` flow 1 did). No hash algorithm or output extraction was defined. Two implementations would never produce matching codes.

**Decision:** SAS is `first 4 bytes of SHA-256(canonical) mod 1_000_000`, displayed as zero-padded 6 decimal digits. Canonical input is length-prefixed concatenation of `daemon_pubkey || android_pubkey || invitation_id || nonce` with `len_u16_be` prefixes. Test vectors are required in implementation suites.

**Change:** `pairing-protocol.md` → "SAS Confirmation" section completely rewritten with Inputs, Algorithm, Test Vectors, UX Rules, and Why The Code Cannot Be Auto-Compared subsections. The "Why SAS Prevents MITM" section was updated to list `nonce` explicitly as an input.

### S3 — SAS Click-Fatigue Was Unaddressed

**Issue:** If 99% of pairings succeed, users will train themselves to tap "match" without checking. The doc had no UX rule to prevent this.

**Decision:** Phase 1 mandates active confirmation: at least 1 second between display and confirmation, no auto-prefocus, no auto-match. A small instructional line is recommended.

**Change:** `pairing-protocol.md` → "UX Rules" subsection within SAS Confirmation.

### S4 — Downgrade Capability Was Implicit

**Issue:** A misbehaving Relay can manipulate rendezvous hints to force fallback. This is acceptable (confidentiality is preserved) but the original docs did not name it, which would invite future readers to misread it as a vulnerability.

**Decision:** Acknowledge explicitly in both architecture and relay-protocol docs that Relay can force-downgrade the path but cannot break confidentiality.

**Change:** `architecture.md` → new section "Acknowledged Downgrade Capability" under Security Walkthrough; `relay-protocol.md` → new section "Acknowledged Downgrade Capability".

### S5 — Daemon Key Compromise Recovery Was Missing

**Issue:** Docs covered Android-side revoke but said nothing about what happens if a daemon's Ed25519 device key is exfiltrated.

**Decision:** No in-band recovery in phase 1. Operationally: rotate the daemon device key and re-pair every Android device. Daemon device keys should be treated like SSH host keys.

**Change:** `architecture.md` → new section "Daemon Key Compromise" under Security Walkthrough; `pairing-protocol.md` → new section "Daemon Key Compromise Recovery".

### S6 — private_udp_addrs Hygiene Was Unspecified

**Issue:** Daemon and Android could broadcast arbitrary network interface addresses to Relay through `private_udp_addrs`, expanding information disclosure beyond what NAT hairpinning needs.

**Decision:** Restrict to RFC1918 / RFC4193 / link-local ranges and cap the list at 4 entries.

**Change:** `relay-protocol.md` → "private_udp_addrs Hygiene" subsection within Rendezvous.

### S7 — No Consolidated Threat Model Table

**Issue:** Security claims were scattered across multiple sections, making it hard for a reviewer to verify coverage.

**Decision:** Add a single threat model table to `architecture.md` summarizing each threat, defense, and residual risk.

**Change:** `architecture.md` → new "Threat Model Summary" section under Security Model.

---

## Maintainability Findings

### M1 — Android QUIC Library Selection Still Pending

**Issue:** `decision-record.md` lists Cloudflare quiche as an "investigation target" without committing to it. Implementation is blocked on this choice.

**Decision:** This review recommends quiche as the default with a small interoperability spike against quic-go before implementation. The decision record itself was not edited because the choice has practical implications (binary size, JNI complexity) that the user should sign off explicitly. The review record carries this as an open implementation decision; see "Open Items" below.

**Change:** Not yet applied to docs. Listed in Open Items.

### M2 — Control Stream Frame Ordering Was Not Pinned

**Issue:** `session_index` must arrive before any `session_upsert`/`session_gone`, but nothing in the docs required the daemon implementation to serialize emissions through one writer. A multi-goroutine writer would race.

**Decision:** Mandate a fixed ordering after `hello`: `session_index` first, then deltas. Recommend single-writer-goroutine pattern.

**Change:** `transport-protocol.md` → new "Control Stream Frame Ordering" subsection.

### M3 — `interactive_stream_id` Namespace Was Ambiguous

**Issue:** Docs did not say whether `interactive_stream_id` is a QUIC stream id or a protocol-level id, who allocates it, or whether the stream is bidirectional.

**Decision:** It IS the QUIC stream id. Interactive streams are server-initiated unidirectional (daemon→Android). Input flows back on the bidirectional control stream.

**Change:** `transport-protocol.md` → "Stream Roles And Direction" subsection added; `interactive_granted` field documentation updated.

### M4 — Direct Vs Fallback Race Was Undefined

**Issue:** Sequential vs happy-eyeballs not chosen; no timeout written.

**Decision:** Sequential with `3s` direct attempt deadline. Loser of the race is canceled. Configurable but default fixed for cross-deployment comparability.

**Change:** `architecture.md` → "Direct And Fallback Strategy" updated; `transport-protocol.md` → new "Direct Attempt Deadline" subsection; `sequence-flows.md` → flow 3 timeout note made explicit.

### M5 — Reconnect Backoff Was Missing

**Issue:** No backoff rule. Multi-daemon environments would synchronize retries on connectivity events.

**Decision:** Exponential backoff with full jitter, base 1s, cap 60s, per-daemon-independent.

**Change:** `architecture.md` → "Reconnect Backoff" added in Direct And Fallback Strategy section; `transport-protocol.md` → new "Reconnect Backoff" subsection.

### M6 — Backpressure Isolation Was Unspecified

**Issue:** A slow Android consumer on one stream could stall the daemon's per-session output if the daemon implementation shared a writer.

**Decision:** Daemon must isolate per-session write paths so that one stream's backpressure cannot block the others.

**Change:** `architecture.md` → new "Liveness And Backpressure" section.

### M7 — ALPN Was Missing

**Issue:** Without an ALPN identifier, the QUIC service could be confused with HTTP/3 or other QUIC services on shared infrastructure.

**Decision:** ALPN is `tunnel-conn/1`. Connections that do not negotiate this ALPN are rejected.

**Change:** `transport-protocol.md` → new "ALPN" subsection within Security Model.

### M8 — QUIC Liveness Strategy Was Unspecified

**Issue:** Docs did not say whether application-layer heartbeats were needed.

**Decision:** Rely on QUIC PING. Idle timeout 30s, keep-alive interval 15s. No application heartbeat.

**Change:** `architecture.md` → "Liveness And Backpressure" section; `transport-protocol.md` → "QUIC Transport Parameters" table.

---

## UX Findings

### U1 — Daemon-Visible-But-No-Sessions Loading State Was Vague

**Issue:** Architecture allowed a multi-second gap where the daemon card is visible but the session list is not yet populated.

**Decision:** Show daemon card with `Loading sessions…` subtitle; reflect transport state on the card; phase 1 does not display stale cached session counts.

**Change:** `mobile-reference.md` → new "Daemon-Visible-But-No-Sessions Loading State" subsection within Session Bootstrap.

### U2 — Path Badge Semantics Were Implicit

**Issue:** A "Direct" / "Relay" badge could be misread as "Relay = less secure".

**Decision:** Document explicitly: badge is informational and indicates expected latency only; both paths have identical encryption; UI copy MUST NOT imply Relay is less secure.

**Change:** `architecture.md` → "Path Badge Semantics" subsection added; `mobile-reference.md` → new "Path Badge Semantics" section; `android-client-behavior.md` → Path Badge section expanded.

### U3 — Multi-Interactive UI Focus Was Underspecified

**Issue:** Multiple interactive sessions are allowed, but the docs did not require unambiguous keyboard focus, opening the door to misrouting input.

**Decision:** UI must show one focused terminal at a time with a clear visual indicator. Daemon also drops input frames whose `session_id` is not currently granted, but the app must not rely on that as a primary safeguard. `session_id` field made mandatory on every input frame.

**Change:** `mobile-reference.md` → new "Multi-Interactive UI Focus" section; `transport-protocol.md` → input frame semantics tightened ("session_id is mandatory; daemon MUST drop input for non-granted sessions").

### U4 — Pairing Error Codes Were Missing

**Issue:** Failure rules listed only "fail closed" with no actionable code surface.

**Decision:** Phase-1 stable enum: `pairing_invitation_expired`, `pairing_invitation_invalid`, `pairing_invitation_consumed`, `pairing_account_mismatch`, `pairing_relay_unreachable`, `pairing_signature_failed`, `pairing_sas_mismatch`, `pairing_unknown_error`.

**Change:** `pairing-protocol.md` → new "Pairing Error Codes" subsection within Failure Rules.

---

## Subscription Model

### Sub1–Sub4 — Subscription Model Was Entirely Missing

**Issue:** The user explicitly requested subscription support, but no document covered where entitlement is enforced, what is gated, what happens on lapse, or how errors are surfaced.

**Decision:** Created a new dedicated doc covering enforcement location, gated actions, lapsed-subscription degradation behavior, error envelope, and security non-interference.

**Change:** New file `docs/connectivity/subscription-model.md`. Added to references in `architecture.md`.

Key principles locked in:

- entitlement enforced only at Relay
- direct path is never gated (no Relay cost)
- pairing counts and fallback tunnel issuance are the gating dimensions
- lapsed subscriptions degrade gracefully (existing pairings keep working)
- subscription state MUST NOT influence cryptographic decisions

This first-pass subscription direction was later superseded by the active-session lease model documented in the "Second Review Pass" section below and in `docs/connectivity/subscription-model.md`. It is kept here only as historical traceability for how the design evolved.

---

## Implementation Robustness Findings

### R1 — STUN Deployment Specifics Were Vague

**Issue:** Self-hosted STUN was named but with no concrete deployment shape.

**Decision:** Per-region listener on UDP `3478`; hostname convention `stun.<relay-domain>`; classic RFC 5389/8489 Binding only; no ICE features; no public third-party STUN in production.

**Change:** `relay-protocol.md` → new "STUN Deployment Shape" subsection within "What STUN Does And Does Not Do".

### R2 — `attempt_id` Lifecycle Was Undefined

**Issue:** No rules for duplicate attempts, fallback token reuse, or expiry.

**Decision:** UUID minted by Android per attempt; new attempts within 5s supersede older ones; rendezvous hints expire 30s after `rendezvous_open`. Fallback tunnel tokens are not carried inside rendezvous hints. They are issued only after direct failure, one single-use token per side, each bound to `attempt_id` and actor identity.

**Change:** `relay-protocol.md` → new "attempt_id Rules" subsection within Rendezvous and updated "Fallback Relay Setup" section; `sequence-flows.md` → flow 3 updated to request side-specific fallback tokens only after direct timeout.

### R7 — Startup Snapshot Redundancy

**Issue:** `daemon_snapshot` and `pairing_snapshot` overlapped on the app side and would have forced the Android client to reconcile two near-identical stores for daemon visibility.

**Decision:** Remove `pairing_snapshot` from startup. App startup now receives `daemon_snapshot` plus `realtime_ready`. Pairing remains incremental through `paired_device_visible` and `paired_device_revoked`.

**Change:** `relay-protocol.md` → startup snapshot section simplified; `mobile-reference.md` and `sequence-flows.md` updated accordingly.

### R8 — `interactive_capable` Was An Unstable Hint

**Issue:** `interactive_capable` looked like a real-time promise even though the daemon remains free to grant or deny interactive attach per session at request time.

**Decision:** Remove `interactive_capable` from phase-1 session metadata. The true capability check is `interactive_request` followed by `interactive_granted` or `interactive_denied`.

**Change:** `daemon-session-sync.md` → session metadata field list simplified.

### R3 — QUIC Transport Parameters Were Not Listed

**Issue:** Without a listed parameter set, daemon and Android could advertise different limits and break under load.

**Decision:** Document the phase-1 baseline: `max_idle_timeout=30s`, `keep_alive=15s`, `max_incoming_streams=64` server, `max_data=16MB`, `max_stream_data=1MB`.

**Change:** `transport-protocol.md` → new "QUIC Transport Parameters" subsection within Security Model.

### R4 — Test Vectors Are Still Pending

**Issue:** Security primitives need golden test vectors so independent implementations can verify alignment.

**Decision:** SAS algorithm is precise enough to produce vectors; vector byte values will be filled in once daemon and Android implementations exist. Cert pinning and tunnel forwarding test scenarios will be added in implementation phase.

**Change:** `pairing-protocol.md` → "Test Vectors" subsection added with the requirement; actual vector bytes are deferred to implementation.

### R5 — Naming Inconsistencies

**Issue:** `relay-protocol.md` used `paired_device_revoked` but `pairing-protocol.md` did not. `sequence-flows.md` referenced `paired_device_visible` not declared in `relay-protocol.md`. SAS inputs disagreed between docs.

**Decision:** Add `paired_device_visible` and `paired_device_revoked` to the canonical Relay event family with explicit responsibilities. Unify SAS input list to include nonce.

**Change:** `relay-protocol.md` → Pairing event responsibilities expanded with both event names; `pairing-protocol.md` → SAS Confirmation rewritten with nonce included; `pairing-protocol.md` → "Why SAS Prevents MITM" updated to list nonce.

### R6 — Protocol Versioning Policy Was Missing

**Issue:** `hello.protocol_version` carried no rules for mismatch handling, unknown frame types, or what counts as breaking.

**Decision:** Single integer; mismatch closes connection with `protocol_version_mismatch` error; unknown frame types and unknown fields silently ignored; phase 1 = v1.

**Change:** `transport-protocol.md` → new "Protocol Versioning" section.

---

## Open Items

These items were identified but not closed by this review pass. Each is annotated with its current state as of the user disposition recorded on 2026-04-26.

### O1 — Android QUIC Library Final Decision — RESOLVED 2026-04-26

User confirmed **Cloudflare quiche via JNI** as the phase-1 default. `kwik` is retained as a fallback only if quiche packaging proves unworkable. This is now recorded in `docs/connectivity/decision-record.md` under "Selected Android QUIC Library", along with the phase-0 interop spike requirement.

### O2 — SAS Test Vector Bytes — DEFERRED TO IMPLEMENTATION

This is not a design decision. Test vectors are byte-level outputs that the SAS algorithm in `pairing-protocol.md` produces for chosen inputs; they are recorded in test suites once a working implementation exists so that daemon and Android can verify byte-for-byte agreement.

Process expectation:

- during phase-0 implementation, the first side to implement the SAS algorithm picks a small set of representative inputs (e.g., 3–5 cases covering normal pubkeys, edge-length invitation_ids, and short/long nonces) and computes the canonical SAS for each
- those input/output pairs are committed to the daemon-side Go test suite as golden vectors
- the Android implementation is required to produce the same outputs against the same inputs before integration testing

The user does not need to decide anything here at design time.

### O3 — CLAUDE.md / README / AGENTS.md Cascading Update — DEFERRED 2026-04-26

User decision (2026-04-26): do not update project-root canonical surfaces yet.

TODO before phase-1 implementation work begins:

- add a "Connectivity (in design)" pointer to `CLAUDE.md` Start Here referencing `docs/connectivity/architecture.md`
- mention the connectivity direction in `README.md` so external readers understand the current attach path is legacy
- update `AGENTS.md` with the same scope language used in `CLAUDE.md`

This follow-up is intentionally not done now because the connectivity design is still pre-implementation; updating canonical surfaces too early risks describing behavior that does not exist yet.

### O4 — Subscription Tier Numbers — DEFERRED, UNDER DISCUSSION 2026-04-26

User has not yet decided concrete tier limits. The `subscription-model.md` document deliberately leaves numbers out so that the architecture is not coupled to a specific business model.

A separate discussion is needed to choose at minimum:

- whether the free tier exists primarily as a trial or as a generous personal-use baseline
- how many paired daemons / paired Android devices each tier allows
- whether fallback relay is metered by bandwidth, by minutes, by concurrent tunnels, or by a combination
- how the user is informed when they approach a limit

The connectivity protocol layer is unaffected by these choices; only Relay-side enforcement and Android UI copy are.

### O5 — Re-Generated Implementation Plan — DEFERRED 2026-04-26

User decision (2026-04-26): do not generate a new program plan yet. The connectivity doc set should stabilize first.

When ready, the new plan should be broken into phases (pairing → presence/rendezvous → direct QUIC → fallback tunnel → interactive streams → legacy retirement) and live under `docs/plans/` with a fresh date.

---

## Second Review Pass — 2026-04-26 (Late)

After the user redesigned the subscription model around active-session leases and committed the docs, a second full review was performed. New findings (C1–C8 critical and I1–I10 important) were identified and resolved. User dispositions for the Q1–Q9 follow-up questions are recorded here.

### User Dispositions On Open Questions

| Question | Disposition |
|---|---|
| Q1 — lease grace window | **5 minutes** at second-pass review time; later superseded by the third-pass "no additional daemon-side grace" cleanup below |
| Q2 — lease token format | **JWT (EdDSA / Ed25519)** with standard RFC 7519 claims plus `device`, `session`, `jti` |
| Q3 — Pro upgrade payment flow | **Deferred** — kept as TODO; `upgrade_url` is empty in phase 1 |
| Q4 — multi-daemon QUIC strategy | **Eager-with-idle-tear-down** — connect every paired online daemon; tear down background daemon QUIC after 60s of UI inactivity |
| Q5 — auto-release on detail exit | **Manual release only** — preserve "instant return" experience; explicit release controls required |
| Q6 — invitation TTL | **5 minutes** default; not user-configurable in phase 1 |
| Q7 — daemon device key storage | **`~/.tunnel/identity.json` mode 0600** in phase 1; OS keyring deferred |
| Q8 — pair-daemon limit on free | **Not limited** — pairing does not consume Relay resources |
| Q9 — `daemon_register` event | **Yes** — daemon emits explicit register event with display, platform, and version metadata |

### Critical Findings (C1–C8) Closed

#### C1 — Lease Signing Key Distribution Was Unspecified

**Superseded:** The current phase-1 design no longer uses daemon-validated lease tokens or signing-key distribution. The notes below remain as historical context only.

**Issue:** Daemon must verify Relay-issued lease tokens but the doc did not say how daemon obtains Relay's signing public keys.

**Decision:** Relay pushes a `lease_signing_keys` event over the daemon-side WebSocket immediately after `daemon_register` and on every key rotation. Daemon persists the most recent keyset to disk; rolling rotation is supported via `kid` headers. Tokens with unknown `kid` are rejected. A daemon that has never received a keyset MUST refuse all `session_activate`.

**Change:** `relay-protocol.md` → new "Lease Signing Key Distribution" section; daemon-side startup sequence updated to include `lease_signing_keys`.

#### C2 — Relay Outage Behavior For Lease Renewal Was Undefined

**Issue:** With strict `exp` enforcement, a 30-second Relay hiccup would disconnect every active user.

**Decision:** Daemon-side grace window of `5 minutes` beyond `exp`. Daemon accepts an unexpired-or-within-grace token; Relay never honors grace itself when issuing renewals. This was later superseded by the third-pass cleanup that removed the extra grace window entirely.

**Change:** `relay-protocol.md` → grace window listed in lease defaults; `subscription-model.md` → new "Lease Grace Window" section detailing semantics and lapsed-subscription interaction. Both were later replaced by the "No Additional Grace Window" rule in the current contract docs.

#### C3 — Lease Token Format Was Unspecified

**Issue:** Token said "signed" but did not pin algorithm, container, or claims. Two implementations would not agree.

**Decision:** Phase-1 token is **JWT, EdDSA (Ed25519)**, with claims `iss`, `sub`, `aud`, `device`, `session`, `iat`, `exp`, `jti`. Daemon validation order is fully specified. A worked example is included.

**Change:** `subscription-model.md` → new "Lease Token Format" section with full claim table and example.

#### C4 — Daemon-Side WebSocket Event Catalog Was Incomplete

**Issue:** Documents named individual daemon-side events in scattered places but no catalog existed; daemon implementers would not know the full surface area.

**Decision:** Added an authoritative "Daemon-Side Event Catalog" listing what daemon sends and what daemon receives.

**Change:** `relay-protocol.md` → new "Daemon-Side Event Catalog" section.

#### C5 — Stale Legacy Event Names — VERIFIED CLEAN

`pairing_snapshot`, `session_index_snapshot`, `connection_state_snapshot`, `pairing_state_snapshot` are absent from all functional docs. Only this review record references them historically.

#### C6 — Invitation Persistence Was Unspecified

**Issue:** `tunnel daemon pair` mints one-time invitations, but daemon restart between mint and consume could allow invitation replay.

**Decision:** Daemon persists invitation roster (`invitation_id`, `nonce`, `correlation_id`, `expires_at`, `consumed`) to disk; reload on startup; reject expired or consumed; sweep removes records past `expires_at`. Default invitation TTL is `5 minutes`.

**Change:** `pairing-protocol.md` → new "Invitation Persistence" section.

#### C7 — QUIC 0-RTT Was Not Explicitly Disabled

**Issue:** quic-go enables 0-RTT by default. Replay of 0-RTT data could re-execute destructive shell commands.

**Decision:** 0-RTT is DISABLED on both server and client in phase 1.

**Change:** `transport-protocol.md` → QUIC Transport Parameters table includes 0-RTT row; new "0-RTT Is Disabled" subsection explains rationale.

#### C8 — Pro Upgrade Payment Flow Selection — DEFERRED

**Issue:** `upgrade_url` was named but no flow was chosen.

**Decision (per user):** Defer payment work entirely. `upgrade_url` is an empty string in phase 1. App displays the upgrade explanation as text only with no clickable link.

**Change:** `subscription-model.md` → new "Payment Flow Deferred" section with concrete phase-1 expectations; error envelope example updated; `upgrade_url` documented as empty in phase 1.

### Important Findings (I1–I10) Closed

#### I1 — Multi-Daemon Resource Strategy

**Decision:** Eager-with-idle-tear-down. Foreground and most-recently-used daemon stay connected; other paired-online daemons tear down their QUIC connection after `60s` of UI inactivity. Relay realtime WS remains open.

**Change:** `mobile-reference.md` → new "Multi-Daemon Connection Strategy" subsection.

#### I2 — Server-Side Rate Limits Missing

**Decision:** Phase-1 defaults: rendezvous 10/min/account, lease 30/min/account, pairing-response 10/min/device. Errors include `retry_after_seconds`.

**Change:** `relay-protocol.md` → new "Server-Side Rate Limits" section.

#### I3 — STUN Retry Policy

**Decision:** 3 attempts with 500ms / 1s / 2s timeouts; on full failure fall back to relay tunnel directly.

**Change:** `transport-protocol.md` → new "STUN Retry Policy" subsection.

#### I4 — Symmetric NAT Acknowledgment

**Decision:** Phase 1 does not traverse symmetric NATs; it falls back to relay tunnel. This is documented as expected behavior, not a defect.

**Change:** `transport-protocol.md` → new "NAT Traversal Limitations" subsection.

#### I5 — Lease Token Renewal Overlap

**Decision:** Renewals share `jti`, get strictly later `exp`. Daemon accepts any unexpired-with-grace token; brief overlap during renewal is allowed.

**Change:** `subscription-model.md` → new "Renewal Token Overlap" section.

#### I6 — Account Switch Behavior

**Decision:** Local pairing trust is daemon-scoped and persists across logout/account-switch. Relay-derived visibility is account-scoped and clears on logout. Account removal (not just logout) clears local pairing fingerprints.

**Change:** `mobile-reference.md` → new "Account Switch Behavior" section.

#### I7 — Lease Release UX

**Decision:** No auto-release on detail-view exit. Explicit "Release this session" control required. Modal on locked-session tap names both sessions and offers explicit "Release X and activate Y".

**Change:** `subscription-model.md` → "Auto-Release Policy" subsection added under Lease Release; `mobile-reference.md` → "Lease Release UX" subsection added under Subscription And Locked Sessions; `android-client-behavior.md` → Subscription Rendering updated.

#### I8 — Daemon Device Key Storage Location

**Decision:** Phase-1 stores `~/.tunnel/identity.json` mode `0600`; future phases may move to OS keyring (macOS Keychain / Linux secret-service) without protocol changes.

**Change:** `pairing-protocol.md` → "Daemon Stores" section expanded to specify storage location and mode.

#### I9 — Account-Token Theft Threat Row

**Decision:** Added explicit row to threat model table acknowledging that stolen account tokens enable daemon impersonation; SAS comparison detects the substitution; account-token confidentiality is the indirect prerequisite.

**Change:** `architecture.md` → Threat Model Summary extended.

#### I10 — Aggregated Error Codes

**Decision:** Created a single source of truth catalog covering pairing, transport, lease, relay, and QUIC error codes with handling guidance and user-facing string templates.

**Change:** New file `docs/connectivity/error-codes.md`.

### New Files Created In This Pass

- `docs/connectivity/state-machines.md` — three canonical state machines (per-daemon transport, per-session interactive, lease lifecycle) with transition rules and side mirrors
- `docs/connectivity/error-codes.md` — single-source-of-truth error catalog

### Component Overview Diagram

A component overview Mermaid diagram was added to the top of `architecture.md` so first-time readers can grasp the entire system shape (Android, operator-hosted edge, user computer) and the four communication carriers between them.

---

## Open Items (Updated)

### O1 — Android QUIC Library — RESOLVED 2026-04-26

User confirmed **Cloudflare quiche via JNI**. `decision-record.md` records this with `kwik` as fallback.

### O2 — SAS Test Vector Bytes — DEFERRED TO IMPLEMENTATION

Algorithm fully specified; bytes filled in once implementations exist.

### O3 — CLAUDE.md / README / AGENTS.md Cascade — DEFERRED 2026-04-26

TODO before phase-1 implementation begins.

### O4 — Subscription Tier Numbers — RESOLVED 2026-04-26

Replaced by single-dimension active-session lease model. Free = 1 active session, Pro = up to 10. Documented in `subscription-model.md`.

### Third-Pass Cleanup After Full-Doc Review — 2026-04-26

After the full-doc review that followed the user's broader edits, several contract-tightening changes were applied to remove implementation ambiguity:

- removed the extra daemon-side lease grace window; token validity now ends exactly at `exp`
- added Relay → daemon `lease_revoked` fan-out so explicit release / logout / account switch take effect immediately even on direct connections
- clarified that Relay owns active-session lease issuance but does not own daemon-side interactive grant / deny decisions
- relaxed the error-code naming rule so the existing short transport reason enums remain valid without contradicting the conventions section

### O5 — Re-Generated Implementation Plan — DEFERRED 2026-04-26

User decision: connectivity docs should stabilize first; new plan generated later.

### O6 — Pro Upgrade Payment Flow — DEFERRED 2026-04-26 (NEW)

User decision: defer payment. Phase 1 ships with `upgrade_url` empty and no clickable upgrade affordance. Decision between Stripe-only, Google Play Billing, or hybrid is left for a later phase.

### O7 — SAS Active Confirmation UX (NEW IN SECOND PASS)

The pairing-protocol mandates an active-confirmation pattern (≥1s delay before confirm, no auto-prefocus). Phase-1 Android implementation should validate UX testing of this pattern; the rule is documented but the actual Android UI work has not started.

---

## Files Modified

First pass (2026-04-26 morning):

- `docs/connectivity/architecture.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/pairing-protocol.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/sequence-flows.md`
- `docs/connectivity/mobile-reference.md`
- `docs/connectivity/android-client-behavior.md`

Second pass (2026-04-26 late):

- `docs/connectivity/architecture.md` (component diagram, account-token threat row, links)
- `docs/connectivity/transport-protocol.md` (0-RTT disabled, STUN retries, symmetric NAT)
- `docs/connectivity/pairing-protocol.md` (invitation persistence, daemon key storage)
- `docs/connectivity/relay-protocol.md` (lease signing keys, daemon_register, daemon-side event catalog, server-side rate limits)
- `docs/connectivity/subscription-model.md` (JWT format, current no-additional-grace rule, renewal overlap, manual-release UX, payment-deferred section)
- `docs/connectivity/mobile-reference.md` (multi-daemon strategy, account switch, lease release UX)
- `docs/connectivity/android-client-behavior.md` (subscription rendering update)

## Files Created

First pass:

- `docs/connectivity/subscription-model.md`
- `docs/connectivity/2026-04-26-architect-review.md` (this document)

Second pass:

- `docs/connectivity/state-machines.md`
- `docs/connectivity/error-codes.md`

## How To Use This Document

If a future reviewer or implementer questions a specific design choice, they should:

1. find the relevant **ID** in this document (e.g., `S1`, `M3`, `Sub1`)
2. read the **Issue** to understand what was missing
3. read the **Decision** to understand the rationale
4. read the **Change** pointer to find the doc text that absorbs the resolution

If a decision turns out to be wrong, update both the relevant doc and add a follow-up entry here. Do not silently overwrite history.

## Related Documents

- `docs/connectivity/architecture.md`
- `docs/connectivity/decision-record.md`
- `docs/brainstorms/2026-04-23-direct-attach-control-plane-requirements.md`
