---
date: 2026-04-11
topic: repo-layout-restructure
---

# Repo Layout Restructure

## Problem Frame

The repository currently mixes Go implementation packages and non-code project assets at the repo root:

- runtime implementation packages live in top-level directories such as `connector/`, `launcher/`, `protocol/`, `relay/`, and `session/`
- entrypoints live in `cmd/agentunnel/` and `cmd/relay/`
- non-Go assets such as `docs/`, `deploy/`, and `scripts/` also live at the root

That shape works, but it obscures product boundaries. The `tunnel` runtime code is split across several unrelated top-level directories, while `relay` implementation sits beside them as a peer. The repo reads more like a collection of packages than two concrete products with one shared internal protocol.

Verified current constraints from the codebase:

- the repository is one Go module rooted at `go.mod`
- the build currently points at `./cmd/agentunnel` and `./cmd/relay` in `Makefile`
- `connector/`, `launcher/`, and `session/` are only imported by the local `tunnel` runtime
- `relay/` is only imported by `cmd/relay`
- `protocol/` is shared by both `tunnel` and `relay`

This restructure should make the repo read like the actual product:

- `tunnel` entrypoint plus its internal runtime packages
- `relay` entrypoint plus its internal server packages
- one shared internal protocol package
- project assets left clearly separate at the root

```text
repo root
├── cmd/
│   ├── tunnel/
│   └── relay/
├── internal/
│   ├── tunnel/
│   │   ├── connector/
│   │   ├── launcher/
│   │   └── session/
│   ├── relay/
│   └── protocol/
├── docs/
├── deploy/
├── scripts/
└── go.mod
```

## Requirements

**Target Layout**
- R1. The repository must keep a top-level `cmd/` directory for executable entrypoints.
- R2. The local CLI entrypoint must move from `cmd/agentunnel/` to `cmd/tunnel/`.
- R3. The relay entrypoint must remain under `cmd/relay/`.
- R4. `connector/`, `launcher/`, and `session/` must move under `internal/tunnel/`.
- R5. `relay/` implementation code must move under `internal/relay/`.
- R6. Shared protocol code must move from top-level `protocol/` to `internal/protocol/`.
- R7. The repo must remain a single Go module rooted at `go.mod`.

**Boundary Clarity**
- R8. The restructure must make `tunnel` and `relay` the primary code ownership boundaries rather than leaving implementation packages as unrelated root-level peers.
- R9. The restructure must preserve `protocol` as the only shared implementation package between `tunnel` and `relay` in this phase.
- R10. The first pass should keep `internal/relay/` as one package directory rather than splitting it into more subpackages unless the move itself reveals a concrete need.
- R11. Non-Go project assets such as `docs/`, `deploy/`, `scripts/`, `bin/`, and `logs/` must remain top-level in this phase.

**Behavior and Public Surface**
- R12. The restructure must not intentionally change runtime behavior, protocol behavior, auth behavior, or product scope.
- R13. The public binary names must remain `tunnel` and `relay`.
- R14. Existing user-facing commands documented in `README.md` and operational docs should stay the same except where source paths must be updated from `cmd/agentunnel` to `cmd/tunnel`.
- R15. The current attach-oriented product contract must remain unchanged by this restructure.

**Repository Consistency**
- R16. All Go imports must be updated to the new package locations consistently.
- R17. Build and test entrypoints in `Makefile` must be updated to the new source paths.
- R18. Architecture and onboarding-style docs that mention source paths must be updated to the new layout.
- R19. The repo must not retain stale references to `cmd/agentunnel` after the restructure is complete.
- R20. The repo must not retain stale top-level package references to `connector/`, `launcher/`, `protocol/`, `relay/`, or `session/` once those packages move under `internal/`.

**Execution Discipline**
- R21. The first pass must be a structural move plus import/path cleanup, not a simultaneous package-boundary redesign.
- R22. The restructure must be reviewable as a layout refactor rather than a mixed refactor plus feature change.
- R23. If any code must be mechanically adjusted because of the move, those adjustments should stay minimal and directly tied to the new paths.

## Success Criteria

- A new contributor can identify the two product surfaces by scanning `cmd/` and `internal/` once.
- The runtime ownership is visually obvious: `internal/tunnel/...` belongs to the CLI runtime, `internal/relay/` belongs to the relay server, and `internal/protocol/` is the shared wire contract.
- `make build`, `make test`, and `make test-relay` can be updated to work against the new layout without changing the intended binaries or behaviors.
- The repo no longer exposes the previous root-level implementation-package sprawl.

## Scope Boundaries

- No auth redesign.
- No protocol redesign.
- No multi-module split.
- No rename of the product binaries away from `tunnel` and `relay`.
- No attempt in this phase to reorganize `docs/`, `deploy/`, or `scripts/` beyond leaving them at the root.
- No opportunistic subpackage explosion inside `internal/relay/`.

## Key Decisions

- Use `cmd + internal`: executable entrypoints stay under `cmd/`, while implementation packages move under `internal/`.
- Rename the CLI source entrypoint to `cmd/tunnel`: the source path should match the user-facing binary name.
- Keep the repo single-module: the current project size does not justify multiple Go modules.
- Move shared protocol code into `internal/protocol/`: the protocol is shared inside this repo, not published as a reusable public library.
- Keep the first pass conservative: directory clarity is the goal, not package-theory perfection.

## Dependencies / Assumptions

- `Makefile`, `README.md`, `docs/architecture.md`, and any tests or docs that refer to source paths will need coordinated updates during implementation.
- External users consume the built binaries and documented commands, not the current internal import paths, so moving packages under `internal/` does not break an intended public SDK surface.
- The repository's tests are the main regression guard for proving the move stayed behavioral-neutral.

## Outstanding Questions

### Deferred to Planning
- Affects R10-R11. Technical: Should `internal/relay/` stay flat as one package directory or pick up a small amount of file regrouping once the move is done?
- Affects R16-R20. Technical: What is the safest file-move order to keep the repo buildable throughout the refactor?
- Affects R17-R18. Technical: Which docs should be updated in the same change set versus a follow-up cleanup pass?

## Next Steps

→ `/prompts:ce-plan` for structured implementation planning
