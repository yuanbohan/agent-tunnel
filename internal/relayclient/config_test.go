package relayclient

import "testing"

func TestLoadConfigDisabledWhenNoRelayURL(t *testing.T) {
	cfg, enabled, err := LoadConfig(func(string) string { return "" }, "")
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if enabled {
		t.Fatal("enabled = true, want false")
	}
	if cfg.URL != "" || cfg.Token != "" {
		t.Fatalf("cfg = %#v, want zero value", cfg)
	}
}

func TestLoadConfigRequiresTokenWhenRelayURLPresent(t *testing.T) {
	_, _, err := LoadConfig(func(key string) string {
		if key == "AGENTUNNEL_RELAY_URL" {
			return "wss://relay.example"
		}
		return ""
	}, "")
	if err == nil {
		t.Fatal("expected an error for missing token")
	}
}

func TestLoadConfigPrefersFlagURLOverEnvironment(t *testing.T) {
	cfg, enabled, err := LoadConfig(func(key string) string {
		if key == "AGENTUNNEL_RELAY_URL" {
			return "wss://ignored.example"
		}
		if key == "AGENTUNNEL_RELAY_TOKEN" {
			return "secret-token"
		}
		return ""
	}, "wss://relay.example")
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if !enabled {
		t.Fatal("enabled = false, want true")
	}
	if cfg.URL != "wss://relay.example" {
		t.Fatalf("URL = %q, want wss://relay.example", cfg.URL)
	}
	if cfg.Token != "secret-token" {
		t.Fatalf("Token = %q, want secret-token", cfg.Token)
	}
}
