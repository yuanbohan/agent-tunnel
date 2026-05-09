---
title: "feat: Finish mobile pairing readiness"
type: feat
status: proposed
date: 2026-05-06
origin: planning discussion for mobile pairing development
---

# feat: Finish mobile pairing readiness

## Summary

Finish the Relay and computer-side connectivity work required before the mobile client builds its production pairing flow. This plan intentionally prioritizes functional readiness: real QR pairing, client-neutral naming, simpler REST resources, keeping the existing login plus agent-token creation flow, and a usable computer session transport path. Security hardening that is not required for the first functional pass is recorded as explicit TODO/FIXME work rather than implemented in this phase.

---

## Problem Frame

The current connectivity pairing foundation is close enough for Go-side tests and early Android experiments, but it is not yet ready as the public contract for mobile development:

- The pairing payload is printed as signed JSON; there is no real QR rendering flow.
- Public protocol and API names still expose Android-specific terms even though the product must support iOS and other future clients.
- App-facing REST resources still use legacy device/session verbs that make mobile implementation more complex than necessary.
- Agent-token creation and login flow should stay unchanged in this version so mobile and computer clients can ship core pairing/connectivity first.
- The real broker-backed interactive transport still needs to send useful session snapshots and live bytes over the connectivity path.

---

## Product Vocabulary

- **Computer:** A trusted desktop/laptop/server running the Tunnel daemon and owning local sessions.
- **Client device:** A phone, tablet, or other app client. Android is the first implementation, not the protocol name.
- **Trusted client:** A client device that completed SAS-confirmed pairing with a computer.
- **Agent token:** The existing long-lived bearer credential used by computer-side processes. Keep this name for now.

---

## Requirements

- R1. Pairing invitations must be scannable with a real QR code.
- R2. Pairing and connectivity protocol fields must use client-neutral names, not Android-only names.
- R3. Relay app-facing REST APIs should expose simple resources: computers, sessions, and pairing responses.
- R4. Mobile app sessions may continue creating agent tokens in this version.
- R5. `tunnel auth login` should keep the existing login interface and continue creating its own agent token through the current flow.
- R6. App realtime connectivity should carry presence/path/rendezvous/fallback events, not one-shot pairing response submission.
- R7. Computer-side connectivity transport must support session index, preview/snapshot, live bytes, input, resize, and reconnect well enough for mobile implementation.
- R8. Legacy Relay attach APIs may remain for existing clients, but new mobile connectivity must not depend on `/api/sessions/:id/attach/ws`.
- R9. Security restrictions that are not implemented in this functional phase must be documented as TODO/FIXME items so they are visible before production hardening.

---

## Scope Boundaries

- Do not preserve backwards compatibility for Android-specific connectivity field names.
- Do not rename `agent token` in this phase.
- Do not change agent-token creation permissions in this version. Mobile and computer both keep the current creation capability.
- Do not move trusted-client rosters into durable Relay state. Computer/daemon local trust remains authoritative.
- Do not make Relay content-aware. Relay may route opaque encrypted packets and control metadata, but it must not derive terminal semantics from terminal bytes.
- Do not implement broad auth hardening in this phase unless it is necessary to avoid an immediate credential leak. Add TODO/FIXME and documentation instead.

---

## API Direction

### App REST API

Prefer this app-facing shape for the mobile connectivity path:

- `GET /api/account/policy`
- `GET /api/computers`
- `POST /api/computers/:computerID/sessions`
- `DELETE /api/sessions/:sessionID`
- `POST /api/pairing/responses`
- `POST /api/agent-tokens`
- `GET /api/agent-tokens`
- `DELETE /api/agent-tokens/:tokenID`

### Realtime API

- `GET /api/connectivity/ws`: app-authenticated realtime channel for computer presence, path state, rendezvous hints, and fallback tunnel coordination.
- `GET /connectivity/computer/ws`: agent-token-authenticated computer realtime channel replacing the public naming of daemon-specific connectivity routes.
- `GET /connectivity/tunnel/ws`: opaque fallback packet tunnel, if the existing route remains useful.

### Agent Token Creation Flow

Keep the current token creation flow for this version:

- `POST /api/auth/login` for account login
- `POST /api/agent-tokens` for token creation

Computer-side and mobile clients can both use this current route pair while we prioritize pairing/connectivity core functionality.

---

## Breaking Field Renames

Use client-neutral names in new mobile-facing contracts:

- `daemon_id` / `device_id` -> `computer_id`
- `daemon_public_key` -> `computer_public_key`
- `daemon_fingerprint` -> `computer_fingerprint`
- `android_fingerprint` -> `client_fingerprint`
- `android_pubkey` -> `client_public_key`
- `android_display_name` -> `client_display_name`
- `device_fingerprint` on app auth -> `client_fingerprint`
- `trusted_devices` -> `trusted_clients`
- `paired_device_visible` -> `computer_visible`
- `paired_device_removed` -> `computer_removed`
- `paired_device_revoked` -> `client_revoked`

Implementation may keep internal package names such as `daemon` where they are clearly local process concepts. Public API, docs, protocol payloads, and mobile-facing test clients should use the new vocabulary.

---

## Security TODOs

