---
title: feat: session connectivity program plan
type: feat
status: superseded
date: 2026-04-24
origin: docs/brainstorms/2026-04-23-direct-attach-control-plane-requirements.md
superseded_by: docs/connectivity/decision-record.md
---

# feat: session connectivity program plan

## Status

This document is intentionally kept only as a breadcrumb.

The earlier implementation-program draft was written around a WebRTC-based direction. That direction has now been superseded by the QUIC-based architecture recorded in:

- `docs/connectivity/decision-record.md`
- `docs/connectivity/architecture.md`

## Why This Plan Was Superseded

The old plan assumed:

- WebRTC DataChannels
- Relay-managed signaling for SDP / ICE style negotiation
- TURN / coturn fallback
- Relay-visible session discovery through `GET /api/sessions`

Those are no longer the selected architecture.

The current direction is:

- QUIC/TLS 1.3 + device-key pinning
- daemon-owned session discovery after secure daemon connectivity
- Relay-owned daemon presence, pairing transport, rendezvous, and fallback packet relay
- WebSocket-over-HTTPS relay tunnel for encrypted QUIC packets

## Follow-Up

A new implementation plan should be written later from the `docs/connectivity/` architecture set rather than by editing the old WebRTC plan in place.
