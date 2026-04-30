# Connectivity State Machines

## Status

This document collects the core state machines of the QUIC session-connectivity architecture. Tier policy is computer-scoped only; there is no per-session tier state.

## Per-Daemon Transport State

This state is owned by the Android connection manager. Each daemon connection has one independent instance of this state machine. The state name is also surfaced in the UI as the computer-card status.

```mermaid
stateDiagram-v2
    [*] --> offline
    offline --> connecting_direct: active trusted computer online,<br/>rendezvous starts
    connecting_direct --> offline: daemon goes offline<br/>via Relay presence
    connecting_direct --> connected_direct: QUIC/TLS handshake OK<br/>over direct UDP
    connecting_direct --> connecting_relay: direct attempt<br/>deadline expired
    connecting_relay --> offline: daemon goes offline<br/>via Relay presence
    connecting_relay --> connected_relay: QUIC/TLS handshake OK<br/>over relay tunnel
    connecting_relay --> reconnecting: tunnel setup failed
    connected_direct --> reconnecting: connection lost
    connected_relay --> reconnecting: connection lost
    reconnecting --> connecting_direct: backoff elapsed,<br/>retry direct
    connected_direct --> offline: daemon goes offline<br/>via Relay presence
    connected_relay --> offline: daemon goes offline
    reconnecting --> offline: daemon goes offline
```

### Transition Rules

- `offline -> connecting_direct` happens only for a trusted computer the current tier allows Android to connect.
- `connecting_direct -> connecting_relay` is sequential, not happy-eyeballs; the deadline is an implementation default.
- `connecting_* -> offline` happens immediately if Relay presence marks the daemon offline before the transport finishes connecting.
- `reconnecting` uses exponential backoff with jitter.
- the path badge is derived from this state and confirmed by transport diagnostics: `connected_direct -> "Direct"`, `connected_relay -> "Relay"`, others -> status word.

### Daemon-Side Mirror

The daemon also maintains a per-Android-connection state, but once QUIC/TLS is accepted it treats direct and relay carriers identically and reports only advisory path diagnostics (`direct` or `relay`).

## Per-Session UI Lifecycle

This state is owned per `(computer, session)` pair on Android. Multiple sessions on the same daemon connection can each be in different states.

```mermaid
stateDiagram-v2
    [*] --> absent
    absent --> visible: session appears in session_index or session_upsert
    visible --> preview_active: preview_snapshot arrives<br/>for subscribed row
    preview_active --> visible: preview_unsubscribe or<br/>preview cleared
    visible --> interactive_pending: interactive_request sent
    preview_active --> interactive_pending: interactive_request sent
    interactive_pending --> interactive_active: interactive_granted +<br/>stream observed
    interactive_pending --> preview_active: interactive_denied<br/>and preview still subscribed
    interactive_pending --> visible: interactive_denied<br/>and no preview subscription
    interactive_pending --> absent: interactive_denied(session_unavailable)<br/>followed by session_gone
    interactive_active --> preview_active: interactive_release sent<br/>and preview remains subscribed
    interactive_active --> visible: interactive_release sent<br/>and no preview subscription
    visible --> absent: session_gone received
    preview_active --> absent: session_gone received
    interactive_active --> absent: session_gone received
```

### State Meanings

- `absent` - the session is not currently present in the daemon roster.
- `visible` - the row is visible and usable in the official app because its computer is active.
- `preview_active` - the app subscribed to preview and is receiving live preview snapshots for this session.
- `interactive_pending` - the app sent `interactive_request` and is awaiting daemon response.
- `interactive_active` - daemon opened an interactive stream; snapshot and live bytes are flowing.

### Rules

- Free and Pro use the same per-session UI lifecycle once the computer is active.
- Android may choose which previews to subscribe to for performance or list UX, but not as a tier gate.
- `interactive_denied(session_unavailable)` should be followed by row removal once `session_gone` is processed.

### Daemon-Side Mirror

From the daemon's perspective, a paired device may:

- subscribe or unsubscribe preview for any session
- request interactive for any session
- receive grant / deny per session

The daemon does not model tier, active computer count, or selected sessions.

## Trusted-Computer Policy Lifecycle

This state is owned by the official Android app per account.

```mermaid
stateDiagram-v2
    [*] --> loading
    loading --> free_none: tier = free,<br/>no trusted computer
    loading --> free_active_one: tier = free,<br/>one active trusted computer
    loading --> free_resolution_required: tier = free,<br/>multiple active trusted computers
    loading --> pro_active: tier = pro,<br/>0-10 trusted computers

    free_none --> replacing_or_pairing: user starts pairing
    free_active_one --> replacing_or_pairing: user starts Replace Computer
    replacing_or_pairing --> free_active_one: SAS succeeds,<br/>new trust becomes active
    replacing_or_pairing --> free_none: SAS fails or canceled,<br/>no previous trust
    replacing_or_pairing --> free_active_one: SAS fails or canceled,<br/>old trust remains active

    free_resolution_required --> free_active_one: user chooses one computer
    pro_active --> pro_limit_reached: trusted computer count = 10
    pro_limit_reached --> pro_active: user removes a computer
    pro_active --> free_resolution_required: tier changes to free,<br/>trusted count > 1
    pro_active --> free_active_one: tier changes to free,<br/>trusted count = 1
    pro_active --> free_none: tier changes to free,<br/>trusted count = 0
```

### Notes

- Relay provides only the tier input for this machine.
- Android local state provides trusted-computer inventory.
- Free auto-connects only the one active trusted computer.
- Pro auto-connects online trusted computers up to the ten-computer limit.
- Downgrade resolution blocks multi-computer auto-connect until the user chooses one active computer.
- Replace Computer is transactional: failed or canceled new pairing leaves the old active trust unchanged.
- TODO: add daemon-side old-trust revoke after Replace Computer success.

## Cross-Reference

- preview and interactive frame behavior are in `../protocol/transport.md`
- Free / Pro computer rules are described in `../ux/subscription.md`
- recommended Android behavior is in `../ux/android.md`

## Related Documents

- `../architecture.md`
- `../contract.md`
- `../protocol/transport.md`
- `../protocol/relay.md`
- `../ux/subscription.md`
- `error-codes.md`
- `sequence-flows.md`
