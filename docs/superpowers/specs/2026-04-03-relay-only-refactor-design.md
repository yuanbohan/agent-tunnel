# Relay-Only Refactor Design

**Date:** 2026-04-03
**Status:** Approved

## Goal

Refactor agent-tunnel to remove all legacy and localhost-only code, making the relay the sole browser access path. Simplify the codebase for long-term maintenance with a deep package restructure. Produce protocol documentation sufficient for native mobile client development.

## Decisions

| Decision | Choice |
|----------|--------|
| Localhost web server in agentunnel | **Remove** — browser access only through relay |
| Local terminal in agentunnel | **Keep** — interactive stdin/stdout at the launching terminal |
| Relay connection | **Mandatory** — agentunnel refuses to start without relay URL + token |
| Mobile client strategy | **Protocol documentation only** — no Go Mobile library |
| Web UI loading | **Relay server only** — agentunnel has zero webui dependency |
| Relay server port | **Configurable** via `--port` flag (highest precedence), `AGENTUNNEL_RELAY_ADDR` env var, or default `8586` |

## Scope

### Delete

| Path | Reason |
|------|--------|
| `cmd/agent/` | Legacy shell-over-WebSocket server |
| `cmd/client/` | Legacy shell-over-WebSocket client |
| `internal/agent/` | Legacy PTY + single-WebSocket handler |
| `internal/client/` | Legacy WebSocket client + raw terminal |
| `internal/server/` | Localhost HTTP/WebSocket server for agentunnel |
| `internal/relayapi/` | Merged into `protocol/` |
| `internal/` | Entire directory removed — packages promoted to repo root |
| `web/index.html` | Localhost single-session entrypoint |
| `web/src/main.ts` | Localhost app bootstrap |
| `web/src/session_url.ts` | Localhost WebSocket URL derivation |
| `web/src/input_filter.test.ts` | Test file for deleted `main.ts` import (filter kept as shared module) |

### Rename + Move (Package Restructure)

All Go packages move from `internal/` to repo root. Additionally:

| Current | Proposed | Rationale |
|---------|----------|-----------|
| `internal/relayclient/` | `connector/` | Relay is the only mode; "connector" is clearer |
| `internal/relayserver/` | `relay/` | Shorter; no ambiguity with agent-side `connector` |
| `internal/session/` | `session/` | Same name, promoted to root |
| `internal/protocol/` | `protocol/` | Same name, promoted to root |
| `internal/launcher/` | `launcher/` | Same name, promoted to root |
| `internal/webui/` | `webui/` | Same name, promoted to root |

### Merge

| Source | Target | What moves |
|--------|--------|------------|
| `internal/relayapi/` | `protocol/` | `SessionInfo`, `AgentFrame`, `RegisterFrame()` — wire types that belong alongside `Message` |

### Web Module Renames

| Current | Proposed | Rationale |
|---------|----------|-----------|
| `web/relay.html` | `web/index.html` | Single entrypoint, no disambiguation needed |
| `web/src/relay_app.ts` | `web/src/app.ts` | Drop `relay_` prefix |
| `web/src/relay_routes.ts` | `web/src/routes.ts` | Drop `relay_` prefix |
| `web/src/relay_api.ts` | `web/src/api.ts` | Drop `relay_` prefix |
| `web/src/relay_dashboard.ts` | `web/src/dashboard.ts` | Drop `relay_` prefix |
| `web/src/relay_session_page.ts` | `web/src/session_page.ts` | Drop `relay_` prefix |
| `web/src/relay_types.ts` | `web/src/types.ts` | Drop `relay_` prefix |
| `web/src/relay.css` | `web/src/style.css` | Drop `relay_` prefix |

### Keep (Unchanged Role, New Location)

| Path | Role |
|------|------|
| `cmd/agentunnel/` | Agent binary (simplified startup) |
| `cmd/relay/` | Relay server binary |
| `web/src/terminal.ts` | xterm.js wrapper (shared) |
| `web/src/connection.ts` | WebSocket manager (shared) |
| `web/src/protocol.ts` | TS-side message encoding (shared) |
| `web/src/input_filter.ts` | xterm auto-response filter (shared; was only used by localhost but relay input also needs it) |

## Targeted Improvement: Relay Input Filtering

