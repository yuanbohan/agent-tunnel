---
date: 2026-05-14
topic: relay-cn-split-stun-compose-deployment
---

# Relay-CN Split STUN Compose Deployment

## Summary

Deploy `relay-cn` with Docker Compose using one release build artifact published under separate GHCR image names, with two independently managed runtime services: `relay` for HTTP/WebSocket Relay traffic and `stun` for Binding-only UDP STUN. Relay should be easy to update frequently, while STUN should stay pinned to a stable image tag until the STUN code or deployment contract changes.

---

## Problem Frame

The repository already has a Docker Compose deployment path for Relay and PostgreSQL, and the current Relay binary can start an embedded Binding-only STUN listener from `relay serve`. That coupling is operationally awkward for `relay-cn`: Relay will change often as product behavior evolves, but STUN is deliberately small and stable, and restarting or repinning it on every Relay update adds avoidable operational surface.

The deployment target is a complete `relay-cn` stack managed by Docker Compose. It needs to preserve the existing Compose production boundaries: PostgreSQL is containerized, runtime secrets live in the remote Compose `.env`, Relay/STUN images are pulled from private GHCR, nginx remains the HTTPS reverse proxy for HTTP/WebSocket traffic, and schema changes for existing databases stay manual. The new need is to separate the runtime lifecycle of Relay and STUN without creating a separate STUN Dockerfile or independent release workflow.

---

## Actors

- A1. Maintainer: decides release versions, triggers GitHub Actions releases, and updates deployment docs.
- A2. GitHub Actions release workflow: builds the Relay/STUN image artifact once and publishes service-specific image names to GHCR.
- A3. Relay operator: prepares `relay-cn` secrets, pins image tags, starts services, and verifies health.
- A4. Docker Compose stack: runs PostgreSQL, Relay, and STUN as separately addressable services.
- A5. STUN clients: use the public UDP STUN endpoint for direct connectivity candidate discovery.

---

## Key Flows

- F1. First split-service rollout
  - **Trigger:** `relay-cn` needs to launch or migrate to the complete Compose-managed Relay + STUN stack.
  - **Actors:** A1, A2, A3, A4
  - **Steps:** A release containing the STUN-only startup mode is published, the operator sets both Relay and STUN image tags in the remote `.env`, Compose starts PostgreSQL, Relay, and STUN, and the operator verifies HTTP/WebSocket Relay health plus UDP/STUN reachability.
  - **Outcome:** `relay-cn` runs a complete stack where Relay and STUN are independently visible in Compose.
  - **Covered by:** R1, R2, R4, R5, R8, R10

- F2. Routine Relay update
  - **Trigger:** A new Relay release should be deployed to `relay-cn`.
  - **Actors:** A1, A2, A3, A4
  - **Steps:** The release workflow publishes a new Relay image tag, the operator updates only the Relay image tag, and Compose recreates the Relay service while leaving the STUN tag and service untouched unless explicitly requested.
  - **Outcome:** Relay advances to the new version without forcing a STUN update.
  - **Covered by:** R3, R6, R9

- F3. Rare STUN update
  - **Trigger:** The STUN implementation or operational contract changes.
  - **Actors:** A1, A2, A3, A4, A5
  - **Steps:** The release workflow publishes a new image tag that includes the STUN change, the operator updates the STUN image tag, and Compose recreates the STUN service while Relay can remain on its current tag.
  - **Outcome:** STUN can be updated intentionally and separately from normal Relay releases.
  - **Covered by:** R3, R7, R8, R10

---

## Requirements

**Runtime Separation**

- R1. The Compose deployment must run Relay HTTP/WebSocket traffic and Binding-only STUN as separate services, not as one combined `relay serve` process.
- R2. The Relay service must be able to run with STUN disabled so Relay restarts and updates do not also bind or restart the public STUN listener.
- R3. The STUN service must be able to run from a STUN-specific GHCR image name that points at the same release build artifact as Relay, using a STUN-only startup path.
- R4. The STUN-only service must not require PostgreSQL, Relay app secret, or Relay operator token configuration.
- R5. The STUN service must expose only Binding-only STUN behavior; it must not add TURN, UDP relay, ICE, or media-forwarding responsibilities.

**Version Pinning and Lifecycle**

- R6. Compose must support separate image tag variables for Relay and STUN so `RELAY_IMAGE_TAG` can change without changing `STUN_IMAGE_TAG`.
- R7. The STUN image tag should stay pinned across routine Relay updates unless a maintainer intentionally changes it.
- R8. The first split-service rollout may pin both services to the same release tag, but only because that first release contains the new STUN-only startup mode.
- R9. Relay-specific update commands and docs must make it clear whether they recreate only Relay or the whole stack.

**Relay-CN Operations**

