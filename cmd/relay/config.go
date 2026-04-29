package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	relayconfig "yuanbohan/tunnel/internal/config"
)

const defaultRelayListenAddr = relayconfig.DefaultRelayListenAddr
const defaultRelaySTUNListenAddr = relayconfig.DefaultRelaySTUNListenAddr

type serveConfig struct {
	ListenAddr     string
	STUNListenAddr string
	LogFile        string
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

type inviteListConfig struct {
	RelayAddr     string
	OperatorToken string
}

type userDeleteConfig struct {
	RelayAddr     string
	OperatorToken string
	Username      string
}

type userTierConfig struct {
	RelayAddr     string
	OperatorToken string
	Username      string
	Tier          string
}

type inviteCreateFlags struct {
	Count     int
	ExpiresIn string
}

type inviteDisableFlags struct {
	Code string
}

type inviteListFlags struct{}

type userDeleteFlags struct {
	Username string
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

// applyServeFlags registers serve-subcommand flags on fs.
func applyServeFlags(fs *pflag.FlagSet, cfg *serveConfig) {
	fs.StringVarP(&cfg.ListenAddr, "listen-addr", "a", "", "relay listen address (env: RELAY_LISTEN_ADDR)")
	fs.StringVar(&cfg.STUNListenAddr, "stun-listen-addr", "", `STUN UDP listen address, or "off" to disable (env: RELAY_STUN_LISTEN_ADDR)`)
	fs.StringVarP(&cfg.LogFile, "log-file", "L", "", "append structured logs to this file (env: RELAY_LOG_FILE, default: stderr)")
}

func finalizeServeConfig(cfg serveConfig, getenv func(string) string) (serveConfig, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = envOrDefault(getenv, "RELAY_LISTEN_ADDR", defaultRelayListenAddr)
	}
	if cfg.STUNListenAddr == "" {
		cfg.STUNListenAddr = envOrDefault(getenv, "RELAY_STUN_LISTEN_ADDR", defaultRelaySTUNListenAddr)
	}
	if cfg.LogFile == "" {
		cfg.LogFile = envValue(getenv, "RELAY_LOG_FILE")
	}
	if err := relayconfig.SetupRelay(getenv, cfg.ListenAddr, cfg.STUNListenAddr); err != nil {
		return serveConfig{}, usagef("%v", err)
	}
	return serveConfig{ListenAddr: relayconfig.RelayListenAddr(), STUNListenAddr: relayconfig.RelaySTUNListenAddr(), LogFile: cfg.LogFile}, nil
}

func loadServeConfig(getenv func(string) string, args []string) (serveConfig, error) {
	var cfg serveConfig
	fs := newFlagSet("serve")
	applyServeFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		return serveConfig{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return serveConfig{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return finalizeServeConfig(cfg, getenv)
}

// applyInviteCreateFlags registers invite-create flags on fs.
func applyInviteCreateFlags(fs *pflag.FlagSet, flags *inviteCreateFlags) {
	fs.IntVarP(&flags.Count, "count", "n", 1, "number of invite codes to create")
	fs.StringVarP(&flags.ExpiresIn, "expires-in", "e", "7d", "invite lifetime in whole days, for example 7d")
}

func finalizeInviteCreateConfig(flags inviteCreateFlags, getenv func(string) string) (inviteCreateConfig, error) {
	cfg := inviteCreateConfig{
		RelayAddr:     envOrDefault(getenv, "RELAY_LISTEN_ADDR", defaultRelayListenAddr),
		OperatorToken: envValue(getenv, "RELAY_OPERATOR_TOKEN"),
		Count:         flags.Count,
	}
	if cfg.OperatorToken == "" {
		return inviteCreateConfig{}, usagef("missing RELAY_OPERATOR_TOKEN")
	}
	if cfg.Count <= 0 {
		return inviteCreateConfig{}, usagef("invalid --count: must be greater than 0")
	}
	days, err := parseInviteExpiryDays(flags.ExpiresIn)
	if err != nil {
		return inviteCreateConfig{}, err
	}
	cfg.ExpiresInDays = days
	return cfg, nil
}

func loadInviteCreateConfig(getenv func(string) string, args []string) (inviteCreateConfig, error) {
	var flags inviteCreateFlags
	fs := newFlagSet("invite create")
	applyInviteCreateFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return inviteCreateConfig{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return inviteCreateConfig{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return finalizeInviteCreateConfig(flags, getenv)
}

// applyInviteDisableFlags registers invite-disable flags on fs.
func applyInviteDisableFlags(fs *pflag.FlagSet, flags *inviteDisableFlags) {
	fs.StringVarP(&flags.Code, "code", "c", "", "invite code to disable (required)")
}

func finalizeInviteDisableConfig(flags inviteDisableFlags, getenv func(string) string) (inviteDisableConfig, error) {
	cfg := inviteDisableConfig{
		RelayAddr:     envOrDefault(getenv, "RELAY_LISTEN_ADDR", defaultRelayListenAddr),
		OperatorToken: envValue(getenv, "RELAY_OPERATOR_TOKEN"),
		Code:          flags.Code,
	}
	switch {
	case cfg.OperatorToken == "":
		return inviteDisableConfig{}, usagef("missing RELAY_OPERATOR_TOKEN")
	case strings.TrimSpace(cfg.Code) == "":
		return inviteDisableConfig{}, usagef(`required flag(s) "code" not set`)
	}
	return cfg, nil
}

func loadInviteDisableConfig(getenv func(string) string, args []string) (inviteDisableConfig, error) {
	var flags inviteDisableFlags
	fs := newFlagSet("invite disable")
	applyInviteDisableFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return inviteDisableConfig{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return inviteDisableConfig{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return finalizeInviteDisableConfig(flags, getenv)
}

// applyInviteListFlags registers invite-list flags on fs.
func applyInviteListFlags(fs *pflag.FlagSet, flags *inviteListFlags) {}

func finalizeInviteListConfig(flags inviteListFlags, getenv func(string) string) (inviteListConfig, error) {
	cfg := inviteListConfig{
		RelayAddr:     envOrDefault(getenv, "RELAY_LISTEN_ADDR", defaultRelayListenAddr),
		OperatorToken: envValue(getenv, "RELAY_OPERATOR_TOKEN"),
	}
	if cfg.OperatorToken == "" {
		return inviteListConfig{}, usagef("missing RELAY_OPERATOR_TOKEN")
	}
	return cfg, nil
}

func loadInviteListConfig(getenv func(string) string, args []string) (inviteListConfig, error) {
	var flags inviteListFlags
	fs := newFlagSet("invite list")
	applyInviteListFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return inviteListConfig{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return inviteListConfig{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return finalizeInviteListConfig(flags, getenv)
}

// applyUserDeleteFlags registers user-delete flags on fs.
func applyUserDeleteFlags(fs *pflag.FlagSet, flags *userDeleteFlags) {
	fs.StringVarP(&flags.Username, "username", "u", "", "username to delete (required)")
}

func finalizeUserDeleteConfig(flags userDeleteFlags, getenv func(string) string) (userDeleteConfig, error) {
	cfg := userDeleteConfig{
		RelayAddr:     envOrDefault(getenv, "RELAY_LISTEN_ADDR", defaultRelayListenAddr),
		OperatorToken: envValue(getenv, "RELAY_OPERATOR_TOKEN"),
		Username:      flags.Username,
	}
	switch {
	case cfg.OperatorToken == "":
		return userDeleteConfig{}, usagef("missing RELAY_OPERATOR_TOKEN")
	case strings.TrimSpace(cfg.Username) == "":
		return userDeleteConfig{}, usagef(`required flag(s) "username" not set`)
	}
	return cfg, nil
}

func loadUserDeleteConfig(getenv func(string) string, args []string) (userDeleteConfig, error) {
	var flags userDeleteFlags
	fs := newFlagSet("user delete")
	applyUserDeleteFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return userDeleteConfig{}, usagef("%v", err)
	}
	if len(fs.Args()) != 0 {
		return userDeleteConfig{}, usagef("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return finalizeUserDeleteConfig(flags, getenv)
}

func finalizeUserTierConfig(username, tier string, getenv func(string) string) (userTierConfig, error) {
	cfg := userTierConfig{
		RelayAddr:     envOrDefault(getenv, "RELAY_LISTEN_ADDR", defaultRelayListenAddr),
		OperatorToken: envValue(getenv, "RELAY_OPERATOR_TOKEN"),
		Username:      username,
		Tier:          tier,
	}
	switch {
	case cfg.OperatorToken == "":
		return userTierConfig{}, usagef("missing RELAY_OPERATOR_TOKEN")
	case strings.TrimSpace(cfg.Username) == "":
		return userTierConfig{}, usagef("missing username")
	case strings.TrimSpace(cfg.Tier) == "":
		return userTierConfig{}, usagef("missing tier")
	}
	return cfg, nil
}

func loadUserTierConfig(getenv func(string) string, args []string) (userTierConfig, error) {
	fs := newFlagSet("user tier")
	if err := fs.Parse(args); err != nil {
		return userTierConfig{}, usagef("%v", err)
	}
	remaining := fs.Args()
	if len(remaining) != 2 {
		return userTierConfig{}, usagef("accepts 2 arg(s), received %d", len(remaining))
	}
	return finalizeUserTierConfig(remaining[0], remaining[1], getenv)
}

func parseInviteExpiryDays(raw string) (int, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" || !strings.HasSuffix(trimmed, "d") {
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

func newFlagSet(name string) *pflag.FlagSet {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}
