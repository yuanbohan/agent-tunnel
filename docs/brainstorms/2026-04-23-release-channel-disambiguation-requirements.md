---
date: 2026-04-23
topic: release-channel-disambiguation
---

# Release Channel Disambiguation

## Problem Frame

Tunnel CLI releases and Relay image releases currently share nearby versioning language, and Relay image publishing is triggered by bare semver tags such as `v0.1.2`. That makes a tag hard to interpret: the same-looking tag can be mistaken for either a Tunnel release or a Relay release.

The release model should make the product being released explicit at the trigger point while preserving the clean version identifiers users and operators already see in published outputs. A maintainer should be able to publish Tunnel or Relay through GitHub Actions dispatch, with product-specific source tags such as `tunnel-v0.1.2` and `relay-v0.1.2`, while the generated Tunnel binary version, public Tunnel release assets, and Relay Docker image tag remain plain `v0.1.2`.

---

## Actors

- A1. Maintainer: chooses which product to release and what version to publish.
- A2. GitHub Actions release workflow: validates the requested product/version, creates or consumes the product-prefixed release tag, builds artifacts, verifies embedded version metadata, and publishes outputs.
- A3. Tunnel user: installs or updates the public Tunnel CLI using plain `vX.Y.Z` versions.
- A4. Relay operator: deploys Relay Docker images using plain `vX.Y.Z` image tags in Compose.
- A5. Local developer: builds and installs local binaries for development without creating release tags or embedding official release identity.

---

## Key Flows

- F1. Tunnel release dispatch
  - **Trigger:** A maintainer manually starts the release workflow and selects Tunnel with version `vX.Y.Z`.
  - **Actors:** A1, A2, A3
  - **Steps:** The workflow validates the version, associates the release with source tag `tunnel-vX.Y.Z`, builds Tunnel packages with embedded version `vX.Y.Z`, publishes public distribution assets, and verifies the public installer reports `tunnel vX.Y.Z`.
  - **Outcome:** The source repository clearly records a Tunnel release tag, while the public Tunnel release remains plain `vX.Y.Z`.
  - **Covered by:** R1, R2, R3, R5, R6

- F2. Relay release dispatch
  - **Trigger:** A maintainer manually starts the release workflow and selects Relay with version `vX.Y.Z`.
  - **Actors:** A1, A2, A4
  - **Steps:** The workflow validates the version, associates the release with source tag `relay-vX.Y.Z`, builds the Relay Docker image with embedded version `vX.Y.Z`, verifies `relay version`, and publishes `ghcr.io/yuanbohan/agent-tunnel-relay:vX.Y.Z`.
  - **Outcome:** The source repository clearly records a Relay release tag, while the deployable Docker image tag remains plain `vX.Y.Z`.
  - **Covered by:** R1, R2, R4, R5, R6

- F3. Local development install
  - **Trigger:** A developer runs `make install` or another local build/install target.
  - **Actors:** A5
  - **Steps:** The local command builds and installs development binaries without calculating the next release version, creating tags, pushing tags, or marking binaries as official releases.
  - **Outcome:** Local installation stays simple and cannot be confused with the formal GitHub Actions release path.
  - **Covered by:** R7, R8, R9

---

## Requirements

**Release trigger semantics**

- R1. Official Tunnel and Relay releases must be initiated through an explicit GitHub Actions dispatch path rather than through a bare semver tag push.
- R2. The dispatch experience must make the selected product explicit before publication, either by a product input or by an equivalently unambiguous release entrypoint.
- R3. A Tunnel release must use a source tag in the form `tunnel-vX.Y.Z`.
- R4. A Relay release must use a source tag in the form `relay-vX.Y.Z`.
- R5. Release workflows must reject ambiguous bare release tags such as `vX.Y.Z` as official source-release triggers.

**Published version semantics**

- R6. Product prefixes are source-control disambiguators only; published product versions must strip the prefix and remain `vX.Y.Z`.
- R7. Tunnel public release assets, installer behavior, `latest.json`, native update/rollback metadata, and `tunnel --version` must continue to expose the plain version `vX.Y.Z`.
- R8. Relay Docker image tags, image labels where version is user-visible, `/api/version`, and `relay version` must continue to expose the plain version `vX.Y.Z`.

