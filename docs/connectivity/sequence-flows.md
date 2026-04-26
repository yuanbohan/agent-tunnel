# Connectivity Sequence Flows

## Status

This document captures detailed end-to-end sequence flows for the target connectivity architecture under `docs/connectivity/`.

It is intentionally more operational than `architecture.md`. Its purpose is to make the moving pieces easy to reason about before implementation:

- pairing
- SAS confirmation
- STUN-assisted direct connection attempts
- relay fallback tunnel establishment
- daemon-driven session synchronization
- interactive session attachment over multiple QUIC streams

These flows describe the intended target design. They are not a statement that the current repository already implements them.

## Actors

The diagrams use these actors:

- `Android UI`: the visible mobile app screens and user actions
- `Android ConnMgr`: the mobile connectivity manager responsible for relay control-plane and daemon transport
- `Relay RT`: Relay realtime control plane
- `Relay Tunnel`: Relay fallback packet tunnel service
- `STUN`: self-hosted STUN service operated in the same edge footprint as Relay
- `Daemon ConnMgr`: daemon-side connectivity manager
- `Daemon SessionMgr`: daemon-side session authority, preview generator, and PTY bridge

## Flow 1: Pairing Invitation And SAS Confirmation

```mermaid
sequenceDiagram
    autonumber
    participant User
    participant DaemonCLI as tunnel daemon pair
    participant DaemonConn as Daemon ConnMgr
    participant RelayRT as Relay RT
    participant AndroidUI as Android UI
    participant AndroidConn as Android ConnMgr

    User->>DaemonCLI: run `tunnel daemon pair`
    DaemonCLI->>DaemonConn: create one-time invitation
    DaemonConn->>RelayRT: reserve short-lived correlation_id
    RelayRT-->>DaemonConn: correlation_id
    DaemonConn-->>DaemonCLI: invitation payload\n(account_id, daemon_id, daemon_pubkey,\ninvitation_id, nonce, expires_at,\ncorrelation_id, signature)
    DaemonCLI-->>User: render QR code

    User->>AndroidUI: scan QR
    AndroidUI->>AndroidConn: parse invitation
    AndroidConn->>AndroidConn: verify daemon signature\nverify account binding\nverify expiry
    AndroidConn->>AndroidConn: sign(invitation_id || nonce || android_pubkey)
    AndroidConn->>RelayRT: pair_response_submit\n(correlation_id, android_pubkey, signature)
    RelayRT->>DaemonConn: pair_response_forward
    DaemonConn->>DaemonConn: verify invitation still valid\nverify Android signature

    DaemonConn->>DaemonConn: derive SAS from:\ndaemon_pubkey, android_pubkey,\ninvitation_id, nonce
    AndroidConn->>AndroidConn: derive same SAS from:\ndaemon_pubkey, android_pubkey,\ninvitation_id, nonce

    DaemonConn-->>DaemonCLI: show 6-digit SAS
    AndroidConn-->>AndroidUI: show 6-digit SAS
    User->>DaemonCLI: confirm SAS matches
    User->>AndroidUI: confirm SAS matches

    DaemonConn->>DaemonConn: persist Android fingerprint
    AndroidConn->>AndroidConn: persist daemon fingerprint
    DaemonConn->>RelayRT: pair_completed\n(derived visibility grant only)
    RelayRT-->>AndroidConn: paired_device_visible
```

### What This Flow Establishes

- Relay transports pairing messages but is not the trust root.
- The daemon-authored invitation is the initial trust anchor.
- The Android device proves possession of its device key by signing the invitation challenge.
- The 6-digit SAS gives the user a final MITM check.
- Successful pairing produces pinned peer identities on Android and daemon. It does not produce one long-lived shared transport secret.

## Flow 2: App Startup To Direct Connection Success

