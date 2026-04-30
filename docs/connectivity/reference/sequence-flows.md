# Connectivity Sequence Flows

## Status

This document captures detailed end-to-end sequence flows for the target connectivity architecture under `docs/connectivity/`.

It is intentionally more operational than `architecture.md`. Its purpose is to make the moving pieces easy to reason about before implementation:

- pairing
- SAS confirmation
- STUN-assisted direct connection attempts
- relay fallback tunnel establishment
- daemon-brokered session synchronization
- preview and interactive attach over QUIC streams

These flows describe the intended target design. They are not a statement that the current repository already implements them.

## Actors

The diagrams use these actors:

- `Android UI`: the visible mobile app screens and user actions
- `Android ConnMgr`: the mobile connectivity manager responsible for Relay control-plane and daemon transport
- `Relay RT`: Relay realtime control plane and app policy API surface
- `Relay Tunnel`: Relay fallback packet tunnel service
- `STUN`: self-hosted STUN service operated in the same edge footprint as Relay
- `Daemon ConnMgr`: daemon-side connectivity manager
- `Daemon Broker`: daemon-side local broker between mobile transport and local `tunnel run` sessions
- `tunnel run`: the local session owner that owns PTY, mirror, preview source, and interactive attach

## Flow 0: Local Session Registration

```mermaid
sequenceDiagram
    autonumber
    participant TunnelRun as tunnel run
    participant DaemonBroker as Daemon Broker

    TunnelRun->>DaemonBroker: open local socket
    TunnelRun->>DaemonBroker: register_session\n(session_id, metadata)
    TunnelRun->>DaemonBroker: preview_update(latest preview)
    loop while session lives
        TunnelRun->>DaemonBroker: session_update(metadata changes)
        TunnelRun->>DaemonBroker: preview_update(preview changes)
    end
    TunnelRun->>DaemonBroker: session_gone
    TunnelRun-->>DaemonBroker: close local connection
```

### What This Flow Shows

- every `tunnel run` explicitly registers itself with daemon
- the local connection lifetime is the primary liveness signal
- daemon keeps the latest preview cached even before any mobile preview subscription arrives

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
    DaemonConn->>RelayRT: reserve short-lived correlation_id
    RelayRT-->>DaemonConn: correlation_id + account_id
    DaemonCLI->>DaemonConn: create one-time invitation
    DaemonConn-->>DaemonCLI: invitation payload\n(account_id, daemon_id, daemon_pubkey,\ninvitation_id, nonce, expires_at,\ncorrelation_id, signature)
    DaemonCLI-->>User: print JSON invitation payload

    User->>AndroidUI: import invitation payload
    AndroidUI->>AndroidConn: parse invitation
    AndroidConn->>AndroidConn: verify daemon signature\nverify expiry
    AndroidConn->>AndroidConn: sign(invitation_id || nonce || android_pubkey || relay_asserted_account_id)
    AndroidConn->>RelayRT: pair_response_submit\n(correlation_id, android_pubkey, signature, account_id)
    RelayRT->>DaemonConn: pair_response_forward\n(+ relay_asserted_account_id)
    DaemonConn->>DaemonConn: verify invitation still valid\nverify Android signature\nverify account matches invitation\nstore pending response

    DaemonConn->>DaemonConn: derive SAS from:\ndaemon_pubkey, android_pubkey,\ninvitation_id, nonce
    AndroidConn->>AndroidConn: derive same SAS from:\ndaemon_pubkey, android_pubkey,\ninvitation_id, nonce

    DaemonConn-->>DaemonCLI: show 6-digit SAS
    AndroidConn-->>AndroidUI: show 6-digit SAS
    User->>DaemonCLI: confirm SAS matches
    User->>AndroidUI: confirm SAS matches

    DaemonConn->>DaemonConn: persist Android fingerprint
    AndroidConn->>AndroidConn: persist daemon fingerprint
    DaemonConn->>RelayRT: pair_completed
    RelayRT-->>AndroidConn: paired_device_visible
