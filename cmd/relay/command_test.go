package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRunWithHandlersDispatchesServeCommand(t *testing.T) {
	env := runtimeEnv{
		getenv: testEnv(map[string]string{
			"RELAY_DATABASE_URL":   "postgres://relay",
			"RELAY_APP_SECRET":     "secret",
			"RELAY_OPERATOR_TOKEN": "operator-secret",
		}),
	}

	called := false
	err := runWithHandlers([]string{"serve", "--listen-addr", "127.0.0.1:9999", "--log-file", "/tmp/relay.log"}, env, commandHandlers{
		serve: func(_ context.Context, cfg serveConfig) error {
			called = true
			if cfg.ListenAddr != "127.0.0.1:9999" {
				t.Fatalf("ListenAddr = %q, want 127.0.0.1:9999", cfg.ListenAddr)
			}
			if cfg.STUNListenAddr != defaultRelaySTUNListenAddr {
				t.Fatalf("STUNListenAddr = %q, want %q", cfg.STUNListenAddr, defaultRelaySTUNListenAddr)
			}
			if cfg.LogFile != "/tmp/relay.log" {
				t.Fatalf("LogFile = %q, want /tmp/relay.log", cfg.LogFile)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runWithHandlers returned error: %v", err)
	}
	if !called {
		t.Fatal("serve handler was not called")
	}
}

func TestRunWithHandlersRejectsUnknownCommand(t *testing.T) {
	err := runWithHandlers([]string{"wat"}, runtimeEnv{}, commandHandlers{})
	if err == nil {
		t.Fatal("expected unknown command to fail")
	}
	var usageErr usageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("error = %#v, want usageError", err)
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("error = %q, want unknown-command message", err.Error())
	}
}

func TestRunWithHandlersInviteDisableRequiresCode(t *testing.T) {
	err := runWithHandlers([]string{"invite", "disable"}, runtimeEnv{
		getenv: testEnv(map[string]string{
			"RELAY_OPERATOR_TOKEN": "operator-secret",
		}),
	}, commandHandlers{})
	if err == nil {
		t.Fatal("expected missing code to fail")
	}
	if !strings.Contains(err.Error(), `required flag(s) "code" not set`) {
		t.Fatalf("error = %q, want required code message", err.Error())
	}
}

func TestRunWithHandlersUserDeleteRequiresUsername(t *testing.T) {
	err := runWithHandlers([]string{"user", "delete"}, runtimeEnv{
		getenv: testEnv(map[string]string{
			"RELAY_OPERATOR_TOKEN": "operator-secret",
		}),
	}, commandHandlers{})
	if err == nil {
		t.Fatal("expected missing username to fail")
	}
	if !strings.Contains(err.Error(), `required flag(s) "username" not set`) {
		t.Fatalf("error = %q, want required username message", err.Error())
	}
}

func TestRunWithHandlersUserTierRequiresUsernameAndTier(t *testing.T) {
	err := runWithHandlers([]string{"user", "tier", "alice"}, runtimeEnv{
		getenv: testEnv(map[string]string{
			"RELAY_OPERATOR_TOKEN": "operator-secret",
		}),
	}, commandHandlers{})
	if err == nil {
		t.Fatal("expected missing tier to fail")
	}
	if !strings.Contains(err.Error(), "accepts 2 arg(s), received 1") {
		t.Fatalf("error = %q, want required tier message", err.Error())
	}
}

func TestRunWithHandlersRejectsRelayAddrFlagOnOperatorSubcommands(t *testing.T) {
	for _, args := range [][]string{
		{"invite", "create", "--relay-addr", "127.0.0.1:9999"},
		{"invite", "disable", "--relay-addr", "127.0.0.1:9999", "--code", "AB2C3D"},
		{"invite", "list", "--relay-addr", "127.0.0.1:9999"},
		{"user", "delete", "--relay-addr", "127.0.0.1:9999", "--username", "alice"},
		{"user", "tier", "--relay-addr", "127.0.0.1:9999", "alice", "pro"},
	} {
		err := runWithHandlers(args, runtimeEnv{
			getenv: testEnv(map[string]string{
				"RELAY_OPERATOR_TOKEN": "operator-secret",
			}),
		}, commandHandlers{})
		if err == nil {
			t.Fatalf("args %v: expected unknown flag error", args)
		}
		if !strings.Contains(err.Error(), "unknown flag: --relay-addr") {
			t.Fatalf("args %v: error = %q, want unknown relay-addr flag", args, err.Error())
		}
	}
}

func TestRunWithHandlersPrintsVersion(t *testing.T) {
	var stdout bytes.Buffer
	err := runWithHandlers([]string{"version"}, runtimeEnv{stdout: &stdout}, commandHandlers{})
	if err != nil {
		t.Fatalf("runWithHandlers returned error: %v", err)
	}
	want := "relay v0.1.0-dev\nbranch: unknown\ncommit: unknown\nbuild:  unknown\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunWithHandlersHelpExplainsServerAndLocalOperatorRequirements(t *testing.T) {
	var stdout bytes.Buffer
	err := runWithHandlers([]string{"--help"}, runtimeEnv{stdout: &stdout}, commandHandlers{})
	if err != nil {
		t.Fatalf("runWithHandlers returned error: %v", err)
	}
	for _, fragment := range []string{
		`Run the relay server and local-only operator commands.`,
		`RELAY_DATABASE_URL`,
		`RELAY_APP_SECRET`,
		`RELAY_OPERATOR_TOKEN`,
		`local-only`,
		`RELAY_LISTEN_ADDR`,
		`RELAY_STUN_LISTEN_ADDR`,
		`relay invite disable --code AB2C3D`,
		`relay user tier alice pro`,
	} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("help output = %q, want fragment %q", stdout.String(), fragment)
		}
	}
	if strings.Contains(stdout.String(), "completion") {
		t.Fatalf("help output = %q, want completion command to be hidden", stdout.String())
	}

	for _, args := range [][]string{
		{"invite", "--help"},
		{"invite", "create", "--help"},
		{"invite", "disable", "--help"},
		{"invite", "list", "--help"},
		{"user", "--help"},
		{"user", "delete", "--help"},
		{"user", "tier", "--help"},
	} {
		stdout.Reset()
		err := runWithHandlers(args, runtimeEnv{stdout: &stdout}, commandHandlers{})
		if err != nil {
			t.Fatalf("args %v: runWithHandlers returned error: %v", args, err)
		}
		if strings.Contains(stdout.String(), "--relay-addr") {
			t.Fatalf("args %v: help output = %q, want relay-addr flag to stay hidden", args, stdout.String())
		}
		if strings.Contains(stdout.String(), "completion") {
			t.Fatalf("args %v: help output = %q, want completion command to be hidden", args, stdout.String())
		}
	}
}

