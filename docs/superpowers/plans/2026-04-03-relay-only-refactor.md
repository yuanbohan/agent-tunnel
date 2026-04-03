# Relay-Only Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove legacy agent/client code, drop the localhost web server, promote all Go packages from `internal/` to repo root, make relay mandatory, and write protocol docs for mobile clients.

**Architecture:** Two binaries (`cmd/agentunnel` and `cmd/relay`) with shared packages at repo root (`protocol/`, `session/`, `connector/`, `launcher/`, `relay/`, `webui/`). The `agentunnel` binary owns the PTY and connects to the relay; the `relay` binary serves the web UI and brokers browser access. No `internal/` directory.

**Tech Stack:** Go 1.25, TypeScript, Vite, xterm.js, gorilla/websocket

---

### Task 1: Delete legacy code

Remove `cmd/agent`, `cmd/client`, `internal/agent`, `internal/client`, and `internal/server`. These are the legacy shell-over-WebSocket pair and the localhost-only web server that `agentunnel` no longer needs.

**Files:**
- Delete: `cmd/agent/main.go`
- Delete: `cmd/client/main.go`
- Delete: `internal/agent/handler.go`
- Delete: `internal/agent/pty.go`
- Delete: `internal/client/session.go`
- Delete: `internal/client/terminal.go`
- Delete: `internal/server/server.go`
- Delete: `internal/server/server_test.go`

- [ ] **Step 1: Delete legacy directories**

```bash
rm -rf cmd/agent cmd/client internal/agent internal/client internal/server
```

- [ ] **Step 2: Verify no remaining imports of deleted packages**

```bash
grep -r '"yuanbohan/tunnel/internal/agent"' --include='*.go' .
grep -r '"yuanbohan/tunnel/internal/client"' --include='*.go' .
grep -r '"yuanbohan/tunnel/internal/server"' --include='*.go' .
```

Expected: no matches except `cmd/agentunnel/main.go` for `internal/server` (will be fixed in Task 4).

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "chore: remove legacy agent/client and localhost server code"
```

---

### Task 2: Promote Go packages from `internal/` to repo root

Move `internal/session`, `internal/protocol`, `internal/launcher`, `internal/webui` to the repo root. This is a pure move — no code changes yet, only import path updates.

**Files:**
- Move: `internal/session/` → `session/`
- Move: `internal/protocol/` → `protocol/`
- Move: `internal/launcher/` → `launcher/`
- Move: `internal/webui/` → `webui/`

- [ ] **Step 1: Move directories**

```bash
mv internal/session session
mv internal/protocol protocol
mv internal/launcher launcher
mv internal/webui webui
```

- [ ] **Step 2: Update all import paths**

Find and replace across the entire repo:

| Old | New |
|-----|-----|
| `"yuanbohan/tunnel/internal/session"` | `"yuanbohan/tunnel/session"` |
| `"yuanbohan/tunnel/internal/protocol"` | `"yuanbohan/tunnel/protocol"` |
| `"yuanbohan/tunnel/internal/launcher"` | `"yuanbohan/tunnel/launcher"` |
| `"yuanbohan/tunnel/internal/webui"` | `"yuanbohan/tunnel/webui"` |

Files that need updating (search all `.go` files):
- `cmd/agentunnel/main.go`
- `cmd/agentunnel/main_test.go`
- `cmd/relay/main.go`
- `cmd/relay/main_test.go`
- `internal/relayclient/connector.go`
- `internal/relayclient/connector_test.go`
- `internal/relayserver/server.go`
- `internal/relayserver/server_test.go`
- `internal/relayserver/registry.go`
- `internal/relayserver/registry_test.go`

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: success (the `cmd/agentunnel` build will fail due to the `internal/server` import removed in Task 1 — that's expected and fixed in Task 4).

- [ ] **Step 4: Run tests on moved packages**

```bash
go test ./session/... ./protocol/... ./launcher/...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: promote session, protocol, launcher, webui from internal/ to repo root"
```

---

### Task 3: Merge `relayapi` into `protocol` and promote

Move `SessionInfo`, `AgentFrame`, and `RegisterFrame` from `internal/relayapi/` into `protocol/`. Then delete `internal/relayapi/`.

**Files:**
- Modify: `protocol/message.go` — add SessionInfo, AgentFrame, RegisterFrame
- Create: `protocol/relay_types_test.go` — move tests from `internal/relayapi/types_test.go`
- Delete: `internal/relayapi/types.go`
- Delete: `internal/relayapi/types_test.go`

- [ ] **Step 1: Add relay types to protocol/message.go**

Append to `protocol/message.go` after the existing code:

```go
import "time"  // add to existing import block

// SessionInfo describes a live agent session registered with the relay.
type SessionInfo struct {
	SessionID      string     `json:"session_id"`
	Launcher       string     `json:"launcher"`
	Label          string     `json:"label,omitempty"`
	CWD            string     `json:"cwd"`
	CommandPreview string     `json:"command_preview"`
	StartedAt      time.Time  `json:"started_at"`
	LastPreview    string     `json:"last_preview,omitempty"`
	LastActiveAt   *time.Time `json:"last_active_at,omitempty"`
}

