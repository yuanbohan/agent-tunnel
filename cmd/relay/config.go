package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	relayconfig "yuanbohan/tunnel/internal/config"
)

const defaultRelayListenAddr = relayconfig.DefaultRelayListenAddr

type serveConfig struct {
	ListenAddr string
}

type inviteCreateConfig struct {
	RelayAddr     string
	OperatorToken string
	Count         int
	ExpiresInDays int
}

type inviteDisableConfig struct {
	RelayAddr     string
	OperatorToken string
	Code          string
}

type userDeleteConfig struct {
	RelayAddr     string
	OperatorToken string
	Username      string
}

type usageError struct {
	msg string
}

func (e usageError) Error() string {
	return e.msg
}

func usagef(format string, args ...any) error {
	return usageError{msg: fmt.Sprintf(format, args...)}
}

func loadServeConfig(getenv func(string) string, args []string) (serveConfig, error) {
	cfg := serveConfig{
		ListenAddr: envOrDefault(getenv, "RELAY_LISTEN_ADDR", defaultRelayListenAddr),
	}

	fs := newFlagSet("serve")
	fs.StringVar(&cfg.ListenAddr, "listen-addr", cfg.ListenAddr, "relay listen address")
	if err := fs.Parse(args); err != nil {
		return serveConfig{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return serveConfig{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := relayconfig.SetupRelay(getenv, cfg.ListenAddr); err != nil {
		return serveConfig{}, usagef("%v", err)
	}
	return serveConfig{ListenAddr: relayconfig.RelayListenAddr()}, nil
}

func loadInviteCreateConfig(getenv func(string) string, args []string) (inviteCreateConfig, error) {
	cfg := inviteCreateConfig{
		RelayAddr:     envOrDefault(getenv, "RELAY_LISTEN_ADDR", defaultRelayListenAddr),
		OperatorToken: envValue(getenv, "RELAY_OPERATOR_TOKEN"),
		Count:         1,
	}
	expiresIn := "7d"

	fs := newFlagSet("invite create")
	fs.IntVar(&cfg.Count, "count", cfg.Count, "number of invite codes to create")
	fs.StringVar(&expiresIn, "expires-in", expiresIn, "invite lifetime in whole days, for example 7d")
	if err := fs.Parse(args); err != nil {
		return inviteCreateConfig{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return inviteCreateConfig{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if cfg.OperatorToken == "" {
		return inviteCreateConfig{}, usagef("missing RELAY_OPERATOR_TOKEN")
	}
	if cfg.Count <= 0 {
		return inviteCreateConfig{}, usagef("invalid --count: must be greater than 0")
	}
	days, err := parseInviteExpiryDays(expiresIn)
	if err != nil {
		return inviteCreateConfig{}, err
	}
	cfg.ExpiresInDays = days
	return cfg, nil
}

func loadInviteDisableConfig(getenv func(string) string, args []string) (inviteDisableConfig, error) {
	cfg := inviteDisableConfig{
		RelayAddr:     envOrDefault(getenv, "RELAY_LISTEN_ADDR", defaultRelayListenAddr),
		OperatorToken: envValue(getenv, "RELAY_OPERATOR_TOKEN"),
	}

	fs := newFlagSet("invite disable")
	fs.StringVar(&cfg.Code, "code", "", "invite code to disable")
	if err := fs.Parse(args); err != nil {
		return inviteDisableConfig{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return inviteDisableConfig{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	switch {
	case cfg.OperatorToken == "":
		return inviteDisableConfig{}, usagef("missing RELAY_OPERATOR_TOKEN")
	case strings.TrimSpace(cfg.Code) == "":
		return inviteDisableConfig{}, usagef("missing --code")
	default:
		return cfg, nil
	}
}

func loadUserDeleteConfig(getenv func(string) string, args []string) (userDeleteConfig, error) {
	cfg := userDeleteConfig{
		RelayAddr:     envOrDefault(getenv, "RELAY_LISTEN_ADDR", defaultRelayListenAddr),
		OperatorToken: envValue(getenv, "RELAY_OPERATOR_TOKEN"),
	}

	fs := newFlagSet("user delete")
	fs.StringVar(&cfg.Username, "username", "", "username to delete")
	if err := fs.Parse(args); err != nil {
		return userDeleteConfig{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return userDeleteConfig{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	switch {
	case cfg.OperatorToken == "":
		return userDeleteConfig{}, usagef("missing RELAY_OPERATOR_TOKEN")
	case strings.TrimSpace(cfg.Username) == "":
		return userDeleteConfig{}, usagef("missing --username")
	default:
		return cfg, nil
	}
}

func parseInviteExpiryDays(raw string) (int, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return 0, usagef("invalid --expires-in %q: use whole days like 1d or 7d", raw)
	}
	if !strings.HasSuffix(trimmed, "d") {
		return 0, usagef("invalid --expires-in %q: use whole days like 1d or 7d", raw)
	}
	days, err := strconv.Atoi(strings.TrimSuffix(trimmed, "d"))
	if err != nil || days <= 0 {
		return 0, usagef("invalid --expires-in %q: use whole days like 1d or 7d", raw)
	}
	return days, nil
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

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}