These are required before a production security signoff, but they should not block the first functional implementation unless the implementation would otherwise expose plaintext secrets.

- TODO(auth): Add login and agent-token creation throttling. Use at least `IP + username` keyed failure accounting, generic errors, and retry guidance.
- TODO(auth): Revisit whether agent-token creation should be split by client type in a later hardening phase.
- TODO(auth): Audit agent-token creation and revocation with actor type, token id/name, IP, and user-agent. Never log plaintext token values, password values, or token digests.
- TODO(auth): Verify plaintext agent tokens are returned only once and never written to structured logs.
- TODO(auth): Keep agent-token scope narrow: computer sockets and computer-owned session operations only. Agent tokens must not create app sessions, create other agent tokens, change account credentials, or submit pairing responses.
- TODO(auth): Decide whether `tunnel auth logout` should support or default to server-side token revocation when the local token id is known.
- TODO(auth): Decide whether long-lived agent tokens need expiration or rotation policy. First pass may keep current indefinite tokens if revoke/list/audit are available.
- TODO(api): Reject credentials and bearer tokens in query strings for new auth routes.
- TODO(ops): Document TLS-only production deployment expectations for all auth and pairing routes.

---

## Implementation Units

- U1. **Real QR pairing**

**Goal:** Make `tunnel daemon pair` render a scannable pairing QR instead of only printing JSON.

**Approach:**
- Keep the signed pairing invitation payload as the canonical data.
- Add terminal QR rendering for CLI pairing.
- Preserve a machine-readable output mode for tests and non-interactive flows.
- Update pairing docs with scan flow, expiry, and SAS confirmation expectations.

**Verification:**
- Unit test invitation encoding remains stable.
- Manual verification that the QR can be scanned by a real mobile camera/scanner.

---

- U2. **Client-neutral pairing and connectivity contract**

**Goal:** Remove Android-only public names from pairing, Relay visibility, and connectivity protocol payloads.

**Approach:**
- Rename public request/response/event fields to `client_*`, `computer_*`, and `trusted_clients`.
- Keep Android-only wording only in Android implementation notes.
- Update Go tests and simulators to use generic client naming.

**Verification:**
- `rg 'android|Android|trusted Android|paired device' docs internal` should only find historical docs, Android-specific implementation notes, or internal compatibility comments.

---

- U3. **REST API simplification**

**Goal:** Make app-facing Relay APIs read as direct resources rather than transport implementation details.

**Approach:**
- Add or document the preferred resource routes under `/api/computers`, `/api/computers/:id/sessions`, `/api/sessions/:id`, and `/api/pairing/responses`.
- Keep legacy routes only as compatibility aliases if needed.
- Move pairing response submission out of the app realtime channel.

**Verification:**
- `docs/api.md` describes one primary mobile path.
- Tests cover the primary routes, not only legacy aliases.

---

- U4. **Keep current login plus token flow**

**Goal:** Keep auth behavior stable while pairing/connectivity core features ship.

**Approach:**
- Keep `POST /api/auth/login` and `POST /api/agent-tokens` as the active creation flow.
- Keep `tunnel auth login` behavior unchanged for this version.
- Keep mobile app behavior unchanged for this version.
- Add TODO/FIXME comments or docs for throttling, audit, permission split, and rotation gaps that are not implemented in the functional pass.

**Verification:**
- Mobile app access tokens can still create agent tokens via the current route.
- `tunnel auth login` still produces a usable local auth token for computer-side processes using the current flow.

---

- U5. **Connectivity transport completion**

**Goal:** Make the new mobile connectivity path usable without relying on legacy Relay attach.

**Approach:**
- Complete broker-backed snapshot/live-byte forwarding over the connectivity transport.
- Ensure session index, preview subscription, interactive attach, input, resize, and reconnect flows use the computer transport.
- Preserve Relay content-opacity: Relay routes control and opaque packets only.

**Verification:**
- Focused Go tests cover session index, launch, interactive snapshot, live bytes, input, resize, reconnect, and disconnect behavior.
- Existing legacy attach tests keep passing.

---

- U6. **Docs and handoff**

**Goal:** Give mobile implementers one current contract to build against.

**Approach:**
- Update `docs/api.md`, `docs/protocol.md`, `docs/architecture.md`, and daemon/connectivity docs for the new primary flow.
- Clearly mark legacy routes as legacy where they remain.
- Keep security TODOs visible in docs until implemented.

**Verification:**
- Mobile-facing docs do not require reading old Android-specific plans to understand pairing.
- Current tests and docs agree on names and route shapes.

---

## Suggested Build Order

1. U2 client-neutral contract renames, because mobile should not build against Android-only protocol names.
2. U3 REST simplification, because it defines the app-facing shape.
3. U1 real QR pairing, because it unlocks real scan UX.
4. U5 connectivity transport completion, because it makes the paired path useful.
5. U4 keep current login plus token flow, to avoid auth churn during core feature delivery.
6. U6 docs and handoff updates throughout the implementation.

---

## Verification Commands

- `go test ./internal/connectivity/pairing ./internal/connectivity/pairtest ./internal/protocol`
- `go test ./internal/relay/...`
- `go test ./internal/tunnel/daemon`
- `go test ./...`
- Manual QR scan with at least one real mobile scanner/client.