```mermaid
sequenceDiagram
    autonumber
    participant AndroidUI as Android UI
    participant AndroidConn as Android ConnMgr
    participant RelayRT as Relay RT
    participant STUN
    participant DaemonConn as Daemon ConnMgr
    participant DaemonSess as Daemon SessionMgr

    AndroidUI->>AndroidConn: app foreground / user logged in
    AndroidConn->>RelayRT: open app realtime websocket
    RelayRT-->>AndroidConn: daemon_snapshot
    RelayRT-->>AndroidConn: realtime_ready
    AndroidConn-->>AndroidUI: render visible daemon cards

    AndroidConn->>AndroidConn: select visible online paired daemon
    AndroidConn->>STUN: Binding Request
    STUN-->>AndroidConn: Binding Response\n(public A_ip:A_port)

    DaemonConn->>STUN: Binding Request
    STUN-->>DaemonConn: Binding Response\n(public D_ip:D_port)

    AndroidConn->>RelayRT: rendezvous_open\n(daemon_id, attempt_id, A_ip:A_port, private addrs)
    RelayRT->>DaemonConn: rendezvous_hint\n(Android candidates)
    DaemonConn->>RelayRT: rendezvous_hint\n(daemon_id, attempt_id, D_ip:D_port, private addrs)
    RelayRT->>AndroidConn: rendezvous_hint\n(Daemon candidates)

    par UDP hole punching
        AndroidConn->>DaemonConn: UDP probe packets to daemon candidates
        DaemonConn->>AndroidConn: UDP probe packets to Android candidates
    end

    AndroidConn->>DaemonConn: QUIC/TLS handshake over direct UDP
    DaemonConn->>AndroidConn: QUIC/TLS handshake over direct UDP
    AndroidConn->>AndroidConn: verify daemon pinned identity
    DaemonConn->>DaemonConn: verify Android pinned identity

    AndroidConn->>DaemonConn: open control stream
    DaemonConn->>AndroidConn: hello(path=direct)
    DaemonConn->>AndroidConn: session_index
    AndroidConn-->>AndroidUI: render session metadata\nbadge = Direct
```

### What This Flow Shows

- Relay tells Android which daemons are visible and online.
- STUN is only used to learn public UDP mappings.
- Relay carries rendezvous hints but never terminal data.
- Direct success means the daemon becomes the source of session list and preview data.
- Path state is tracked per daemon connection, so the Android badge is `Direct` for that daemon.

## Flow 3: Direct Attempt Fails And Falls Back To Relay

```mermaid
sequenceDiagram
    autonumber
    participant AndroidUI as Android UI
    participant AndroidConn as Android ConnMgr
    participant RelayRT as Relay RT
    participant RelayTunnel as Relay Tunnel
    participant DaemonConn as Daemon ConnMgr
    participant DaemonSess as Daemon SessionMgr

    AndroidConn->>RelayRT: daemon online already known
    AndroidConn->>RelayRT: rendezvous_open(attempt_id)
    RelayRT->>DaemonConn: rendezvous_hint(attempt_id)
    DaemonConn->>RelayRT: rendezvous_hint(attempt_id)
    RelayRT->>AndroidConn: rendezvous_hint(attempt_id)

    par direct attempt
        AndroidConn->>DaemonConn: UDP probes + QUIC direct attempt
        DaemonConn->>AndroidConn: UDP probes + QUIC direct attempt
    end

    Note over AndroidConn,DaemonConn: 3s direct attempt deadline expires<br/>without QUIC handshake completion
    AndroidConn->>AndroidConn: cancel direct attempt
    DaemonConn->>DaemonConn: cancel direct attempt

    AndroidConn->>RelayRT: relay_tunnel_request(attempt_id, actor=android)
    DaemonConn->>RelayRT: relay_tunnel_request(attempt_id, actor=daemon)
    RelayRT-->>AndroidConn: relay_tunnel_ready(android_token)
    RelayRT-->>DaemonConn: relay_tunnel_ready(daemon_token)

    AndroidConn->>RelayTunnel: open websocket tunnel(android_token, attempt_id)
    DaemonConn->>RelayTunnel: open websocket tunnel(daemon_token, attempt_id)
    RelayTunnel->>RelayTunnel: pair Android and daemon tunnels by attempt_id

    AndroidConn->>RelayTunnel: encrypted QUIC packets
    RelayTunnel->>DaemonConn: forward encrypted QUIC packets
    DaemonConn->>RelayTunnel: encrypted QUIC packets
    RelayTunnel->>AndroidConn: forward encrypted QUIC packets

    AndroidConn->>DaemonConn: new QUIC/TLS handshake over relay tunnel
    DaemonConn->>AndroidConn: new QUIC/TLS handshake over relay tunnel
    AndroidConn->>AndroidConn: verify daemon pinned identity
    DaemonConn->>DaemonConn: verify Android pinned identity

    AndroidConn->>DaemonConn: open control stream
    DaemonConn->>AndroidConn: hello(path=relay)
    DaemonConn->>AndroidConn: session_index
    AndroidConn-->>AndroidUI: render session metadata\nbadge = Relay
```

