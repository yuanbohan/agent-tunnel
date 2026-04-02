package main

import (
	"net/http"
	"testing"
	"time"
)

func TestLoadMainConfigDefaultsListenAddr(t *testing.T) {
	cfg, err := loadMainConfig(func(string) string { return "" })
	if err == nil {
		t.Fatal("expected missing auth env to fail")
	}

	cfg, err = loadMainConfig(func(key string) string {
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
	})
	if err != nil {
		t.Fatalf("loadMainConfig returned error: %v", err)
	}
	if cfg.ListenAddr != ":8586" {
		t.Fatalf("ListenAddr = %q, want :8586", cfg.ListenAddr)
	}
}

func TestNewHTTPServerConfiguresTimeouts(t *testing.T) {
	srv := newHTTPServer(mainConfig{ListenAddr: ":8586"}, http.NewServeMux())

	if srv.Addr != ":8586" {
		t.Fatalf("Addr = %q, want :8586", srv.Addr)
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