// AgentFrame is the JSON envelope for agent-to-relay WebSocket messages.
type AgentFrame struct {
	Type    string       `json:"type"`
	Session *SessionInfo `json:"session,omitempty"`
	Data    string       `json:"data,omitempty"`
	Cols    int          `json:"cols,omitempty"`
	Rows    int          `json:"rows,omitempty"`
}

// RegisterFrame builds the initial registration message an agent sends to the relay.
func RegisterFrame(info SessionInfo) AgentFrame {
	return AgentFrame{
		Type:    "register",
		Session: &info,
	}
}
```

- [ ] **Step 2: Move tests**

Copy `internal/relayapi/types_test.go` to `protocol/relay_types_test.go`. Change `package relayapi` to `package protocol`. Update any type references from `relayapi.SessionInfo` to `SessionInfo` etc. (they're already unqualified in package-internal tests).

- [ ] **Step 3: Run tests**

```bash
go test ./protocol/...
```

Expected: all pass.

- [ ] **Step 4: Update all imports of relayapi**

Replace `"yuanbohan/tunnel/internal/relayapi"` with `"yuanbohan/tunnel/protocol"` in:
- `cmd/agentunnel/main.go` — change `relayapi.SessionInfo` → `protocol.SessionInfo`, `relayapi.RegisterFrame` is not used here (connector builds it)
- `internal/relayclient/connector.go` — change `relayapi.SessionInfo` → `protocol.SessionInfo`, `relayapi.RegisterFrame` → `protocol.RegisterFrame`
- `internal/relayserver/server.go` — change `relayapi.AgentFrame` → `protocol.AgentFrame`
- `internal/relayserver/registry.go` — change `relayapi.SessionInfo` → `protocol.SessionInfo`
- `internal/relayserver/registry_test.go` — same
- `internal/relayserver/server_test.go` — same

- [ ] **Step 5: Delete relayapi**

```bash
rm -rf internal/relayapi
```

- [ ] **Step 6: Run tests**

```bash
go test ./protocol/... ./internal/relayclient/... ./internal/relayserver/...
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: merge relayapi types into protocol package"
```

---

### Task 4: Rename and promote relayclient → connector

Move `internal/relayclient/` to `connector/`. The `Config` struct and `LoadConfig` function will be removed in Task 6 when we simplify the agentunnel startup. For now, just move and rename.

**Files:**
- Move: `internal/relayclient/` → `connector/`

- [ ] **Step 1: Move directory**

```bash
mv internal/relayclient connector
```

- [ ] **Step 2: Update package declaration**

In all `.go` files under `connector/`, change `package relayclient` to `package connector`.

Files: `connector/config.go`, `connector/config_test.go`, `connector/connector.go`, `connector/connector_test.go`

- [ ] **Step 3: Update import paths**

Replace `"yuanbohan/tunnel/internal/relayclient"` with `"yuanbohan/tunnel/connector"` in:
- `cmd/agentunnel/main.go`
- `cmd/agentunnel/main_test.go`

Also update any references to `relayclient.Config` → `connector.Config`, `relayclient.LoadConfig` → `connector.LoadConfig`, `relayclient.New` → `connector.New`.

- [ ] **Step 4: Run tests**

```bash
go test ./connector/...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: rename relayclient to connector, promote to repo root"
```

---

### Task 5: Rename and promote relayserver → relay

Move `internal/relayserver/` to `relay/`.

**Files:**
- Move: `internal/relayserver/` → `relay/`

- [ ] **Step 1: Move directory**

```bash
mv internal/relayserver relay
```

- [ ] **Step 2: Update package declaration**

In all `.go` files under `relay/`, change `package relayserver` to `package relay`.

Files: `relay/auth.go`, `relay/preview.go`, `relay/preview_test.go`, `relay/registry.go`, `relay/registry_test.go`, `relay/server.go`, `relay/server_test.go`

- [ ] **Step 3: Update import paths**

Replace `"yuanbohan/tunnel/internal/relayserver"` with `"yuanbohan/tunnel/relay"` in:
- `cmd/relay/main.go`
- `cmd/relay/main_test.go`

Also update `relayserver.NewHandler` → `relay.NewHandler`, `relayserver.HandlerConfig` → `relay.HandlerConfig`, `relayserver.NewRegistry` → `relay.NewRegistry`, etc.

- [ ] **Step 4: Delete the now-empty internal directory**

```bash
rmdir internal
```

If not empty, check what's left and investigate.

- [ ] **Step 5: Run all tests**

```bash
go test ./...
```

Expected: `cmd/agentunnel` may still fail (it references `server.StartLocal` which was deleted in Task 1). All other packages should pass.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: rename relayserver to relay, promote to repo root, remove internal/"
```

---

### Task 6: Simplify cmd/agentunnel — mandatory relay, no localhost server

Rewrite `cmd/agentunnel/main.go` to require relay URL and token, remove localhost server, and simplify the startup flow. Also rewrite `args.go` to validate relay config.