```

### What This Flow Establishes

- Relay transports pairing messages but is not the trust root for device identity.
- The daemon-authored invitation is the initial device trust anchor.
- Android proves possession of its device key by signing the invitation challenge.
- Account binding is based on Relay assertion, and that assertion is bound into the signed pairing transcript.
- The 6-digit SAS gives the user a final MITM check.

## Flow 2: App Startup To Direct Connection Success

```mermaid
sequenceDiagram
    autonumber
    participant AndroidUI as Android UI
    participant AndroidConn as Android ConnMgr
    participant RelayRT as Relay RT
    participant STUN
    participant DaemonConn as Daemon ConnMgr
    participant DaemonBroker as Daemon Broker

    AndroidUI->>AndroidConn: app foreground / user logged in
    AndroidConn->>RelayRT: fetch subscription tier
    RelayRT-->>AndroidConn: {tier}
    AndroidConn->>RelayRT: open app realtime websocket
    AndroidConn->>RelayRT: app_register(app_version, protocol_version)
    RelayRT-->>AndroidConn: daemon_snapshot
    AndroidConn-->>AndroidUI: render visible daemon cards

    AndroidUI->>AndroidConn: user opens one daemon card
    AndroidConn->>AndroidConn: open transport for that daemon card
    AndroidConn->>STUN: Binding Request
    STUN-->>AndroidConn: Binding Response\n(public A_ip:A_port)

    AndroidConn->>RelayRT: rendezvous_open\n(daemon_id, attempt_id, A_ip:A_port, private addrs)
    RelayRT->>DaemonConn: rendezvous_hint\n(Android candidates)
    DaemonConn->>STUN: Binding Request
    STUN-->>DaemonConn: Binding Response\n(public D_ip:D_port)
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
    AndroidConn->>DaemonConn: hello(path=direct)
    DaemonConn->>AndroidConn: hello(path=direct)
    DaemonBroker->>DaemonConn: current session roster
    DaemonConn->>AndroidConn: session_index
    DaemonConn->>AndroidConn: path_state(path=direct, attempt_id)
    AndroidConn->>AndroidConn: apply local free/pro rule
    AndroidConn-->>AndroidUI: render daemon card + session metadata\nbadge = Direct
```

### What This Flow Shows

- Relay tells Android which daemons are visible and which account tier applies.
- STUN is only used to learn public UDP mappings.
- Relay carries rendezvous hints but never terminal data.
- daemon transport starts when the user opens a daemon card, not eagerly for every visible daemon
- Direct success means the daemon becomes the source of session list and preview / interactive routing.
- The free-tier unlocked row is chosen locally by the app within that daemon card after the initial `session_index`.

## Flow 3: Direct Attempt Fails And Falls Back To Relay

```mermaid
sequenceDiagram
    autonumber
    participant AndroidConn as Android ConnMgr
    participant RelayRT as Relay RT
    participant RelayTunnel as Relay Tunnel
    participant DaemonConn as Daemon ConnMgr
    participant DaemonBroker as Daemon Broker

    AndroidConn->>RelayRT: daemon online already known
    AndroidConn->>RelayRT: rendezvous_open(attempt_id)
    RelayRT->>DaemonConn: rendezvous_hint(attempt_id)
    DaemonConn->>RelayRT: rendezvous_hint(attempt_id)
    RelayRT->>AndroidConn: rendezvous_hint(attempt_id)

    par direct attempt
        AndroidConn->>DaemonConn: UDP probes + QUIC direct attempt
        DaemonConn->>AndroidConn: UDP probes + QUIC direct attempt
    end

    Note over AndroidConn,DaemonConn: direct attempt deadline expires<br/>without QUIC handshake completion
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
    AndroidConn->>DaemonConn: hello(path=relay)
    DaemonConn->>AndroidConn: hello(path=relay)
    DaemonBroker->>DaemonConn: current session roster
    DaemonConn->>AndroidConn: session_index
    DaemonConn->>AndroidConn: path_state(path=relay, fallback_reason)
    AndroidConn->>AndroidConn: apply local free/pro rule
    AndroidConn-->>AndroidUI: render session metadata\nbadge = Relay
