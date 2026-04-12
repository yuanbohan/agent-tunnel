---
date: 2026-04-12
topic: tunnel-cli-help
---

# Tunnel CLI Help

## Problem Frame

`tunnel` currently has a `--version` fast path, but it does not offer a user-facing help command. Verified current behavior in `cmd/tunnel/args.go`: the CLI discards the standard library flag output, requires `TUNNEL_AUTH_TOKEN` for normal execution, and returns only a terse usage string when no launcher command is provided.

That makes first-run discovery unnecessarily awkward. Users cannot ask the CLI how to use it without already knowing the invocation shape, the relevant environment variables, and the difference between `tunnel` flags and launcher arguments.

## Requirements

**Help Entry Points**
- R1. `tunnel --help` must print a human-readable help message and exit successfully.
- R2. `tunnel -h` must behave the same as `tunnel --help`.
- R3. The help path must complete before base URL validation, `TUNNEL_AUTH_TOKEN` lookup, launcher resolution, or terminal and relay startup.

**Help Content**
- R4. The help message must describe the top-level invocation shape for `tunnel`, including flags followed by the launcher command and its arguments.
- R5. The help message must list the supported top-level flags in this revision: `--label`, `--base-url`, `--version`, and the help flag.
- R6. The help message must document the relevant environment variables: `TUNNEL_BASE_URL` and `TUNNEL_AUTH_TOKEN`.
- R7. The help message must make clear that `TUNNEL_BASE_URL` defaults to `https://diaro.me` when unset.
- R8. The help message must include one or two concrete examples of launching a CLI through `tunnel`.

**Missing Command Behavior**
- R9. Running `tunnel` with no launcher command must print the same full help-oriented guidance instead of only a terse one-line usage string.
- R10. The no-command path must still exit non-zero so shells and scripts treat it as incorrect usage.

**Scope and Documentation**
- R11. This pass may leave other runtime validation errors unchanged, including invalid base URL errors and missing `TUNNEL_AUTH_TOKEN` during normal execution, unless a small alignment change is needed to support the new help contract.
- R12. User-facing documentation should mention the new help entrypoint where it materially improves CLI discoverability.

## Success Criteria

- A new user can run `tunnel --help` or `tunnel -h` without any environment variables set and understand how to launch a command through `tunnel`.
- The help output explains the supported top-level flags, the required and optional environment variables, and at least one concrete invocation example.
- Running `tunnel` with no launcher command prints the same help guidance and exits with a non-zero status.
- Existing startup behavior remains unchanged once the user provides a valid launcher command and required environment.

## Scope Boundaries

- No new subcommands.
- No migration to a broader CLI framework.
- No full redesign of all argument-error formatting in this pass.
- No changes to relay, session, launcher, or attach behavior beyond the CLI help contract.

## Key Decisions

- Support both `--help` and `-h`: this matches normal CLI expectations and removes avoidable user guesswork.
- Make help a true fast path: users should be able to discover the CLI contract without already satisfying runtime prerequisites.
- Treat bare `tunnel` as a usage error with full guidance: users get the readable help text, while automation still sees incorrect usage through a non-zero exit.

## Dependencies / Assumptions

- `README.md` is the most likely place to mention the new help entrypoint because it already documents `tunnel --version` and basic launch examples.

## Outstanding Questions

### Resolve Before Planning

None.

### Deferred to Planning

- [Affects R4-R8][Technical] What exact help text layout best fits the existing CLI style while staying concise enough for terminal use?
- [Affects R12][Technical] Where should `README.md` mention `tunnel --help` so it improves discoverability without duplicating too much CLI usage prose?

## Next Steps

→ `/prompts:ce-plan` for structured implementation planning
