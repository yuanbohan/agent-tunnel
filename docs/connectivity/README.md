# Connectivity Docs Index

Phase-1 design for the QUIC session-connectivity stack. This is a design anchor; the repository does not yet implement it.

## Read In This Order

1. `architecture.md` — top-level system shape and security model.
2. `contract.md` — the phase-1 must-ship contract (use this to scope implementation work).
3. The protocol set under `protocol/` — wire-level details.
4. The UX set under `ux/` — Android product behavior.
5. `reference/` — auxiliary references (state machines, error codes, sequence flows, decision history).

## Layout

```
docs/connectivity/
├── README.md                    # this file
├── architecture.md              # system shape + threat model
├── contract.md                  # phase-1 must-ship contract
├── protocol/
│   ├── pairing.md               # invitation, SAS, daemon trust
│   ├── transport.md             # QUIC + TLS + frames + session sync
│   ├── relay.md                 # Relay control plane + fallback tunnel
│   └── local-broker.md          # daemon ↔ tunnel run + preview pipeline
├── ux/
│   ├── android.md               # Android client behavior reference
│   └── subscription.md          # free / pro product rule
├── reference/
│   ├── state-machines.md        # transport / per-session / policy state
│   ├── error-codes.md           # canonical error code catalog
│   ├── sequence-flows.md        # end-to-end timing diagrams
│   └── decision-record.md       # key architectural decisions
└── _archive/
    └── 2026-04-26-architect-review.md
```

## Phase-1 Decisions At A Glance

The full reasoning lives in `contract.md`. The headlines:

- **Direct + WSS-tunneled QUIC fallback** (UDP relay deferred to phase 2 if perf gates trip).
- **Daemon auto-starts** when the user runs `tunnel run`. The user does not manage daemon lifecycle directly.
- **Free unlock = sticky first-attach** per daemon card. No auto-rollover.
- **Opaque app session** is bound server-side to `device_fingerprint`; client supplies it on login. Phase 1 does not require a per-WS device-key proof.
- **Control stream = bidirectional, JSON-typed frames.** Interactive stream = unidirectional (daemon → Android), raw PTY bytes.
- **Cloudflare quiche on Android, quic-go on daemon.** Step 1 validates the protocol/data layer with a Go mobile simulator; real Android `quiche` validation remains a TODO before Android compatibility is claimed.

## Phase-1 Implementation Order

`contract.md` defines four sub-phases (1.0 spike → 1.1 pairing+broker → 1.2 control plane+fallback → 1.3 direct+STUN), each with its own validation checklist.