```

### What This Flow Shows

- Direct and relay are different carriers, not different business protocols.
- Fallback creates a new QUIC/TLS connection. It does not migrate the failed direct connection in place.
- Relay Tunnel forwards encrypted QUIC packets only.
- After fallback succeeds, daemon resends session list over the new connection.

## Flow 4: Preview And Interactive For The Usable Session

```mermaid
sequenceDiagram
    autonumber
    participant AndroidUI as Android UI
    participant AndroidConn as Android ConnMgr
    participant DaemonConn as Daemon ConnMgr
    participant DaemonBroker as Daemon Broker
    participant TunnelRun as tunnel run

    AndroidUI->>AndroidConn: open session S1 detail view
    AndroidConn->>AndroidConn: verify S1 is currently usable\nunder local free/pro rule
    AndroidConn->>DaemonConn: preview_subscribe(session_id=S1)\non control stream
    DaemonBroker->>DaemonConn: latest cached preview for S1
    DaemonConn->>AndroidConn: preview_snapshot(S1)\non control stream

    AndroidConn->>DaemonConn: interactive_request(session_id=S1, cols, rows)\non control stream
    DaemonConn->>DaemonBroker: request interactive attach for S1
    DaemonBroker->>TunnelRun: attach interactive for S1
    TunnelRun-->>DaemonBroker: granted(S1)
    DaemonBroker-->>DaemonConn: interactive granted(S1)
    DaemonConn-->>AndroidConn: interactive_granted\n(session_id=S1, interactive_stream_id=I1)
    DaemonConn->>AndroidConn: open interactive stream I1
    TunnelRun->>DaemonBroker: snapshot bytes for S1
    DaemonBroker->>DaemonConn: snapshot bytes for S1
    DaemonConn->>AndroidConn: snapshot_begin / snapshot_chunk / snapshot_end
    TunnelRun->>DaemonBroker: live bytes for S1
    DaemonBroker->>DaemonConn: live bytes for S1
    DaemonConn->>AndroidConn: live_bytes...\non stream I1
    AndroidConn-->>AndroidUI: render terminal for S1

    AndroidUI->>AndroidConn: user types into S1
    AndroidConn->>DaemonConn: input_text(session_id=S1, ...)\non control stream
    DaemonConn->>DaemonBroker: forward input for S1
    DaemonBroker->>TunnelRun: PTY input for S1
```

### What This Flow Shows

- `tunnel run` remains the PTY owner.
- The daemon is a gateway and local broker, not the terminal owner.
- The official app asks for preview / interactive only for sessions it treats as usable.
- No Relay-issued per-session access token is involved in phase 1.

## Flow 4B: Pro Preview Bootstrap

```mermaid
sequenceDiagram
    autonumber
    participant AndroidConn as Android ConnMgr
    participant DaemonConn as Daemon ConnMgr
    participant DaemonBroker as Daemon Broker

    Note over AndroidConn: account tier = pro

    DaemonConn->>AndroidConn: session_index
    AndroidConn->>AndroidConn: mark all rows usable

    loop for each live session in roster
        AndroidConn->>DaemonConn: preview_subscribe(session_id)
        DaemonBroker->>DaemonConn: latest cached preview for session_id
        DaemonConn->>AndroidConn: preview_snapshot(session_id)
    end