The relay session page (`app.ts`, formerly `relay_app.ts`) forwards `terminal.onData` input without filtering xterm auto-response sequences (CPR, DA reports, etc.). The localhost `main.ts` had this filter but the relay page never got it. As part of this refactor, integrate `input_filter.ts` into the relay input path so that `encodeInput` is only called for real user input. This prevents feedback loops when the relay terminal receives query responses from xterm.js.

## Repo Layout (Post-Refactor)

```
agent-tunnel/
  cmd/
    agentunnel/          # agent binary
    relay/               # relay server binary
  protocol/              # wire types (Message, AgentFrame, SessionInfo)
  session/               # PTY lifecycle, Hub, local terminal
  connector/             # outbound relay client
  launcher/              # executable resolution (claude/codex/gemini)
  relay/                 # relay server logic (auth, registry, handlers)
  webui/                 # embedded web assets
  web/                   # TypeScript source
  docs/
```

## Package Dependency Graph (Post-Refactor)

```
cmd/agentunnel
├── connector/      ← mandatory outbound relay connection
├── protocol/       ← wire types (Message, AgentFrame, SessionInfo)
├── launcher/       ← resolves executable name to PATH
├── session/        ← PTY lifecycle, Hub, local terminal
└── (stdlib: context, os, syscall, signal)

cmd/relay
├── relay/          ← auth, registry, preview, HTTP/WS handlers
│   ├── protocol/   ← wire types
│   └── webui/      ← embedded web assets
└── (stdlib: net/http, os, time)
```

Note: `cmd/agentunnel` has **no dependency** on `webui/` or `relay/`.

## `cmd/agentunnel` Simplified Startup

```
main()
  └─> runWithArgs(args, stderr)
        ├─ 1. parseRunArgs(args)           → label, relayURL, token, launcher, args
        ├─ 2. validate relayURL + token    → fail fast if missing
        ├─ 3. launcher.Resolve(name, args) → launcher.Command
        ├─ 4. session.PrepareLocalTerminal()
        ├─ 5. connector.New(relayURL, token, sessionInfo)
        ├─ 6. session.StartCommandWithInitialSinks(ctx, path, args,
        │       {"relay": connector, "stdout": localStdout})
        ├─ 7. connector.Start(ctx, hub)    → connects + forwards
        ├─ 8. localTerminal.Start(ctx, hub)
        └─ 9. waitForProcessOrShutdown()
```

Removed from old flow:
- Localhost HTTP server (`server.StartLocal()`)
- Conditional relay branching (`if cfg != nil`)
- `relayclient.LoadConfig()` indirection

## `cmd/relay` Port Configuration

Precedence (highest to lowest):
1. `--port` CLI flag
2. `AGENTUNNEL_RELAY_ADDR` environment variable
3. Default `:8586`

## Web Build

Vite config updated:
- Single entrypoint: `index.html` (was `relay.html`)
- Remove multi-page config that referenced old `index.html`
- `npm run build` produces one set of assets embedded by `webui/`

## Protocol Documentation

New file: `docs/protocol.md`

Sections:
1. **Overview** — relay architecture; two roles (agent, browser)
2. **Authentication** — Basic Auth for browser HTTP/WS; Bearer token for agent WS
3. **REST API** — `GET /api/sessions` response shape
4. **Browser WebSocket** — `GET /api/sessions/:id/ws`; JSON frames (`output`, `input`, `resize`)
5. **Agent WebSocket** — `GET /agent/ws`; JSON frames (`register`, `output`, `input`, `resize`)
6. **Frame Reference** — complete JSON schema for every frame type with field descriptions and examples
7. **Connection Lifecycle** — connect → register → stream → heartbeat → disconnect
8. **Mobile Implementation Notes** — recommended native terminal libraries (SwiftTerm for iOS, TerminalView for Android), reconnection strategy, read-only/input toggle pattern

## Makefile Changes

Remove:
- `agent` target
- `client` target
- `bin/agent` and `bin/client` from `build` target

Keep:
- `agentunnel` target (remove `web-build` dependency since agentunnel doesn't serve web)
- `relay` target (keeps `web-build` dependency)
- `build` target (only `bin/agentunnel` and `bin/relay`)
- `web-build`, `web-install`, `web`, `test`, `vet`, `clean`

## Doc Updates

- `README.md` — remove all legacy references, update quick-start to reflect mandatory relay, remove "Legacy Mode" section
- `docs/architecture.md` — rewrite to reflect new package layout, remove legacy section, update dependency graph and diagrams
- `CLAUDE.md` — update package descriptions, remove legacy references, update verification commands
