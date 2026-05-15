---
title: step 03: Daemon local broker and tunnel run registration
type: handoff
status: completed
date: 2026-04-29
step: 3
github_issue: https://github.com/yuanbohan/agent-tunnel/issues/87
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
---

# Step 3: Daemon Local Broker And `tunnel run` Registration

## Purpose

Make the daemon aware of local sessions while keeping `tunnel run` as the
terminal owner.

## Major Modules

- Daemon connectivity core lifecycle
- Required `tunnel run` connect-to-daemon flow
- Long-lived local session registration socket
- Local session roster
- Latest preview cache
- Tmux launch-health separation

## In Scope

- `tunnel run` registers itself with the daemon before starting the user command.
- Daemon knows which local sessions exist.
- Daemon has a latest preview per session.
- Missing tmux degrades remote-launch health without blocking local broker registration.

## Out Of Scope

- Mobile transport
- Pairing UI
- Direct UDP

## Acceptance Checklist

- [x] Local `tunnel run` sessions appear in daemon-local roster.
- [x] Session disappears when the local connection closes.
- [x] Preview is generated locally, not by Relay.
- [x] The local terminal starts only after Relay registration and daemon broker registration have both succeeded.

## Implementation Summary

- `internal/tunnel/daemon/broker.go` adds the daemon-owned long-lived Unix socket listener and live in-memory roster/cache.
- `internal/tunnel/daemon/session_registration.go` adds the `tunnel run` side registration client. It keeps one broker connection open, retries after daemon/socket loss after startup, verifies the daemon Relay base URL and auth-context fingerprint before each broker connect, sends full `register_session` metadata, waits for the daemon broker `register_ack` during startup, sends throttled latest-preview replacements, and sends `session_gone` on close when possible.
- `internal/tunnel/session/terminal_mirror.go` now exposes bounded plain-text preview generation from the owning `tunnel run` mirror. Preview normalization happens on the throttled broker sender path, not on every PTY output chunk.
- `cmd/tunnel/main.go` now requires a same-base-URL and same-auth-context daemon during `tunnel run` startup, requires broker registration before launching the user command, and adds the local registration client as a PTY output sink.
- `tunnel daemon start/status/doctor --json`, `tunnel daemon stop`, `tunnel session list`, `tunnel session stop <session-id>`, `tunnel workspace open`, and `tunnel workspace close` expose the public daemon, session, and local workspace surfaces without leaking daemon-local broker internals.
- Daemon startup uses a daemon-local startup lock so concurrent cold `tunnel run` processes do not race to create competing daemon children.
- The daemon broker enforces owner-only broker socket permissions, closes idle unregistered broker connections, removes stale same-connection re-registrations, and re-applies the preview size/normalization limit before caching previews.
- `internal/tunnel/daemon/runtime.go` now starts the daemon connectivity core without tmux. Missing tmux initializes `launch_health: degraded` with `last_failure: tmux_not_found`, but control, pairing, connectivity realtime, and broker sockets still run.
- The existing Relay `/agent/ws` attach path, PTY owner, local terminal behavior, startup Relay registration gate, and tmux-backed remote launch failure reasons remain unchanged.

## Verification Performed

- `go test ./internal/tunnel/daemon ./internal/tunnel/session ./cmd/tunnel`
- `go test ./...`
- `make test`
- `make test-relay`
- `make build`
- `git diff --check`

## Known Gaps

- The broker roster/cache is daemon-local. Public CLI session discovery is account-level and Relay-backed through `tunnel session list`; mobile visibility uses the daemon transport session index rather than a public broker-sessions command.
- Peer credential enforcement is limited to owner-only socket path permissions plus same-owner socket checks on Linux/macOS before `tunnel run` sends metadata. Stronger per-platform credential checks remain future hardening.
- Broker preview is latest-only in daemon memory. There is no preview history, durable preview storage, or Relay-visible preview.
- Existing daemon/account mismatch protection verifies both Relay base URL and a non-secret auth-context fingerprint before `tunnel run` starts broker registration and before each broker reconnect. Step 4 transport fanout still must enforce paired-device visibility and authorization before exposing previews to paired devices.

## Follow-Up For Step 4

- Bridge the broker roster into the daemon transport as `session_index`.
- Implement `preview_subscribe` over the daemon transport and serve cached latest preview snapshots.
- Implement interactive request/release, snapshot chunks, live bytes, input, and resize over the fallback-only transport.
- Keep Android production code, QUIC direct path, STUN, and Relay tunnel work out of Step 3.