```

### What This Flow Shows

- pro does not wait for row-by-row user entry before showing live preview within a connected daemon card
- pro still needs the user to open that daemon card first in phase 1
- daemon can answer immediately because the latest preview is already cached from local `tunnel run` push

## Flow 5: Free-Tier Sticky First-Attach

```mermaid
sequenceDiagram
    autonumber
    participant AndroidUI as Android UI
    participant AndroidConn as Android ConnMgr
    participant DaemonConn as Daemon ConnMgr
    participant DaemonBroker as Daemon Broker
    participant TunnelRun as tunnel run

    Note over AndroidConn: Within one daemon card,\nfree tier starts with no unlocked session

    AndroidUI->>AndroidConn: tap session row S2
    AndroidConn-->>AndroidUI: show first-use explanation\n("Tap again to start using S2 on free tier")
    AndroidUI->>AndroidConn: confirm open S2
    AndroidConn->>AndroidConn: set unlocked_session_id = S2
    AndroidConn-->>AndroidUI: lock state updates\nS2 unlocked, other rows locked

    TunnelRun->>DaemonBroker: session S2 exits
    DaemonBroker->>DaemonConn: session_gone(S2)
    DaemonConn->>AndroidConn: session_gone(S2)
    AndroidConn->>AndroidConn: clear unlocked_session_id
    AndroidConn-->>AndroidUI: all rows locked again\nuntil the next user attach
```

### What This Flow Shows

- the first user attach picks the sticky unlocked session for that daemon card
- locked rows stay visible
- when the sticky unlocked session disappears, the card returns to "no unlocked session yet"
- different devices may pick different sticky sessions; phase 1 does not synchronize that choice through Relay

## Flow 6: Reconnect And Fresh Recovery

```mermaid
sequenceDiagram
    autonumber
    participant AndroidConn as Android ConnMgr
    participant RelayRT as Relay RT
    participant RelayTunnel as Relay Tunnel
    participant DaemonConn as Daemon ConnMgr
    participant DaemonBroker as Daemon Broker
    participant TunnelRun as tunnel run

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
    DaemonBroker->>DaemonConn: current session roster
    DaemonConn->>AndroidConn: session_index
    AndroidConn->>AndroidConn: recompute usable vs locked rows

    loop for each session Android still wants live preview for
        AndroidConn->>DaemonConn: preview_subscribe(session_id)
    end

    loop for each still-usable session Android still wants interactive
        AndroidConn->>DaemonConn: interactive_request(session_id)
        DaemonConn->>DaemonBroker: ask local tunnel run
        TunnelRun-->>DaemonBroker: granted
        DaemonBroker-->>DaemonConn: granted
        DaemonConn-->>AndroidConn: interactive_granted(stream_id)
        DaemonConn->>AndroidConn: fresh snapshot on that stream
        DaemonConn->>AndroidConn: live bytes continue
    end
```

### What This Flow Shows

- reconnect is path-agnostic
- recovery is based on fresh daemon state, not missed-byte replay
- preview and interactive are re-established from current app state after reconnect

## State Ownership Summary

### Android ConnMgr Owns

- app policy fetch
- app realtime websocket lifecycle
- per-daemon transport lifecycle
- local free/pro rule evaluation
- preview / interactive subscriptions the official app chooses to request

### Daemon ConnMgr Owns

- paired-device validation
- direct-vs-relay carrier selection
- QUIC/TLS transport lifecycle
- stream creation and routing

### Daemon Broker Owns

- local session directory
- accepting explicit local `register_session` connections from `tunnel run`
- caching the latest preview pushed by each owning `tunnel run`
- fanning cached preview updates to subscribed mobile clients
- forwarding interactive attach requests to the owning `tunnel run`
- bridging snapshot / live bytes / input between transport and local session owners

### `tunnel run` Owns

- PTY lifecycle
- mirror and preview source
- snapshot generation
- live terminal bytes
- terminal input handling

### Relay Owns

- subscription tier exposure to the official app
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

- `../architecture.md`
- `../contract.md`
- `../protocol/pairing.md`
- `../protocol/relay.md`
- `../protocol/transport.md`
- `../protocol/local-broker.md`
- `../ux/android.md`