**Files:**
- Modify: `cmd/agentunnel/args.go`
- Modify: `cmd/agentunnel/main.go`
- Modify: `cmd/agentunnel/args_test.go`
- Modify: `cmd/agentunnel/main_test.go`
- Delete: `connector/config.go`
- Delete: `connector/config_test.go`

- [ ] **Step 1: Rewrite args.go**

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

type runArgs struct {
	Label        string
	RelayURL     string
	RelayToken   string
	Launcher     string
	LauncherArgs []string
}

func parseRunArgs(argv []string) (runArgs, error) {
	fs := flag.NewFlagSet("agentunnel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var cfg runArgs
	fs.StringVar(&cfg.Label, "label", "", "optional session label for relay dashboard")
	fs.StringVar(&cfg.RelayURL, "relay-url", "", "relay websocket URL (or AGENTUNNEL_RELAY_URL)")

	if err := fs.Parse(argv[1:]); err != nil {
		return runArgs{}, err
	}

	// relay-url: flag > env > error
	if cfg.RelayURL == "" {
		cfg.RelayURL = os.Getenv("AGENTUNNEL_RELAY_URL")
	}
	if cfg.RelayURL == "" {
		return runArgs{}, fmt.Errorf("relay URL is required: use --relay-url or set AGENTUNNEL_RELAY_URL")
	}

	// token: env only
	cfg.RelayToken = os.Getenv("AGENTUNNEL_RELAY_TOKEN")
	if cfg.RelayToken == "" {
		return runArgs{}, fmt.Errorf("AGENTUNNEL_RELAY_TOKEN is required")
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return runArgs{}, fmt.Errorf("usage: agentunnel [--label label] [--relay-url url] <claude|codex|gemini> [args...]")
	}

	cfg.Launcher = rest[0]
	cfg.LauncherArgs = append([]string(nil), rest[1:]...)
	return cfg, nil
}
```

- [ ] **Step 2: Rewrite main.go**

```go
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"yuanbohan/tunnel/connector"
	"yuanbohan/tunnel/launcher"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/session"
)

var (
	resolveLauncher      = launcher.Resolve
	prepareLocalTerminal = session.PrepareLocalTerminal
	startSession         = session.StartCommandWithInitialSinks
	newConnector         = connector.New
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	return runWithArgs(os.Args, os.Stderr)
}

func runWithArgs(args []string, stderr io.Writer) error {
	parsed, err := parseRunArgs(args)
	if err != nil {
		return err
	}

	command, err := resolveLauncher(parsed.Launcher, parsed.LauncherArgs)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	local, err := prepareLocalTerminal()
	if err != nil {
		return err
	}
	defer local.Restore()

	sinkID, sink := local.SinkRegistration()

	info := protocol.SessionInfo{
		SessionID:      fmt.Sprintf("%d", time.Now().UnixNano()),
		Launcher:       command.Name,
		Label:          parsed.Label,
		CWD:            cwd,
		CommandPreview: strings.TrimSpace(strings.Join(append([]string{filepath.Base(command.Path)}, command.Args...), " ")),
		StartedAt:      time.Now().UTC(),
	}

	relay := newConnector(parsed.RelayURL, parsed.RelayToken, info)

	initialSinks := map[string]session.OutputSink{
		sinkID:  sink,
		"relay": relay,
	}

	running, err := startSession(ctx, command.Path, command.Args, initialSinks)
	if err != nil {
		return err
	}
	defer running.Close()

	relay.BindHub(running.Hub)
	go relay.Run(ctx)

	fmt.Fprintf(
		stderr,
		"▶ agentunnel — %s\n  relay: %s\n  local terminal is interactive\n\n",
		command.Name,
		parsed.RelayURL,
	)

	done := local.Start(ctx, running.Hub)

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- running.Wait()
	}()

	return waitForProcessOrShutdown(ctx, done, waitErr)
}

func waitForProcessOrShutdown(ctx context.Context, localDone <-chan struct{}, waitErr <-chan error) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-localDone:
			localDone = nil
		case err := <-waitErr:
			return err
		}
	}
}
```

- [ ] **Step 3: Update connector.New signature**

Modify `connector/connector.go` to accept URL, token, and info directly instead of a `Config` struct:

```go
func New(url, token string, info protocol.SessionInfo) *Connector {
	return &Connector{
		url:      url,
		token:    token,
		info:     info,
		outbound: make(chan []byte, 128),
		dialer:   websocket.DefaultDialer,
	}
}
```

Update the `Connector` struct fields:

```go
type Connector struct {
	url   string
	token string
	info  protocol.SessionInfo
	hub   *session.Hub

	outbound chan []byte
	dialer   *websocket.Dialer
}
```

Update `runOnce` to use `c.url` and `c.token` instead of `c.cfg.URL` and `c.cfg.Token`:

```go
func (c *Connector) runOnce(ctx context.Context) error {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.token)

	conn, _, err := c.dialer.DialContext(ctx, c.url+"/agent/ws", headers)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := conn.WriteJSON(protocol.RegisterFrame(c.info)); err != nil {
		return err
	}
	// ... rest unchanged
