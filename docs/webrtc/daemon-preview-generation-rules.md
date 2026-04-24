# Daemon Preview Generation Rules

## Status

This document captures the recommended phase-1 rules for daemon-side preview generation in the WebRTC session-connectivity architecture.

It exists because preview is a product feature in its own right:

- it is not Relay metadata
- it is not full terminal emulation
- it is not computed on Android

## Purpose

The preview generator should produce a lightweight text projection that is:

- fast to compute
- stable to render in a session list
- cheap to send frequently
- clearly separate from interactive terminal state

## Core Rules

- preview is generated on the daemon
- preview is plain text
- preview is not terminal emulation
- preview is snapshot-based, not diff-based
- preview updates are event-driven and lightly throttled

## Recommended Output Shape

Recommended preview payload:

- `session_id`
- `text`
- `version`
- `updated_at`

The preview text should be:

- recent and relevant
- bounded in size
- normalized for list rendering

## Text Normalization

Recommended phase-1 normalization:

- strip ANSI styling
- collapse rendering to plain text
- trim obviously empty trailing lines
- normalize line endings

Preview should be optimized for readability, not terminal fidelity.

## Size Boundaries

Phase 1 should keep preview bounded.

Recommended default:

- keep only the most recent tail window
- apply a fixed line cap
- apply a fixed total character cap

The exact line and character caps remain implementation details, but they must be deterministic.

## Update Rules

Preview should update:

- when the underlying terminal mirror changes in a way that changes the preview text
- not on every byte blindly if the resulting preview text is unchanged

Recommended phase-1 strategy:

- recompute on content change
- compare against the previous preview snapshot
- emit only when the preview text changed
- lightly throttle burst updates

## Empty Preview Semantics

The daemon must be able to distinguish:

- preview is empty
- preview has not yet been emitted for this connection

Phase 1 should use an explicit initial `preview_snapshot` even when the text is empty, so Android can distinguish "no content" from "not initialized yet".

## Security Notes

Preview is content-bearing.

That means:

- preview must not be exposed through Relay plaintext
- preview should be treated as sensitive local content on Android
- preview generation should avoid leaking more content than the bounded tail window intends

## Related Documents

- `docs/webrtc/architecture.md`
- `docs/webrtc/datachannel-protocol.md`
- `docs/webrtc/android-client-behavior.md`
- `docs/webrtc/mobile-reference.md`
