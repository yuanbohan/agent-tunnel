package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"yuanbohan/tunnel/relay"
)

func validEnv(key string) string {
	switch key {
	case "AGENTUNNEL_BASIC_USER":
		return "demo"
	case "AGENTUNNEL_BASIC_PASSWORD":
		return "secret"
	case "AGENTUNNEL_AGENT_TOKEN":
		return "agent-token"
	default:
		return ""
	}
}

func TestLoadMainConfigDefaultsListenAddr(t *testing.T) {
	_, err := loadMainConfig(func(string) string { return "" }, "")
	if err == nil {
		t.Fatal("expected missing auth env to fail")
	}

	cfg, err := loadMainConfig(validEnv, "")
	if err != nil {
		t.Fatalf("loadMainConfig returned error: %v", err)
	}
	if cfg.ListenAddr != "0.0.0.0:8586" {
		t.Fatalf("ListenAddr = %q, want 0.0.0.0:8586", cfg.ListenAddr)
	}
}

func TestLoadMainConfig_portFlag(t *testing.T) {
	cfg, err := loadMainConfig(validEnv, "9999")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "0.0.0.0:9999" {
		t.Errorf("ListenAddr = %q, want 0.0.0.0:9999", cfg.ListenAddr)
	}
}

func TestLoadMainConfigIgnoresLegacyRelayAddrEnv(t *testing.T) {
	cfg, err := loadMainConfig(func(key string) string {
		if key == "AGENTUNNEL_RELAY_ADDR" {
			return "127.0.0.1:7777"
		}
		return validEnv(key)
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "0.0.0.0:8586" {
		t.Errorf("ListenAddr = %q, want 0.0.0.0:8586 (legacy env should be ignored)", cfg.ListenAddr)
	}
}

func TestLogRelayStartedWritesListenAddr(t *testing.T) {
	var buf bytes.Buffer

	logRelayStarted(relay.NewLogger(&buf), mainConfig{ListenAddr: "0.0.0.0:8586"})

	got := buf.String()
	if !strings.Contains(got, `"event":"relay_started"`) {
		t.Fatalf("log = %q, want event relay_started", got)
	}
	if !strings.Contains(got, `"listen_addr":"0.0.0.0:8586"`) {
		t.Fatalf("log = %q, want listen_addr 0.0.0.0:8586", got)
	}
}

func TestNewHTTPServerConfiguresTimeouts(t *testing.T) {
	srv := newHTTPServer(mainConfig{ListenAddr: "0.0.0.0:8586"}, http.NewServeMux())

	if srv.Addr != "0.0.0.0:8586" {
		t.Fatalf("Addr = %q, want 0.0.0.0:8586", srv.Addr)
	}
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout = %v, want > 0", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout <= 0 {
		t.Fatalf("ReadTimeout = %v, want > 0", srv.ReadTimeout)
	}
	if srv.WriteTimeout <= 0 {
		t.Fatalf("WriteTimeout = %v, want > 0", srv.WriteTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout = %v, want > 0", srv.IdleTimeout)
	}
	if srv.ReadHeaderTimeout > srv.ReadTimeout {
		t.Fatalf("ReadHeaderTimeout = %v, want <= ReadTimeout %v", srv.ReadHeaderTimeout, srv.ReadTimeout)
	}
	if srv.IdleTimeout < 30*time.Second {
		t.Fatalf("IdleTimeout = %v, want >= 30s", srv.IdleTimeout)
	}
}