func TestRunWithHandlersDispatchesInviteSubcommands(t *testing.T) {
	env := runtimeEnv{
		getenv: testEnv(map[string]string{
			"RELAY_OPERATOR_TOKEN": "operator-secret",
		}),
	}

	createCalled := false
	disableCalled := false
	err := runWithHandlers([]string{"invite", "create", "--expires-in", "7d"}, env, commandHandlers{
		inviteCreate: func(_ context.Context, cfg inviteCreateConfig) error {
			createCalled = true
			if cfg.ExpiresInDays != 7 {
				t.Fatalf("ExpiresInDays = %d, want 7", cfg.ExpiresInDays)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("invite create returned error: %v", err)
	}

	err = runWithHandlers([]string{"invite", "disable", "--code", "AB2C3D"}, env, commandHandlers{
		inviteDisable: func(_ context.Context, cfg inviteDisableConfig) error {
			disableCalled = true
			if cfg.Code != "AB2C3D" {
				t.Fatalf("Code = %q, want AB2C3D", cfg.Code)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("invite disable returned error: %v", err)
	}

	if !createCalled || !disableCalled {
		t.Fatalf("createCalled=%v disableCalled=%v, want both true", createCalled, disableCalled)
	}
}

func TestRunWithHandlersDispatchesInviteListSubcommand(t *testing.T) {
	env := runtimeEnv{
		getenv: testEnv(map[string]string{
			"RELAY_OPERATOR_TOKEN": "operator-secret",
		}),
	}

	listCalled := false
	err := runWithHandlers([]string{"invite", "list"}, env, commandHandlers{
		inviteList: func(_ context.Context, cfg inviteListConfig) error {
			listCalled = true
			if cfg.OperatorToken != "operator-secret" {
				t.Fatalf("OperatorToken = %q, want operator-secret", cfg.OperatorToken)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("invite list returned error: %v", err)
	}
	if !listCalled {
		t.Fatal("invite list handler not called")
	}
}

func TestRunWithHandlersDispatchesUserTierSubcommand(t *testing.T) {
	env := runtimeEnv{
		getenv: testEnv(map[string]string{
			"RELAY_OPERATOR_TOKEN": "operator-secret",
		}),
	}

	tierCalled := false
	err := runWithHandlers([]string{"user", "tier", "Alice", "pro"}, env, commandHandlers{
		userTier: func(_ context.Context, cfg userTierConfig) error {
			tierCalled = true
			if cfg.Username != "Alice" {
				t.Fatalf("Username = %q, want Alice", cfg.Username)
			}
			if cfg.Tier != "pro" {
				t.Fatalf("Tier = %q, want pro", cfg.Tier)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("user tier returned error: %v", err)
	}
	if !tierCalled {
		t.Fatal("user tier handler not called")
	}
}

func TestRunWithHandlersUserTierJSONWrapsErrors(t *testing.T) {
	var stdout bytes.Buffer
	env := runtimeEnv{
		stdout: &stdout,
		getenv: testEnv(map[string]string{
			"RELAY_OPERATOR_TOKEN": "operator-secret",
		}),
	}

	err := runWithHandlers([]string{"user", "tier", "Alice", "pro", "--json"}, env, commandHandlers{
		userTier: func(context.Context, userTierConfig) error {
			return operatorAPIError{StatusCode: 403, Code: 1007, Message: "forbidden"}
		},
	})
	if err == nil {
		t.Fatal("user tier --json error = nil, want operator error")
	}
	var envelope operatorCommandErrorEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("JSON unmarshal returned error: %v\n%s", err, stdout.String())
	}
	if envelope.Error.Code != "operator_api_error" || envelope.Error.StatusCode != 403 || envelope.Error.Reason != "1007" {
		t.Fatalf("envelope = %#v, want operator API error details", envelope)
	}
}