**Local development boundaries**

- R9. Local build and install commands must not automatically calculate, create, or push release tags.
- R10. `make install` must install locally built development binaries without requiring a release version input and without embedding official release-channel identity.
- R11. Any remaining helper for creating release tags must require an explicit product and version, or be removed if GitHub Actions dispatch becomes the only supported tag creation path.

**Documentation and compatibility**

- R12. Release documentation must explain that source tags are product-prefixed while published product versions are prefix-free.
- R13. Maintainer docs must show separate Tunnel and Relay release examples using `tunnel-vX.Y.Z` and `relay-vX.Y.Z`.
- R14. Operator and user docs must keep examples of install/update/deploy versions as plain `vX.Y.Z`, not `tunnel-vX.Y.Z` or `relay-vX.Y.Z`.
- R15. The existing Tunnel/Relay compatibility-line contract must continue to compare plain product versions after the source tag prefix is stripped.

---

## Acceptance Examples

- AE1. **Covers R1, R2, R3, R6, R7.** Given a maintainer dispatches a Tunnel release for `v0.2.3`, when the workflow completes, the source repo has a `tunnel-v0.2.3` release marker and the installed CLI reports `tunnel v0.2.3`.
- AE2. **Covers R1, R2, R4, R6, R8.** Given a maintainer dispatches a Relay release for `v0.4.1`, when the workflow completes, the source repo has a `relay-v0.4.1` release marker and GHCR contains `ghcr.io/yuanbohan/agent-tunnel-relay:v0.4.1`.
- AE3. **Covers R5.** Given someone pushes or creates a bare tag `v0.5.0`, when release workflows evaluate it, no official Tunnel or Relay publication is triggered solely because of that bare tag.
- AE4. **Covers R9, R10.** Given a developer runs `make install` on a local checkout with no version input, when the command succeeds, it installs development binaries and does not create, increment, or push any git tag.

---

## Success Criteria

- A maintainer can tell from the dispatch input and resulting source tag whether a release is for Tunnel or Relay without inspecting artifacts.
- Tunnel users and Relay operators still interact with simple `vX.Y.Z` product versions.
- Local development commands no longer have hidden release-version behavior.
- A downstream planner can update workflows, scripts, Make targets, and release docs without inventing version semantics.

---

## Scope Boundaries

- This does not merge Tunnel and Relay into one published artifact.
- This does not change the public Tunnel distribution repository or the Relay GHCR package name.
- This does not change the supported Tunnel platform matrix.
- This does not add package-manager distribution such as Homebrew, apt, or yum.
- This does not change the Docker Compose deployment model beyond the version/tag guidance operators consume.
- This does not redesign semantic version compatibility rules; it only clarifies where product prefixes apply.

---

## Key Decisions

- Product-prefixed source tags: `tunnel-vX.Y.Z` and `relay-vX.Y.Z` remove ambiguity in the private source repository.
- Prefix-free published versions: users and operators should not have to learn different version strings for install, update, rollback, image pinning, or compatibility checks.
- Dispatch-first releases: an explicit maintainer action is clearer than tag-push side effects, especially now that two products release from the same source repository.
- No local automatic tag accumulation: local build/install should support development, while official release versioning belongs in GitHub Actions.

---

## Dependencies / Assumptions

- GitHub Actions can create or validate product-prefixed tags from the dispatch context.
- Existing release packaging can accept a plain `vX.Y.Z` after the workflow strips the `tunnel-` or `relay-` source tag prefix.
- Existing compatibility-line helpers operate on plain semver versions and should not be taught product-prefixed versions unless planning finds a strong reason.

---

## Outstanding Questions

### Resolve Before Planning

None.

### Deferred to Planning

- [Affects R1,R2][Technical] Should the final UX be one combined release workflow with a product selector, or two separate dispatch workflows whose names are already product-specific?
- [Affects R3,R4][Technical] Should dispatch create the product-prefixed git tag, or require the tag to exist before the workflow can publish?
- [Affects R9,R11][Technical] Should the existing auto-increment tag helper be deleted entirely, or retained only as an explicit product/version helper?

---

## Next Steps

-> `/ce-plan` for structured implementation planning
