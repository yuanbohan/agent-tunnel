package main

import (
	"context"
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
	err := runWithHandlers([]string{"serve", "--listen-addr", "127.0.0.1:9999"}, env, commandHandlers{
		serve: func(_ context.Context, cfg serveConfig) error {
			called = true
			if cfg.ListenAddr != "127.0.0.1:9999" {
				t.Fatalf("ListenAddr = %q, want 127.0.0.1:9999", cfg.ListenAddr)
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
	if !strings.Contains(err.Error(), "relay serve") {
		t.Fatalf("error = %q, want root usage", err.Error())
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
