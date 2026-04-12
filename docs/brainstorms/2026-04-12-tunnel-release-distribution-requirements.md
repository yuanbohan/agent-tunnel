---
date: 2026-04-12
topic: tunnel-release-distribution
---

# Tunnel Release Distribution

## Problem Frame

`tunnel` currently ships as a source-built CLI. That is fine for contributors, but it is too heavy for end users who should be able to install the CLI with one documented command. The release flow also needs to preserve the current repository privacy model: source stays in the private `agent-tunnel` repo, while installable binaries and the public install entrypoint are published through a separate public distribution surface.

This work should create a repeatable release path that starts from a manually approved GitHub Actions run, builds versioned release artifacts for the supported operating system and CPU combinations, publishes those artifacts to a public distribution repo, and gives users a single `curl | bash` install path that selects the right artifact for their machine.

```mermaid
flowchart TB
    A[Maintainer triggers manual release in private repo]
    B[Workflow validates version and builds tunnel binaries]
    C[Workflow packages archives and checksums]
    D[Workflow publishes matching release to public distribution repo]
    E[Public repo exposes install.sh and release assets]
    F[User runs curl -fsSL .../install.sh | bash]
    G[Installer detects OS and CPU]
    H[Installer downloads matching archive and verifies checksum]
    I[Installer places tunnel in ~/.local/bin]

    A --> B --> C --> D --> E --> F --> G --> H --> I
```

## Requirements

**Distribution Model**
- R1. The canonical source repository for `tunnel` must remain private.
- R2. Installable `tunnel` release artifacts must be published through a separate public distribution repository so users without source access can download them.
- R3. The public distribution repository must provide a stable public HTTPS install entrypoint suitable for `curl -fsSL ... | bash`.
- R4. The public distribution repository must not be presented as the canonical source-of-truth for development; it exists to distribute installable artifacts and release-facing documentation.

**Release Workflow**
- R5. A maintainer must be able to trigger a release manually from GitHub Actions rather than relying on an automatic release from every pushed tag.
- R6. The release workflow must build `tunnel` for these targets in v1: `darwin/arm64`, `darwin/amd64`, `linux/amd64`, and `linux/arm64`.
- R7. Each release target must produce a downloadable artifact with a predictable naming scheme that encodes the version, operating system, and CPU architecture.
- R8. Each release must publish a checksum manifest covering all user-downloadable artifacts.
- R9. The public release created for a version must contain the same version identifier across artifact names, release metadata, and installer lookup behavior.
- R10. The release workflow must fail the publication step if any required platform artifact or required checksum output is missing.

**Installer Experience**
- R11. The official v1 installation path must be a shell installer invoked via `curl | bash`.
- R12. The installer must default to the latest stable public release when no version is specified.
- R13. The installer must allow advanced users and automation to pin an explicit version, such as `VERSION=v1.2.3`.
- R14. The installer must detect the current operating system and CPU architecture and choose the matching release artifact automatically.
- R15. The installer must install `tunnel` into `~/.local/bin` by default and must not require `sudo`.
- R16. If `~/.local/bin` is not on the user's `PATH`, the installer must complete the install and then print clear next-step guidance instead of attempting shell-profile mutation silently.
- R17. The installed CLI must provide a straightforward way for users and support workflows to confirm the installed version.

**Integrity and Failure Behavior**
- R18. The installer must verify the downloaded artifact against the published checksum manifest before replacing or installing the local binary.
- R19. The installer must fail closed with a clear error message when it cannot map the current machine to a supported target, download the expected files, or verify integrity.
- R20. Users must not be left with a partially replaced `tunnel` binary if the download or verification step fails.

**Scope and Documentation**
- R21. v1 release distribution scope is limited to the `tunnel` CLI; `relay` and `relay-migrate` remain outside this public binary distribution flow.
- R22. v1 distribution scope is limited to the official shell installer path; package-manager integrations such as Homebrew are deferred.
- R23. User-facing documentation must show the default install command, the pinned-version install command, the supported platform matrix, and the install location.
- R24. Maintainer-facing documentation must describe how to trigger a release, what gets published publicly, and how to recover from a failed or incomplete release publication.
- R25. Tunnel and Relay must share an explicit compatibility contract: same compatibility line means compatibility is guaranteed, while a compatibility-line change means compatibility is no longer guaranteed. For `v1+`, the compatibility line is the semver major version. For pre-`v1`, the compatibility line is `0.minor`.

## Success Criteria

- A maintainer can create a new `tunnel` release through one manual GitHub Actions run in the private repo.
- A user with no access to the private source repo can install the latest `tunnel` release from the public distribution repo with one documented shell command.
- The installer succeeds on all four supported v1 targets without requiring users to choose artifacts manually.
- The installer rejects corrupted or mismatched downloads before replacing the local binary.
- The published install contract makes the Tunnel/Relay compatibility line explicit enough that a maintainer can tell whether a given pair is expected to work.
- Release and install docs are clear enough that a first-time maintainer and a first-time user can complete the flow without repo-specific tribal knowledge.

## Scope Boundaries

- No Windows support in v1.
- No public binary distribution for `relay` or `relay-migrate` in v1.
- No Homebrew, apt, yum, or other package-manager integrations in v1.
- No automatic `sudo` escalation or installation into system-wide locations in v1.
- No silent shell-profile edits to add `~/.local/bin` to `PATH`.
- No requirement that the public distribution repo expose private source history or become the main contributor repo.

## Key Decisions

- Public distribution repo instead of public source repo: source privacy is preserved while installable artifacts remain publicly reachable.
- Manual release trigger instead of auto-release on tag push: maintainers keep an explicit approval point before publishing public binaries.
- Four-target platform matrix in v1: this covers the common Mac and Linux combinations with relatively low marginal cost for a Go CLI.
- `~/.local/bin` install target: avoids `sudo`, keeps the installer compatible with unprivileged shell usage, and matches common CLI conventions.
- Latest-by-default installer with version pinning: new users get the shortest happy path while automation and rollback flows can still lock to a specific version.
- Shell installer only in v1: one official path keeps the first release flow small enough to harden before adding package managers.
- Compatibility line instead of raw semver major for pre-`v1`: this keeps `v0.1.x` distinct from `v0.2.x`, which matches the intended Relay/Tunnel contract before `v1`.

## Dependencies / Assumptions

- Maintainers can create and administer a separate public GitHub repository dedicated to `tunnel` distribution.
- The private repo can authenticate to publish releases or release assets into that public repo.
- GitHub Releases remain the public artifact hosting surface for v1.
- Go cross-compilation remains sufficient for the supported `tunnel` targets.

## Outstanding Questions

### Resolve Before Planning

None.

### Deferred to Planning

- [Affects R2,R5,R9][Technical] Should cross-repo publication be done directly from the private repo workflow into the public repo, or should the private repo trigger a second workflow owned by the public repo?
- [Affects R7,R8,R18][Technical] What exact artifact layout should v1 use for archives, checksum manifests, and any future signature or attestation files?
- [Affects R17][Technical] What version-reporting contract should `tunnel` expose so release metadata and installer checks stay aligned?
- [Affects R24][Needs research] What is the cleanest recovery flow when a public release is partially created but asset upload fails midway?
- [Affects R18][Needs research] Should v1 stop at checksum verification, or also publish GitHub-native attestations or signatures as an immediate follow-on hardening step?

## Next Steps

→ `/prompts:ce-plan` for structured implementation planning