- R10. `relay-cn` verification must include UDP/STUN reachability in addition to the existing DNS, HTTP health, API auth, WebSocket auth, and Compose service checks.
- R11. The deployment docs must call out the host firewall and DNS expectations for UDP STUN, including that nginx does not proxy STUN traffic.
- R12. Operator commands for invite/user management must continue to run against the Relay service, not the STUN service.
- R13. The Compose deployment must continue to keep runtime Relay/PostgreSQL secrets only in the remote `/opt/agentunnel/compose/.env`.

**Release and Documentation**

- R14. The GitHub Actions release path should keep one Docker build for the Relay binary and publish that artifact under both Relay and STUN GHCR image names.
- R15. The Relay image build and release smoke checks must verify release metadata for both service-specific image names.
- R16. Deployment docs must explain the two-service model, separate tag pins, first rollout expectation, routine Relay update path, rare STUN update path, and STUN verification.
- R17. Existing PostgreSQL schema rules remain unchanged: fresh databases initialize from the full schema snapshot, and existing deployed databases are changed manually by an operator.

---

## Acceptance Examples

- AE1. **Covers R1, R2, R4.** Given `relay-cn` is started with Compose, when the Relay service is recreated for a routine update, the STUN service remains separately running and does not require Relay database or app/operator secrets to start.
- AE2. **Covers R6, R7, R9.** Given `RELAY_IMAGE_TAG=v0.2.4` and `STUN_IMAGE_TAG=v0.2.1`, when the operator updates only `RELAY_IMAGE_TAG` and runs the documented Relay update path, Compose updates Relay without changing the STUN image tag.
- AE3. **Covers R3, R5, R8.** Given the first split-service rollout uses one new image tag for both services, when the STUN service starts, it uses the STUN-only startup path and exposes Binding-only STUN rather than the Relay HTTP/WebSocket server.
- AE4. **Covers R10, R11.** Given `relay-cn` is running, when the operator runs the status check, the output includes a UDP/STUN result in addition to existing HTTP/WebSocket checks.
- AE5. **Covers R14, R15.** Given the maintainer runs the existing Release workflow for `relay`, when the image is published to GHCR, the same build artifact is available as both `agent-tunnel-relay` and `agent-tunnel-stun` through separate Compose tags.

---

## Success Criteria

- `relay-cn` can run Relay, STUN, and PostgreSQL under Docker Compose with Relay and STUN shown as separate services.
- Routine Relay releases can be deployed without repinning or restarting the STUN service by default.
- STUN can remain on a stable known-good tag for long periods and be updated intentionally when needed.
- A future planner has enough scope clarity to design the STUN-only startup path, Compose changes, release checks, and relay-cn verification without inventing product or operational behavior.

---

## Scope Boundaries

- Do not create a separate STUN Dockerfile or independent STUN release workflow in this iteration.
- Do not add TURN, UDP relay, ICE, WebRTC, or public third-party STUN dependency.
- Do not bundle nginx, TLS, or certbot into Compose; host nginx remains the HTTP/WebSocket reverse proxy.
- Do not change Relay auth, session routing, attach semantics, or connectivity rendezvous semantics except as needed to keep STUN startup separate.
- Do not automate production schema migrations or change the existing PostgreSQL snapshot/manual-SQL contract.
- Do not require STUN clients to use a new application-level auth model; Binding-only STUN remains stateless.

---

## Key Decisions

- Use one Docker build artifact and two image names: this avoids a second build/release path while making the STUN service image reference unambiguous.
- Pin Relay and STUN independently: this matches their different update frequency and reduces unnecessary STUN churn.
- Make STUN-only startup secret-free: Binding-only STUN should not depend on Relay database or app/operator credentials.
- Keep the first rollout explicit: both tags can start on the same release, but the operational model should support divergence immediately after.

---

## Dependencies / Assumptions

- Current repository state has STUN embedded in Relay startup; a dedicated STUN-only startup path does not currently exist.
- Current Compose state publishes UDP `3478` from the Relay service; split deployment requires moving that public UDP responsibility to the STUN service.
- The existing Release workflow and `Dockerfile.relay` remain the source for the shared build artifact.
- `relay-cn` has or will have Docker Engine, Docker Compose plugin, GHCR pull credentials, remote Compose `.env`, host firewall rules for UDP `3478`, and DNS for the public Relay/STUN hostnames.

---

## Outstanding Questions

### Deferred to Planning

- [Affects R3][Technical] Choose the exact STUN-only command shape.
- [Affects R6, R9][Technical] Decide whether routine update automation should target only the Relay service by default or keep the current whole-stack `up -d` behavior with documentation safeguards.
- [Affects R10][Technical] Choose the most reliable STUN status-check mechanism for relay-cn operations.
- [Affects R11][Technical] Confirm the desired public STUN hostname convention for `relay-cn`.
