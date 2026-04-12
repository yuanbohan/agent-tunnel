package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	runLocalE2EEnv   = "AGENTUNNEL_RUN_LOCAL_E2E"
	testDatabaseEnv  = "AGENTUNNEL_TEST_DATABASE_URL"
	relayReadyWindow = 10 * time.Second
	stopTimeout      = 5 * time.Second
)

type binaries struct {
	migrator string
	relay    string
	tunnel   string
	launcher string
}

type Harness struct {
	t             *testing.T
	rootDir       string
	schemaDir     string
	tempDir       string
	binDir        string
	db            *sql.DB
	dsn           string
	binaries      binaries
	listenAddr    string
	baseURL       string
	appSecret     string
	operatorToken string
	httpClient    *http.Client
	relay         *managedProcess
	tunnel        *TunnelProcess
}

func newHarness(t *testing.T) *Harness {
	t.Helper()

	if os.Getenv(runLocalE2EEnv) != "1" {
		t.Skipf("set %s=1 to run local e2e regression", runLocalE2EEnv)
	}

	dsn := requireNonEmptyEnv(t, testDatabaseEnv)
	root := repoRoot(t)
	tmp := t.TempDir()
	listenAddr := reserveLoopbackAddr(t)

	h := &Harness{
		t:          t,
		rootDir:    root,
		schemaDir:  schemaDir(t),
		tempDir:    tmp,
		binDir:     filepath.Join(tmp, "bin"),
		db:         openTestDatabase(t, dsn),
		dsn:        dsn,
		listenAddr: listenAddr,
		baseURL:    "http://" + listenAddr,
		appSecret:  fmt.Sprintf("local-e2e-secret-%d", time.Now().UnixNano()),
		operatorToken: fmt.Sprintf(
			"local-e2e-operator-%d",
			time.Now().UnixNano(),
		),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	if err := os.MkdirAll(h.binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin dir: %v", err)
	}
	t.Cleanup(func() {
		h.Close()
	})
	return h
}

func (h *Harness) Prepare(ctx context.Context) {
	h.t.Helper()

	if err := h.buildBinaries(ctx); err != nil {
		h.t.Fatal(err)
	}
	if err := h.runMigrations(ctx); err != nil {
		h.t.Fatal(err)
	}
	if err := h.startRelay(ctx); err != nil {
		h.t.Fatal(err)
	}
}

func (h *Harness) Close() {
	if h.tunnel != nil {
		if err := h.tunnel.Stop(stopTimeout); err != nil {
			h.t.Logf("stop tunnel: %v", err)
		}
	}
	if h.relay != nil {
		if err := h.relay.Stop(stopTimeout); err != nil {
			h.t.Logf("stop relay: %v", err)
		}
	}
}

func (h *Harness) CreateInvite(ctx context.Context) string {
	h.t.Helper()

	cmd := exec.CommandContext(h.commandContext(ctx), h.binaries.relay, "invite", "create", "--count", "1", "--expires-in", "7d")
	cmd.Env = h.commandEnv(map[string]string{
		"RELAY_LISTEN_ADDR":    h.listenAddr,
		"RELAY_OPERATOR_TOKEN": h.operatorToken,
	})

	output, err := runCommand("relay invite create", cmd)
	if err != nil {
		h.t.Fatal(err)
	}
	code, err := trimLine(output)
	if err != nil {
		h.t.Fatalf("parse invite code: %v", err)
	}
	return code
}

func (h *Harness) StartTunnel(agentToken string) *TunnelProcess {
	h.t.Helper()

	tunnel, err := startTunnelProcess(h.binaries.tunnel, TunnelConfig{
		BaseURL:      h.baseURL,
		AgentToken:   agentToken,
		LauncherName: filepath.Base(h.binaries.launcher),
		LauncherPath: h.binDir,
		Label:        "local-e2e",
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.tunnel = tunnel
	return tunnel
}

func (h *Harness) buildBinaries(ctx context.Context) error {
	type target struct {
		pkg  string
		name string
		dest *string
	}

	targets := []target{
		{pkg: "./cmd/migrate", name: "relay-migrate", dest: &h.binaries.migrator},
		{pkg: "./cmd/relay", name: "relay", dest: &h.binaries.relay},
		{pkg: "./cmd/tunnel", name: "tunnel", dest: &h.binaries.tunnel},
		{pkg: "./cmd/e2e-launcher", name: "e2e-launcher", dest: &h.binaries.launcher},
	}

	for _, target := range targets {
		outputPath := buildOutputPath(h.binDir, target.name)
		cmd := exec.CommandContext(h.commandContext(ctx), "go", "build", "-o", outputPath, target.pkg)
		cmd.Dir = h.rootDir
		if output, err := runCommand("go build "+target.pkg, cmd); err != nil {
			return fmt.Errorf("%w", err)
		} else if output != "" {
			h.t.Logf("%s", output)
		}
		mustStatPath(h.t, outputPath)
		*target.dest = outputPath
	}
	return nil
}

func (h *Harness) runMigrations(ctx context.Context) error {
	cmd := exec.CommandContext(h.commandContext(ctx), h.binaries.migrator, "--schema-dir", h.schemaDir)
	cmd.Env = h.commandEnv(map[string]string{
		"RELAY_DATABASE_URL": h.dsn,
	})
	if output, err := runCommand("relay-migrate", cmd); err != nil {
		return err
	} else if output != "" {
		h.t.Logf("%s", output)
	}
	return nil
}

func (h *Harness) startRelay(ctx context.Context) error {
	cmd := exec.CommandContext(h.commandContext(ctx), h.binaries.relay, "serve", "--listen-addr", h.listenAddr)
	cmd.Env = h.commandEnv(map[string]string{
		"RELAY_DATABASE_URL":   h.dsn,
		"RELAY_APP_SECRET":     h.appSecret,
		"RELAY_OPERATOR_TOKEN": h.operatorToken,
		"RELAY_LISTEN_ADDR":    h.listenAddr,
	})

	proc, err := startManagedProcess("relay", cmd)
	if err != nil {
		return err
	}
	h.relay = proc

	readyCtx, cancel := context.WithTimeout(ctx, relayReadyWindow)
	defer cancel()
	return waitForHTTP(readyCtx, h.httpClient, h.baseURL+"/healthz", proc)
}

func (h *Harness) commandEnv(overrides map[string]string) []string {
	env := append([]string(nil), os.Environ()...)
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func (h *Harness) commandContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func reserveLoopbackAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback addr: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close reserved loopback addr: %v", err)
	}
	return addr
}
