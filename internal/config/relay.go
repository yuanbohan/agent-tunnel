package config

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRelayListenAddr     = "127.0.0.1:8586"
	DefaultRelaySTUNListenAddr = "0.0.0.0:3478"
)

type Relay struct {
	ListenAddr             string
	STUNListenAddr         string
	DatabaseURL            string
	AppSecret              string
	OperatorToken          string
	WSSinkBufferSize       int
	WSWriteTimeout         time.Duration
	AgentReadTimeout       time.Duration
	AgentPingInterval      time.Duration
	AgentPingWriteTimeout  time.Duration
	ClientReadTimeout      time.Duration
	ClientPingInterval     time.Duration
	ClientPingWriteTimeout time.Duration
}

var (
	relayMu    sync.RWMutex
	relayState = defaultRelay()
)

func defaultRelay() Relay {
	return Relay{
		ListenAddr:             DefaultRelayListenAddr,
		STUNListenAddr:         DefaultRelaySTUNListenAddr,
		WSSinkBufferSize:       64,
		WSWriteTimeout:         5 * time.Second,
		AgentReadTimeout:       30 * time.Second,
		AgentPingInterval:      10 * time.Second,
		AgentPingWriteTimeout:  5 * time.Second,
		ClientReadTimeout:      30 * time.Second,
		ClientPingInterval:     10 * time.Second,
		ClientPingWriteTimeout: 5 * time.Second,
	}
}

func SetupRelay(getenv func(string) string, listenAddrOverride string, stunListenAddrOverride string) error {
	cfg := defaultRelay()
	cfg.ListenAddr = envOrDefault(getenv, "RELAY_LISTEN_ADDR", cfg.ListenAddr)
	cfg.STUNListenAddr = NormalizeSTUNListenAddr(envOrDefault(getenv, "RELAY_STUN_LISTEN_ADDR", cfg.STUNListenAddr))
	cfg.DatabaseURL = envValue(getenv, "RELAY_DATABASE_URL")
	cfg.AppSecret = envValue(getenv, "RELAY_APP_SECRET")
	cfg.OperatorToken = envValue(getenv, "RELAY_OPERATOR_TOKEN")

	if trimmed := strings.TrimSpace(listenAddrOverride); trimmed != "" {
		cfg.ListenAddr = trimmed
	}
	if trimmed := strings.TrimSpace(stunListenAddrOverride); trimmed != "" {
		cfg.STUNListenAddr = NormalizeSTUNListenAddr(trimmed)
	}

	switch {
	case strings.TrimSpace(cfg.ListenAddr) == "":
		return fmt.Errorf("missing relay listen address")
	case cfg.DatabaseURL == "":
		return fmt.Errorf("missing RELAY_DATABASE_URL")
	case cfg.AppSecret == "":
		return fmt.Errorf("missing RELAY_APP_SECRET")
	case cfg.OperatorToken == "":
		return fmt.Errorf("missing RELAY_OPERATOR_TOKEN")
	}

	setRelay(cfg)
	return nil
}

func UseRelayForTest(cfg Relay) func() {
	prev := CurrentRelay()
	next := defaultRelay()

	if cfg.ListenAddr != "" {
		next.ListenAddr = cfg.ListenAddr
	}
	if cfg.STUNListenAddr != "" {
		next.STUNListenAddr = NormalizeSTUNListenAddr(cfg.STUNListenAddr)
	}
	if cfg.DatabaseURL != "" {
		next.DatabaseURL = cfg.DatabaseURL
	}
	if cfg.AppSecret != "" {
		next.AppSecret = cfg.AppSecret
	}
	if cfg.OperatorToken != "" {
		next.OperatorToken = cfg.OperatorToken
	}
	if cfg.WSSinkBufferSize > 0 {
		next.WSSinkBufferSize = cfg.WSSinkBufferSize
	}
	if cfg.WSWriteTimeout > 0 {
		next.WSWriteTimeout = cfg.WSWriteTimeout
	}
	if cfg.AgentReadTimeout > 0 {
		next.AgentReadTimeout = cfg.AgentReadTimeout
	}
	if cfg.AgentPingInterval > 0 {
		next.AgentPingInterval = cfg.AgentPingInterval
	}
	if cfg.AgentPingWriteTimeout > 0 {
		next.AgentPingWriteTimeout = cfg.AgentPingWriteTimeout
	}
	if cfg.ClientReadTimeout > 0 {
		next.ClientReadTimeout = cfg.ClientReadTimeout
	}
	if cfg.ClientPingInterval > 0 {
		next.ClientPingInterval = cfg.ClientPingInterval
	}
	if cfg.ClientPingWriteTimeout > 0 {
		next.ClientPingWriteTimeout = cfg.ClientPingWriteTimeout
	}

	setRelay(next)
	return func() { setRelay(prev) }
}

func CurrentRelay() Relay {
	relayMu.RLock()
	defer relayMu.RUnlock()
	return relayState
}

func RelayListenAddr() string              { return CurrentRelay().ListenAddr }
func RelaySTUNListenAddr() string          { return CurrentRelay().STUNListenAddr }
func RelayDatabaseURL() string             { return CurrentRelay().DatabaseURL }
func RelayAppSecret() string               { return CurrentRelay().AppSecret }
func RelayOperatorToken() string           { return CurrentRelay().OperatorToken }
func RelayWSSinkBufferSize() int           { return CurrentRelay().WSSinkBufferSize }
func RelayWSWriteTimeout() time.Duration   { return CurrentRelay().WSWriteTimeout }
func RelayAgentReadTimeout() time.Duration { return CurrentRelay().AgentReadTimeout }
func RelayAgentPingInterval() time.Duration {
	return CurrentRelay().AgentPingInterval
}
func RelayAgentPingWriteTimeout() time.Duration {
	return CurrentRelay().AgentPingWriteTimeout
}
func RelayClientReadTimeout() time.Duration {
	return CurrentRelay().ClientReadTimeout
}
func RelayClientPingInterval() time.Duration {
	return CurrentRelay().ClientPingInterval
}
func RelayClientPingWriteTimeout() time.Duration {
	return CurrentRelay().ClientPingWriteTimeout
}

func setRelay(cfg Relay) {
	relayMu.Lock()
	defer relayMu.Unlock()
	relayState = cfg
}

func envOrDefault(getenv func(string) string, key, fallback string) string {
	if value := envValue(getenv, key); value != "" {
		return value
	}
	return fallback
}

func envValue(getenv func(string) string, key string) string {
	if getenv == nil {
		return ""
	}
	return strings.TrimSpace(getenv(key))
}

// NormalizeSTUNListenAddr maps disabled sentinel values to an empty listen address.
func NormalizeSTUNListenAddr(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off", "disabled", "none", "false":
		return ""
	default:
		return strings.TrimSpace(value)
	}
}