```

- [ ] **Step 4: Delete config.go and config_test.go**

```bash
rm connector/config.go connector/config_test.go
```

- [ ] **Step 5: Update connector tests**

In `connector/connector_test.go`, update `New(...)` calls to use the new signature `New(url, token, info)` instead of `New(Config{URL: url, Token: token}, info)`. Also update import from `"yuanbohan/tunnel/internal/relayapi"` to `"yuanbohan/tunnel/protocol"` and change `relayapi.SessionInfo` to `protocol.SessionInfo`.

- [ ] **Step 6: Update args_test.go**

Rewrite tests to cover mandatory relay URL and token:

```go
package main

import (
	"os"
	"testing"
)

func TestParseRunArgs_valid(t *testing.T) {
	os.Setenv("AGENTUNNEL_RELAY_URL", "wss://relay.example")
	os.Setenv("AGENTUNNEL_RELAY_TOKEN", "tok")
	defer os.Unsetenv("AGENTUNNEL_RELAY_URL")
	defer os.Unsetenv("AGENTUNNEL_RELAY_TOKEN")

	args, err := parseRunArgs([]string{"agentunnel", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if args.RelayURL != "wss://relay.example" {
		t.Errorf("RelayURL = %q", args.RelayURL)
	}
	if args.Launcher != "claude" {
		t.Errorf("Launcher = %q", args.Launcher)
	}
}

func TestParseRunArgs_flagOverridesEnv(t *testing.T) {
	os.Setenv("AGENTUNNEL_RELAY_URL", "wss://env.example")
	os.Setenv("AGENTUNNEL_RELAY_TOKEN", "tok")
	defer os.Unsetenv("AGENTUNNEL_RELAY_URL")
	defer os.Unsetenv("AGENTUNNEL_RELAY_TOKEN")

	args, err := parseRunArgs([]string{"agentunnel", "--relay-url", "wss://flag.example", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if args.RelayURL != "wss://flag.example" {
		t.Errorf("RelayURL = %q, want wss://flag.example", args.RelayURL)
	}
}

func TestParseRunArgs_missingRelayURL(t *testing.T) {
	os.Unsetenv("AGENTUNNEL_RELAY_URL")
	os.Unsetenv("AGENTUNNEL_RELAY_TOKEN")

	_, err := parseRunArgs([]string{"agentunnel", "claude"})
	if err == nil {
		t.Fatal("expected error for missing relay URL")
	}
}

func TestParseRunArgs_missingToken(t *testing.T) {
	os.Setenv("AGENTUNNEL_RELAY_URL", "wss://relay.example")
	os.Unsetenv("AGENTUNNEL_RELAY_TOKEN")
	defer os.Unsetenv("AGENTUNNEL_RELAY_URL")

	_, err := parseRunArgs([]string{"agentunnel", "claude"})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestParseRunArgs_missingLauncher(t *testing.T) {
	os.Setenv("AGENTUNNEL_RELAY_URL", "wss://relay.example")
	os.Setenv("AGENTUNNEL_RELAY_TOKEN", "tok")
	defer os.Unsetenv("AGENTUNNEL_RELAY_URL")
	defer os.Unsetenv("AGENTUNNEL_RELAY_TOKEN")

	_, err := parseRunArgs([]string{"agentunnel"})
	if err == nil {
		t.Fatal("expected error for missing launcher")
	}
}

func TestParseRunArgs_withLabel(t *testing.T) {
	os.Setenv("AGENTUNNEL_RELAY_URL", "wss://relay.example")
	os.Setenv("AGENTUNNEL_RELAY_TOKEN", "tok")
	defer os.Unsetenv("AGENTUNNEL_RELAY_URL")
	defer os.Unsetenv("AGENTUNNEL_RELAY_TOKEN")

	args, err := parseRunArgs([]string{"agentunnel", "--label", "my-fix", "codex", "--extra"})
	if err != nil {
		t.Fatal(err)
	}
	if args.Label != "my-fix" {
		t.Errorf("Label = %q", args.Label)
	}
	if args.Launcher != "codex" {
		t.Errorf("Launcher = %q", args.Launcher)
	}
	if len(args.LauncherArgs) != 1 || args.LauncherArgs[0] != "--extra" {
		t.Errorf("LauncherArgs = %v", args.LauncherArgs)
	}
}
```

- [ ] **Step 7: Update main_test.go**

Rewrite `cmd/agentunnel/main_test.go` to remove all references to `server.StartLocal`, `relayclient.LoadConfig`, `relayclient.Config`, and the conditional relay logic. The test stubs should match the new simplified flow: `resolveLauncher`, `prepareLocalTerminal`, `startSession`, `newConnector`. Remove the `startServer` and `loadRelayConfig` test stubs entirely.

Key changes:
- Remove `startServer` stub variable and all references
- Remove `loadRelayConfig` stub variable and all references
- The `newConnector` stub should match new signature: `func(url, token string, info protocol.SessionInfo) *Connector`
- Tests should set `AGENTUNNEL_RELAY_URL` and `AGENTUNNEL_RELAY_TOKEN` env vars
- Remove any test that specifically tested "relay disabled" or "localhost server" behavior

- [ ] **Step 8: Run all tests**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "feat: make relay mandatory, remove localhost server from agentunnel"
```

---

### Task 7: Add --port flag to cmd/relay

Add a `--port` CLI flag to the relay server with precedence: flag > env > default.

**Files:**
- Modify: `cmd/relay/main.go`
- Modify: `cmd/relay/main_test.go`

- [ ] **Step 1: Write failing test**

Add to `cmd/relay/main_test.go`:

```go
func TestLoadMainConfig_portFlag(t *testing.T) {
	cfg, err := loadMainConfig(func(key string) string {
		switch key {
		case "AGENTUNNEL_BASIC_USER":
			return "u"
		case "AGENTUNNEL_BASIC_PASSWORD":
			return "p"
		case "AGENTUNNEL_AGENT_TOKEN":
			return "t"
		}
		return ""
	}, "9999")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":9999" {
		t.Errorf("ListenAddr = %q, want :9999", cfg.ListenAddr)
	}
}

func TestLoadMainConfig_portFlagOverridesEnv(t *testing.T) {
	cfg, err := loadMainConfig(func(key string) string {
		switch key {
		case "AGENTUNNEL_RELAY_ADDR":
			return ":7777"
		case "AGENTUNNEL_BASIC_USER":
			return "u"
		case "AGENTUNNEL_BASIC_PASSWORD":
			return "p"
		case "AGENTUNNEL_AGENT_TOKEN":
			return "t"
		}
		return ""
	}, "9999")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":9999" {
		t.Errorf("ListenAddr = %q, want :9999 (flag should override env)", cfg.ListenAddr)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./cmd/relay/ -run TestLoadMainConfig_portFlag -v
```

Expected: FAIL — `loadMainConfig` doesn't accept port parameter yet.

- [ ] **Step 3: Update loadMainConfig to accept port parameter**

```go
func loadMainConfig(getenv func(string) string, portFlag string) (mainConfig, error) {
	cfg := mainConfig{
		ListenAddr:      getenv("AGENTUNNEL_RELAY_ADDR"),
		BrowserUser:     getenv("AGENTUNNEL_BASIC_USER"),
		BrowserPassword: getenv("AGENTUNNEL_BASIC_PASSWORD"),
		AgentToken:      getenv("AGENTUNNEL_AGENT_TOKEN"),
	}

	// --port flag takes highest precedence
	if portFlag != "" {
		cfg.ListenAddr = ":" + portFlag
	}

	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8586"
	}
	if cfg.BrowserUser == "" || cfg.BrowserPassword == "" || cfg.AgentToken == "" {
		return mainConfig{}, fmt.Errorf("AGENTUNNEL_BASIC_USER, AGENTUNNEL_BASIC_PASSWORD, and AGENTUNNEL_AGENT_TOKEN are required")
	}
	return cfg, nil
}
```

- [ ] **Step 4: Update main() to parse --port flag**

```go
func main() {
	port := flag.String("port", "", "listen port (overrides AGENTUNNEL_RELAY_ADDR)")
	flag.Parse()

	cfg, err := loadMainConfig(os.Getenv, *port)
	if err != nil {
		log.Fatal(err)
	}

	handler := relay.NewHandler(relay.HandlerConfig{
		Registry:        relay.NewRegistry(),
		BrowserUser:     cfg.BrowserUser,
		BrowserPassword: cfg.BrowserPassword,
		AgentToken:      cfg.AgentToken,
	})

	fmt.Fprintf(os.Stderr, "▶ relay listening on %s\n", cfg.ListenAddr)
	log.Fatal(newHTTPServer(cfg, handler).ListenAndServe())
}
```

- [ ] **Step 5: Fix existing tests to pass port parameter**

Update existing `loadMainConfig` test calls to pass `""` as the port parameter:

```go
// Old:
cfg, err := loadMainConfig(func(key string) string { ... })
// New:
cfg, err := loadMainConfig(func(key string) string { ... }, "")
```

- [ ] **Step 6: Run tests**

```bash
go test ./cmd/relay/ -v
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: add --port flag to relay server"
```

---

### Task 8: Update relay server to serve index.html instead of relay.html

After the web rename (Task 9), the built assets will have `index.html` instead of `relay.html`. Update the relay server to look for `index.html`.

**Files:**
- Modify: `relay/server.go`

- [ ] **Step 1: Update serveRelayShellAsset**

In `relay/server.go`, change the `serveRelayShellAsset` function:

```go
func serveRelayShellAsset(w http.ResponseWriter, r *http.Request, files fs.FS) {
	if _, err := fs.Stat(files, "index.html"); err == nil {
		http.ServeFileFS(w, r, files, "index.html")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(relayFallbackHTML))
}
```

- [ ] **Step 2: Update tests that reference relay.html**

Search `relay/server_test.go` for any test that creates a mock filesystem with `relay.html` and change it to `index.html`.

- [ ] **Step 3: Run tests**

```bash
go test ./relay/...
```

Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "fix: serve index.html instead of relay.html in relay server"
```

---

### Task 9: Rename web modules and consolidate to single entrypoint

Remove localhost-only web files, rename relay-prefixed modules, and update Vite config.

**Files:**
- Delete: `web/index.html` (already deleted if Task 1 ran, but the *original* localhost one)
- Delete: `web/src/main.ts`
- Delete: `web/src/session_url.ts`
- Delete: `web/src/session_url.test.ts` (if exists)
- Delete: `web/src/input_filter.test.ts`
- Rename: `web/relay.html` → `web/index.html`
- Rename: `web/src/relay_app.ts` → `web/src/app.ts`
- Rename: `web/src/relay_routes.ts` → `web/src/routes.ts`
- Rename: `web/src/relay_api.ts` → `web/src/api.ts`
- Rename: `web/src/relay_dashboard.ts` → `web/src/dashboard.ts`
- Rename: `web/src/relay_session_page.ts` → `web/src/session_page.ts`
- Rename: `web/src/relay_types.ts` → `web/src/types.ts`
- Rename: `web/src/relay.css` → `web/src/style.css`
- Modify: `web/vite.config.ts`

- [ ] **Step 1: Delete localhost-only files**

```bash
rm -f web/index.html web/src/main.ts web/src/session_url.ts web/src/session_url.test.ts web/src/input_filter.test.ts
```

- [ ] **Step 2: Rename relay.html to index.html**

```bash
mv web/relay.html web/index.html
```

- [ ] **Step 3: Update the new index.html**

Change the title and script src:

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>agent-tunnel relay</title>
</head>
<body>
  <div id="relay-root"></div>
  <script type="module" src="/src/app.ts"></script>
</body>
</html>
```

- [ ] **Step 4: Rename all relay_ prefixed files**

```bash
mv web/src/relay_app.ts web/src/app.ts
mv web/src/relay_routes.ts web/src/routes.ts
mv web/src/relay_api.ts web/src/api.ts
mv web/src/relay_dashboard.ts web/src/dashboard.ts
mv web/src/relay_session_page.ts web/src/session_page.ts
mv web/src/relay_types.ts web/src/types.ts
mv web/src/relay.css web/src/style.css
```

- [ ] **Step 5: Update imports in app.ts**

```typescript
import './style.css'
import { ConnectionManager, type ConnectionStatus } from './connection'
import { decodeOutput, encodeInput, type Message } from './protocol'
import { fetchSessions, relaySessionWebSocketURL } from './api'
import { renderSessionCard } from './dashboard'
import { parseRelayRoute } from './routes'
import { nextInputState, stateChipClass, stateChipLabel } from './session_page'
import { createTerminal } from './terminal'
```

- [ ] **Step 6: Update imports in api.ts**

```typescript
import type { RelaySession } from './types'
```

- [ ] **Step 7: Update imports in dashboard.ts**

```typescript
import type { RelaySession } from './types'
```

- [ ] **Step 8: Integrate input_filter into app.ts**

Add the auto-response filter to the relay session input path. In the `renderSession` function in `app.ts`, add the import and filter:

```typescript
import { isTerminalAutoResponse } from './input_filter'
```

And update the onData handler:

```typescript
  terminal.onData((value) => {
    if (inputEnabled && !isTerminalAutoResponse(value)) {
      conn.send(encodeInput(value))
    }
  })
```

- [ ] **Step 9: Simplify vite.config.ts**

```typescript
import { defineConfig } from 'vite'
import { fileURLToPath } from 'node:url'

const outDir = fileURLToPath(new URL('../webui/dist', import.meta.url))
const rootDir = fileURLToPath(new URL('.', import.meta.url))

export default defineConfig({
  server: {
    port: 3000,
  },
  build: {
    outDir,
    emptyOutDir: true,
  },
  root: rootDir,
})
```

Note: `outDir` changed from `../internal/webui/dist` to `../webui/dist` since webui is now at repo root.

- [ ] **Step 10: Update any remaining test files**

Check for test files that import renamed modules:

```bash
grep -r "relay_" web/src/ --include='*.ts'
```

Fix any remaining references.

- [ ] **Step 11: Build and test**

```bash
cd web && npm run build && npm test
```

Expected: build succeeds, tests pass.

- [ ] **Step 12: Commit**

```bash
git add -A
git commit -m "refactor: consolidate web to single relay entrypoint, drop relay_ prefixes"
```

---

### Task 10: Rebuild embedded web assets

After the web restructure, rebuild the dist/ directory that gets embedded.

**Files:**
- Modify: `webui/dist/` (rebuilt output)

- [ ] **Step 1: Clean and rebuild**

```bash
cd web && npm run build
```

- [ ] **Step 2: Verify the built output**

```bash
ls webui/dist/
```

Expected: `index.html` and `assets/` directory. No `relay.html`.

- [ ] **Step 3: Run full test suite**

```bash
go test ./...
cd web && npm test
```

Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "build: rebuild embedded web assets for single-entrypoint layout"
```

---

### Task 11: Update Makefile

Remove legacy targets, update build to only produce `agentunnel` and `relay`.

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Rewrite Makefile**

```makefile
.PHONY: agentunnel relay build clean vet test web web-install web-build

agentunnel:
	@test -n "$(LAUNCHER)" || (echo "usage: make agentunnel LAUNCHER=claude" && exit 1)
	go run ./cmd/agentunnel $(LAUNCHER)

relay: web-build
	go run ./cmd/relay

web-install:
	cd web && npm install

web-build:
	cd web && npm run build

web:
	cd web && npm run dev

build: web-build
	go build -o bin/agentunnel ./cmd/agentunnel
	go build -o bin/relay ./cmd/relay

clean:
	rm -rf bin/

vet:
	go vet ./...

test:
	go test ./...
	cd web && npm test
```

- [ ] **Step 2: Verify**

```bash
make vet
make test
```

Expected: both pass.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "build: remove legacy targets from Makefile"
```

---

### Task 12: Update README.md

Rewrite README to reflect relay-only architecture.

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Rewrite README.md**

```markdown
# agent-tunnel

Launch a terminal agent locally and access it remotely through a relay server.

`agentunnel` starts a real CLI such as `claude`, `codex`, or `gemini`, keeps the launching terminal interactive, and registers the session with a relay server. The relay serves a browser-based terminal UI for remote access.

## Requirements

- Go 1.25+
- Node.js and npm (for building the relay web UI)
- A supported launcher on `PATH`: `claude`, `codex`, or `gemini`

## Quick Start

### 1. Start the relay server

```bash
export AGENTUNNEL_BASIC_USER=demo
export AGENTUNNEL_BASIC_PASSWORD=secret
export AGENTUNNEL_AGENT_TOKEN=agent-token
make relay
```

The relay listens on `:8586` by default. Override with `--port` or `AGENTUNNEL_RELAY_ADDR`.

### 2. Start an agent session

```bash
export AGENTUNNEL_RELAY_URL=ws://localhost:8586
export AGENTUNNEL_RELAY_TOKEN=agent-token
make agentunnel LAUNCHER=claude
```

Or directly:

```bash
go run ./cmd/agentunnel --relay-url ws://localhost:8586 claude
```

The local terminal stays interactive. The session also appears on the relay dashboard.

### 3. Open the relay dashboard

Navigate to `http://localhost:8586/`, authenticate with Basic Auth, and choose a live session.

## Deploying to a VPS

Run the relay on a public host:

```bash
export AGENTUNNEL_BASIC_USER=demo
export AGENTUNNEL_BASIC_PASSWORD=secret
export AGENTUNNEL_AGENT_TOKEN=agent-token
./bin/relay --port 443
```

Connect from your local machine:

```bash
export AGENTUNNEL_RELAY_URL=wss://relay.example.com
export AGENTUNNEL_RELAY_TOKEN=agent-token
go run ./cmd/agentunnel --label api-fix codex
```

## Supported Launchers

- `claude`
- `codex`
- `gemini`

`agentunnel` resolves these from `PATH` and runs the real CLI unchanged.

## Development

```bash
make web-install   # first time only
make build         # builds bin/agentunnel and bin/relay
make test          # runs Go and web tests
make web           # run web dev server
make web-build     # rebuild embedded web assets
```

If you change files under `web/`, rebuild embedded assets before committing:

```bash
make web-build
```

## Protocol

See [docs/protocol.md](docs/protocol.md) for the full WebSocket protocol specification, including frame formats and authentication — useful for building native mobile clients.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: rewrite README for relay-only architecture"
```

---

### Task 13: Update docs/architecture.md

Rewrite the architecture doc to reflect the new package layout and remove all legacy references.

**Files:**
- Modify: `docs/architecture.md`

- [ ] **Step 1: Rewrite docs/architecture.md**

Key changes:
- Update the high-level overview diagram to remove the localhost HTTP server box
- Update the package dependency graph to show root-level packages (no `internal/`)
- Remove the "Legacy Components" section entirely
- Update all import paths in code examples
- Update the "Web Client Modules" section to show the renamed files (no `relay_` prefix)
- Update the Vite config reference (single entrypoint)
- Update the startup sequence to show mandatory relay (no conditional)
- Remove `server.StartLocal` from the startup flow
- Add note about `--port` flag for relay

- [ ] **Step 2: Commit**

```bash
git add docs/architecture.md
git commit -m "docs: rewrite architecture doc for relay-only layout"
```

---

### Task 14: Update CLAUDE.md

Update the project guide to reflect the new structure.

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Rewrite CLAUDE.md**

Key changes:
- Remove all references to `cmd/agent`, `cmd/client`, `internal/agent`, `internal/client`, `internal/server`
- Remove "Legacy Mode" mentions
- Update package paths: `session/`, `protocol/`, `connector/`, `launcher/`, `relay/`, `webui/` (no `internal/` prefix)
- Remove `internal/relayapi/` and `internal/relayclient/` references
- Update web module descriptions (no `relay_` prefix, single entrypoint)
- Remove "do not break or remove legacy" caveat
- Update "Current Product Boundaries" to remove localhost server mentions

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md for relay-only architecture"
```

---

### Task 15: Write protocol documentation

Create `docs/protocol.md` with the full WebSocket protocol spec for mobile client developers.

**Files:**
- Create: `docs/protocol.md`

- [ ] **Step 1: Write docs/protocol.md**

The document must cover:

**1. Overview** — The relay is a WebSocket broker. Two roles: agents register sessions and stream PTY output; browsers list sessions and attach to view/interact. All communication is JSON over WebSocket text frames.

**2. Authentication**

| Role | Endpoint | Method |
|------|----------|--------|
| Browser (HTTP) | `GET /api/sessions` | Basic Auth |
| Browser (WS) | `GET /api/sessions/:id/ws` | Basic Auth (on upgrade request) |
| Agent (WS) | `GET /agent/ws` | Bearer token in `Authorization` header |

**3. REST API**

`GET /api/sessions` — returns `SessionInfo[]`:

```json
[
  {
    "session_id": "1234567890",
    "launcher": "claude",
    "label": "api-fix",
    "cwd": "/home/user/project",
    "command_preview": "claude --resume",
    "started_at": "2026-04-03T10:00:00Z",
    "last_preview": "Running tests...",
    "last_active_at": "2026-04-03T10:05:00Z"
  }
]
```

**4. Browser WebSocket — `GET /api/sessions/:id/ws`**

After upgrade, the browser receives `output` frames and may send `input` and `resize` frames.

Frames from server to browser:

```json
{"type": "output", "data": "<base64-encoded-bytes>"}
```

Frames from browser to server:

```json
{"type": "input", "data": "<base64-encoded-bytes>"}
{"type": "resize", "cols": 120, "rows": 40}
```

**5. Agent WebSocket — `GET /agent/ws`**

After upgrade, the agent must send a `register` frame first, then streams `output`. It receives `input` and `resize` from attached browsers.

Register frame (agent → relay):

```json
{
  "type": "register",
  "session": {
    "session_id": "1234567890",
    "launcher": "claude",
    "label": "api-fix",
    "cwd": "/home/user/project",
    "command_preview": "claude --resume",
    "started_at": "2026-04-03T10:00:00Z"
  }
}
```

Output frame (agent → relay):

```json
{"type": "output", "data": "<base64-encoded-bytes>"}
```

Input frame (relay → agent):

```json
{"type": "input", "data": "<base64-encoded-bytes>"}
```

Resize frame (relay → agent):

```json
{"type": "resize", "cols": 120, "rows": 40}
```

**6. Frame Reference** — Full JSON schema for each frame type with field descriptions.

**7. Connection Lifecycle**

Agent:
1. Open WebSocket to `/agent/ws` with `Authorization: Bearer <token>`
2. Send `register` frame with session metadata
3. Stream `output` frames as PTY produces bytes
4. Receive `input` and `resize` from relay (forwarded from browsers)
5. Relay sends WebSocket pings every 10s; agent must respond with pong
6. On disconnect, relay removes the session from the registry

Browser:
1. Fetch `GET /api/sessions` with Basic Auth to list live sessions
2. Open WebSocket to `/api/sessions/:id/ws` with Basic Auth
3. Receive `output` frames, render in terminal emulator
4. Optionally send `input` frames (base64-encoded keystrokes)
5. Send `resize` frame when terminal dimensions change
6. Same-origin policy enforced on WebSocket upgrade

**8. Mobile Implementation Notes**

Terminal rendering libraries:
- iOS: SwiftTerm (https://github.com/migueldeicaza/SwiftTerm)
- Android: Termux terminal-emulator or TerminalView

Data encoding: all `data` fields are standard base64 (RFC 4648). Decode to raw bytes before writing to terminal emulator.

Reconnection: the relay does not persist sessions. If the agent disconnects, the session disappears. Mobile clients should handle `onclose` gracefully and return to the session list.

Read-only by default: the relay dashboard is designed for monitoring. Implement an explicit toggle before sending `input` frames to avoid accidental keystrokes.

- [ ] **Step 2: Commit**

```bash
git add docs/protocol.md
git commit -m "docs: add WebSocket protocol specification for mobile clients"
```

---

### Task 16: Final verification

Run the complete test suite and verify the build.

**Files:** None (verification only)

- [ ] **Step 1: Run all Go tests**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 2: Run web tests**

```bash
cd web && npm test
```

Expected: all pass.

- [ ] **Step 3: Build both binaries**

```bash
make build
```

Expected: `bin/agentunnel` and `bin/relay` are produced. No `bin/agent` or `bin/client`.

- [ ] **Step 4: Verify no internal/ directory remains**

```bash
test ! -d internal && echo "OK: internal/ removed" || echo "FAIL: internal/ still exists"
```

Expected: `OK: internal/ removed`

- [ ] **Step 5: Verify no stale imports**

```bash
grep -r 'internal/' --include='*.go' . | grep -v '.git'
```

Expected: no matches.

- [ ] **Step 6: Verify web build output**

```bash
ls webui/dist/index.html && echo "OK" || echo "FAIL"
ls webui/dist/relay.html 2>/dev/null && echo "FAIL: relay.html still exists" || echo "OK: no relay.html"
```

Expected: OK for both.