### What This Flow Shows

- Direct and relay are different carriers, not different business protocols.
- Fallback creates a new QUIC/TLS connection. It does not migrate the failed direct connection in place.
- Relay Tunnel forwards encrypted QUIC packets only.
- After fallback succeeds, daemon resends session list and preview data over the new connection.
- The direct attempt deadline is fixed at `3s` in phase 1. Sequential (not happy-eyeballs) is chosen so the path-state badge stays unambiguous. See `transport-protocol.md` for the deadline contract.

## Flow 4: Interactive Session Attach Over QUIC Streams

```mermaid
sequenceDiagram
    autonumber
    participant AndroidUI as Android UI
    participant AndroidConn as Android ConnMgr
    participant RelayRT as Relay RT
    participant DaemonConn as Daemon ConnMgr
    participant DaemonSess as Daemon SessionMgr

    AndroidUI->>AndroidConn: open session S1 detail view
    AndroidConn->>RelayRT: request active-session lease for S1
    RelayRT-->>AndroidConn: lease_token(session_id=S1)
    AndroidConn->>DaemonConn: session_activate(session_id=S1, lease_token)\non control stream
    DaemonConn->>DaemonSess: validate token and activate S1 preview
    DaemonSess->>DaemonConn: preview snapshots for S1
    DaemonConn->>AndroidConn: preview_snapshot(S1)\non control stream
    AndroidConn->>DaemonConn: interactive_request(session_id=S1, cols, rows)\non control stream
    DaemonConn->>DaemonSess: ask for interactive attach for S1
    DaemonSess-->>DaemonConn: granted(S1)
    DaemonConn-->>AndroidConn: interactive_granted\n(session_id=S1, interactive_stream_id=I1)
    DaemonConn->>AndroidConn: open interactive stream I1
    DaemonSess->>DaemonConn: snapshot bytes for S1
    DaemonConn->>AndroidConn: snapshot_begin(S1)\non stream I1
    DaemonConn->>AndroidConn: snapshot_chunk...\non stream I1
    DaemonConn->>AndroidConn: snapshot_end(S1)\non stream I1
    DaemonSess->>DaemonConn: live bytes for S1
    DaemonConn->>AndroidConn: live_bytes...\non stream I1
    AndroidConn-->>AndroidUI: render terminal for S1

    AndroidUI->>AndroidConn: user types into S1
    AndroidConn->>DaemonConn: input_text(session_id=S1, ...)\non control stream
    DaemonConn->>DaemonSess: forward PTY input for S1
```

### What This Flow Shows

- Interactive control messages stay on the control stream.
- Terminal payload bytes stay on a dedicated interactive stream.
- The daemon is authoritative for whether a session may become interactive.

## Flow 5: Multiple Interactive Sessions On One Daemon Connection

