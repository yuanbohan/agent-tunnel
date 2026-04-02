package main

import "testing"

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
