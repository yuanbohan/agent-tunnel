package main

import (
	"errors"
	"testing"
)

func testEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadServeConfigDefaultsAndRequirements(t *testing.T) {
	_, err := loadServeConfig(testEnv(nil), nil)
	if err == nil {
		t.Fatal("expected missing env to fail")
	}

	cfg, err := loadServeConfig(testEnv(map[string]string{
		"RELAY_DATABASE_URL":   "postgres://relay",
		"RELAY_APP_SECRET":     "secret",
		"RELAY_OPERATOR_TOKEN": "operator-secret",
	}), nil)
	if err != nil {
		t.Fatalf("loadServeConfig returned error: %v", err)
	}
	if cfg.ListenAddr != defaultRelayListenAddr {
		t.Fatalf("ListenAddr = %q, want %q", cfg.ListenAddr, defaultRelayListenAddr)
	}
	if cfg.STUNListenAddr != defaultRelaySTUNListenAddr {
		t.Fatalf("STUNListenAddr = %q, want %q", cfg.STUNListenAddr, defaultRelaySTUNListenAddr)
	}
}

func TestLoadServeConfigAllowsListenAddrFlag(t *testing.T) {
	cfg, err := loadServeConfig(testEnv(map[string]string{
		"RELAY_DATABASE_URL":   "postgres://relay",
		"RELAY_APP_SECRET":     "secret",
		"RELAY_OPERATOR_TOKEN": "operator-secret",
	}), []string{"--listen-addr", "127.0.0.1:9999"})
	if err != nil {
		t.Fatalf("loadServeConfig returned error: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:9999" {
		t.Fatalf("ListenAddr = %q, want 127.0.0.1:9999", cfg.ListenAddr)
	}
}

func TestLoadServeConfigAllowsSTUNListenAddrConfiguration(t *testing.T) {
	cfg, err := loadServeConfig(testEnv(map[string]string{
		"RELAY_DATABASE_URL":     "postgres://relay",
		"RELAY_APP_SECRET":       "secret",
		"RELAY_OPERATOR_TOKEN":   "operator-secret",
		"RELAY_STUN_LISTEN_ADDR": "127.0.0.1:3479",
	}), nil)
	if err != nil {
		t.Fatalf("loadServeConfig returned error: %v", err)
	}
	if cfg.STUNListenAddr != "127.0.0.1:3479" {
		t.Fatalf("STUNListenAddr = %q, want 127.0.0.1:3479", cfg.STUNListenAddr)
	}

	cfg, err = loadServeConfig(testEnv(map[string]string{
		"RELAY_DATABASE_URL":   "postgres://relay",
		"RELAY_APP_SECRET":     "secret",
		"RELAY_OPERATOR_TOKEN": "operator-secret",
	}), []string{"--stun-listen-addr", "off"})
	if err != nil {
		t.Fatalf("loadServeConfig returned error: %v", err)
	}
	if cfg.STUNListenAddr != "" {
		t.Fatalf("STUNListenAddr = %q, want disabled empty value", cfg.STUNListenAddr)
	}
}

func TestLoadServeConfigUsesLogFileEnv(t *testing.T) {
	cfg, err := loadServeConfig(testEnv(map[string]string{
		"RELAY_DATABASE_URL":   "postgres://relay",
		"RELAY_APP_SECRET":     "secret",
		"RELAY_OPERATOR_TOKEN": "operator-secret",
		"RELAY_LOG_FILE":       "/tmp/relay.log",
	}), nil)
	if err != nil {
		t.Fatalf("loadServeConfig returned error: %v", err)
	}
	if cfg.LogFile != "/tmp/relay.log" {
		t.Fatalf("LogFile = %q, want /tmp/relay.log", cfg.LogFile)
	}
}

func TestLoadServeConfigAllowsLogFileFlag(t *testing.T) {
	cfg, err := loadServeConfig(testEnv(map[string]string{
		"RELAY_DATABASE_URL":   "postgres://relay",
		"RELAY_APP_SECRET":     "secret",
		"RELAY_OPERATOR_TOKEN": "operator-secret",
		"RELAY_LOG_FILE":       "/tmp/from-env.log",
	}), []string{"--log-file", "/tmp/from-flag.log"})
	if err != nil {
		t.Fatalf("loadServeConfig returned error: %v", err)
	}
	if cfg.LogFile != "/tmp/from-flag.log" {
		t.Fatalf("LogFile = %q, want /tmp/from-flag.log", cfg.LogFile)
	}
}

func TestLoadInviteCreateConfigUsesDefaultSevenDays(t *testing.T) {
	cfg, err := loadInviteCreateConfig(testEnv(map[string]string{
		"RELAY_OPERATOR_TOKEN": "operator-secret",
	}), nil)
	if err != nil {
		t.Fatalf("loadInviteCreateConfig returned error: %v", err)
	}
	if cfg.ExpiresInDays != 7 {
		t.Fatalf("ExpiresInDays = %d, want 7", cfg.ExpiresInDays)
	}
	if cfg.RelayAddr != defaultRelayListenAddr {
		t.Fatalf("RelayAddr = %q, want %q", cfg.RelayAddr, defaultRelayListenAddr)
	}
}

func TestLoadInviteListConfigRequiresToken(t *testing.T) {
	_, err := loadInviteListConfig(testEnv(map[string]string{
		"RELAY_OPERATOR_TOKEN": "operator-secret",
	}), []string{})
	if err != nil {
		t.Fatalf("loadInviteListConfig returned unexpected error: %v", err)
	}

	_, err = loadInviteListConfig(testEnv(map[string]string{}), []string{})
	if err == nil {
		t.Fatal("expected missing operator token to fail")
	}
}

func TestLoadInviteCreateConfigRejectsInvalidValues(t *testing.T) {
	_, err := loadInviteCreateConfig(testEnv(map[string]string{
		"RELAY_OPERATOR_TOKEN": "operator-secret",
	}), nil)
	if err != nil {
		t.Fatalf("loadInviteCreateConfig returned error: %v", err)
	}

	_, err = loadInviteCreateConfig(testEnv(map[string]string{
		"RELAY_OPERATOR_TOKEN": "operator-secret",
	}), []string{"--count", "0"})
	if err == nil {
		t.Fatal("expected invalid count to fail")
	}

	_, err = loadInviteCreateConfig(testEnv(map[string]string{
		"RELAY_OPERATOR_TOKEN": "operator-secret",
	}), []string{"--expires-in", "168h"})
	if err == nil {
		t.Fatal("expected invalid expires-in to fail")
	}

	cfg, err := loadInviteCreateConfig(testEnv(map[string]string{
		"RELAY_OPERATOR_TOKEN": "operator-secret",
		"RELAY_LISTEN_ADDR":    "127.0.0.1:9999",
	}), []string{"--expires-in", "1d"})
	if err != nil {
		t.Fatalf("loadInviteCreateConfig returned error: %v", err)
	}
	if cfg.ExpiresInDays != 1 {
		t.Fatalf("ExpiresInDays = %d, want 1", cfg.ExpiresInDays)
	}
	if cfg.RelayAddr != "127.0.0.1:9999" {
		t.Fatalf("RelayAddr = %q, want 127.0.0.1:9999", cfg.RelayAddr)
	}
}

func TestLoadUserDeleteConfigRequiresUsername(t *testing.T) {
	_, err := loadUserDeleteConfig(testEnv(map[string]string{
		"RELAY_OPERATOR_TOKEN": "operator-secret",
	}), nil)
	if err == nil {
		t.Fatal("expected missing username to fail")
	}
}

func TestParseInviteExpiryDays(t *testing.T) {
	days, err := parseInviteExpiryDays("7d")
	if err != nil {
		t.Fatalf("parseInviteExpiryDays returned error: %v", err)
	}
	if days != 7 {
		t.Fatalf("days = %d, want 7", days)
	}

	_, err = parseInviteExpiryDays("7h")
	if err == nil {
		t.Fatal("expected invalid suffix to fail")
	}
}

func TestUsagefReturnsUsageError(t *testing.T) {
	err := usagef("hello %s", "world")
	var usageErr usageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("errors.As returned false for %#v", err)
	}
	if usageErr.Error() != "hello world" {
		t.Fatalf("usage error = %q, want hello world", usageErr.Error())
	}
}
