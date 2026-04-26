# Daemon Preview Generation Rules

## Status

This document captures the target phase-1 daemon rules for preview generation in the QUIC session-connectivity architecture.

## Core Rule

Preview is generated only on the daemon.

Relay does not generate it.
Android does not derive it from full terminal output.

## Preview Shape

Preview should be:

- plain text
- bounded in length
- derived from the current terminal mirror
- suitable for list rendering, not terminal emulation

Recommended phase-1 defaults:

- recent visible lines only
- modest total character budget
- ANSI stripped
- whitespace normalized for display

## Update Model

Preview updates should be:

- event-driven
- lightly throttled
- sent as current snapshots, not diffs

Phase 1 should optimize for correctness and simplicity rather than bandwidth micro-optimization.

## Empty Preview

An empty preview is valid.

Examples:

- brand new session with no output
- session metadata available before first preview update
- session output not yet meaningful for preview

The Android app should tolerate an empty preview and simply render no preview text.

## Separation From Session Metadata

Preview is not part of `session_index`.

Keep it separate so the app can independently update:

- session row metadata
- preview content

## References

- `docs/connectivity/architecture.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/daemon-session-sync.md`
- `docs/connectivity/android-client-behavior.md`