```mermaid
sequenceDiagram
    autonumber
    participant AndroidUI as Android UI
    participant AndroidConn as Android ConnMgr
    participant RelayRT as Relay RT
    participant DaemonConn as Daemon ConnMgr
    participant DaemonSess as Daemon SessionMgr

    AndroidUI->>AndroidConn: open session S1 detail
    AndroidConn->>RelayRT: request active-session lease for S1
    RelayRT-->>AndroidConn: lease_token(S1)
    AndroidConn->>DaemonConn: session_activate(S1, lease_token)\non control stream
    AndroidConn->>DaemonConn: interactive_request(S1)\non control stream
    DaemonConn->>DaemonSess: attach S1
    DaemonSess-->>DaemonConn: granted S1
    DaemonConn-->>AndroidConn: interactive_granted(S1, stream I1)
    DaemonConn->>AndroidConn: open interactive stream I1

    AndroidUI->>AndroidConn: also open session S2 detail
    AndroidConn->>RelayRT: request active-session lease for S2
    RelayRT-->>AndroidConn: lease_token(S2)
    AndroidConn->>DaemonConn: session_activate(S2, lease_token)\non control stream
    AndroidConn->>DaemonConn: interactive_request(S2)\non control stream
    DaemonConn->>DaemonSess: attach S2
    DaemonSess-->>DaemonConn: granted S2
    DaemonConn-->>AndroidConn: interactive_granted(S2, stream I2)
    DaemonConn->>AndroidConn: open interactive stream I2

    par stream I1 for S1
        DaemonConn->>AndroidConn: snapshot/live for S1 on I1
    and stream I2 for S2
        DaemonConn->>AndroidConn: snapshot/live for S2 on I2
    end

    AndroidConn->>DaemonConn: input_text(session_id=S1, ...)\non control stream
    AndroidConn->>DaemonConn: input_text(session_id=S2, ...)\non control stream
```

### What This Flow Shows

- There is one daemon connection path, but potentially many interactive sessions under it.
- The path badge remains a daemon-level property:
  - all sessions on this daemon are either currently `Direct`
  - or currently `Relay`
- Interactive state remains session-scoped:
  - grant/deny
  - stream binding
  - input routing

This flow is the general protocol capability. On free tier, Relay should deny the second active-session lease request, so the app will never reach the second `session_activate` step while the first lease is still held.

## Flow 6: Reconnect And Fresh Recovery

```mermaid
sequenceDiagram
    autonumber
    participant AndroidConn as Android ConnMgr
    participant RelayRT as Relay RT
    participant RelayTunnel as Relay Tunnel
    participant DaemonConn as Daemon ConnMgr
    participant DaemonSess as Daemon SessionMgr

    Note over AndroidConn,DaemonConn: existing daemon connection drops

    AndroidConn->>RelayRT: daemon still visible, start reconnect loop
    AndroidConn->>DaemonConn: try direct again if hints remain valid
    alt direct succeeds
        AndroidConn->>DaemonConn: new QUIC/TLS over UDP
    else direct fails
        AndroidConn->>RelayTunnel: open fallback tunnel
        DaemonConn->>RelayTunnel: open fallback tunnel
        AndroidConn->>DaemonConn: new QUIC/TLS over relay tunnel
    end

    DaemonConn->>AndroidConn: hello(path=direct or relay)
    DaemonConn->>AndroidConn: session_index
    DaemonConn->>AndroidConn: preview_snapshot...

    loop for each session Android still wants interactive
        AndroidConn->>DaemonConn: interactive_request(session_id)
        DaemonConn->>DaemonSess: attach session
        DaemonSess-->>DaemonConn: granted
        DaemonConn-->>AndroidConn: interactive_granted(stream_id)
        DaemonConn->>AndroidConn: fresh snapshot on that stream
        DaemonConn->>AndroidConn: live bytes continue
    end
```

### What This Flow Shows

- Reconnect is path-agnostic.
- Recovery is based on fresh daemon state, not missed-byte replay.
- Each still-needed interactive session is reattached independently.

## State Ownership Summary

### Android ConnMgr Owns

- app realtime websocket lifecycle
- per-daemon transport lifecycle:
  - `connecting_direct`
  - `connecting_relay`
  - `connected_direct`
  - `connected_relay`
  - `reconnecting`
- control stream handle per daemon
- interactive stream handles per session

### Daemon ConnMgr Owns

- paired-device validation
- direct-vs-relay carrier selection
- QUIC/TLS transport lifecycle
- stream creation and routing

### Daemon SessionMgr Owns

- session list
- preview generation
- PTY snapshot/live byte production
- per-session interactive attach decisions

### Relay Owns

- daemon presence
- pairing transport
- rendezvous hint exchange
- fallback tunnel pairing

Relay does not own:

- session list
- preview content
- interactive grants
- terminal byte routing semantics

## Related Documents

- `docs/connectivity/architecture.md`
- `docs/connectivity/pairing-protocol.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/mobile-reference.md`
- `docs/connectivity/android-client-behavior.md`
- `docs/connectivity/daemon-session-sync.md`
