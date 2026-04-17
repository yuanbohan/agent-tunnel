package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type stubPrompter struct {
	username string
	password string
	err      error
}

func (p stubPrompter) Prompt(io.Reader, io.Writer) (string, string, error) {
	return p.username, p.password, p.err
}

type fakeStore struct {
	authPath string
	record   storedAuth
	loadErr  error
	saved    *storedAuth
	cleared  bool
}

func (s *fakeStore) AuthFilePath() (string, error) {
	return s.authPath, nil
}

func (s *fakeStore) ConfigFilePath() (string, error) {
	return strings.TrimSuffix(s.authPath, "auth.json") + "config.json", nil
}

func (s *fakeStore) Load() (storedAuth, error) {
	if s.loadErr != nil {
		return storedAuth{}, s.loadErr
	}
	return s.record, nil
}

func (s *fakeStore) Save(record storedAuth) error {
	s.record = record
	s.saved = &record
	return nil
}

func (s *fakeStore) Clear() error {
	s.cleared = true
	return nil
}

func TestRunAuthLoginStoresCreatedToken(t *testing.T) {
	store := &fakeStore{authPath: "/tmp/.tunnel/auth.json"}
	wantTokenName := "tunnel-devbox-20240405-193438"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"message":"success","body":{"access_token":"app-token","refresh_token":"refresh","expires_in":86400,"token_type":"Bearer"}}`))
		case "/api/agent-tokens":
			if got := r.Header.Get("Authorization"); got != "Bearer app-token" {
				t.Fatalf("Authorization = %q, want Bearer app-token", got)
			}
			var requestBody map[string]string
			if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if got := requestBody["name"]; got != wantTokenName {
				t.Fatalf("token creation name = %q, want %q", got, wantTokenName)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"code":0,"message":"success","body":{"id":"tok_123","name":"tunnel-devbox-20260417-120000","created_at":1712345600,"token":"agent-token"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldPrompter := credentialsPrompter
	oldNewStore := newAuthStore
	oldNow := nowFunc
	oldHost := hostNameFunc
	t.Cleanup(func() {
		credentialsPrompter = oldPrompter
		newAuthStore = oldNewStore
		nowFunc = oldNow
		hostNameFunc = oldHost
	})

	credentialsPrompter = stubPrompter{username: "alice", password: "password123"}
	newAuthStore = func() authStore { return store }
	nowFunc = func() time.Time { return time.Unix(1_712_345_678, 0).UTC() }
	hostNameFunc = func() (string, error) { return "DevBox", nil }

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runAuthLogin(context.Background(), authLoginArgs{BaseURL: server.URL}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("runAuthLogin returned error: %v", err)
	}
	if store.saved == nil {
		t.Fatal("store.Save was not called")
	}
	if store.saved.Username != "alice" {
		t.Fatalf("saved username = %q, want alice", store.saved.Username)
	}
	if store.saved.Token != "agent-token" {
		t.Fatalf("saved token = %q, want agent-token", store.saved.Token)
	}
	if store.saved.TokenName != "tunnel-devbox-20260417-120000" {
		t.Fatalf("saved token name = %q, want server-returned token name", store.saved.TokenName)
	}
	if !strings.Contains(stdout.String(), store.authPath) {
		t.Fatalf("stdout = %q, want auth path", stdout.String())
	}
}

func TestRunAuthStatusPrintsSourceAwareJSON(t *testing.T) {
	store := &fakeStore{
		authPath: "/tmp/.tunnel/auth.json",
		record: newStoredAuth(
			"alice",
			"tunnel-devbox-20240405-191438",
			"tok_123",
			"file-token",
			1_712_345_600,
			time.Unix(1_712_345_678, 0),
		),
	}

	oldNewStore := newAuthStore
	oldGetenv := osEnv
	t.Cleanup(func() {
		newAuthStore = oldNewStore
		osEnv = oldGetenv
	})

	newAuthStore = func() authStore { return store }
	osEnv = func(key string) string {
		if key == tunnelAuthTokenEnv {
			return "env-token"
		}
		return ""
	}

	var stdout bytes.Buffer
	if err := runAuthStatus(context.Background(), &stdout); err != nil {
		t.Fatalf("runAuthStatus returned error: %v", err)
	}

	var got authStatusOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal status JSON returned error: %v", err)
	}
	if got.ActiveSource != authSourceEnv {
		t.Fatalf("ActiveSource = %q, want env", got.ActiveSource)
	}
	if !got.Sources.File.Shadowed {
		t.Fatalf("file source = %#v, want shadowed true", got.Sources.File)
	}
	if got.Sources.File.Username != "alice" {
		t.Fatalf("file username = %q, want alice", got.Sources.File.Username)
	}
}

func TestRunAuthLogoutClearsStoredFile(t *testing.T) {
	store := &fakeStore{authPath: "/tmp/.tunnel/auth.json"}

	oldNewStore := newAuthStore
	t.Cleanup(func() {
		newAuthStore = oldNewStore
	})
	newAuthStore = func() authStore { return store }

	var stdout bytes.Buffer
	if err := runAuthLogout(context.Background(), &stdout); err != nil {
		t.Fatalf("runAuthLogout returned error: %v", err)
	}
	if !store.cleared {
		t.Fatal("store.Clear was not called")
	}
	if !strings.Contains(stdout.String(), store.authPath) {
		t.Fatalf("stdout = %q, want auth path", stdout.String())
	}
}

func TestTerminalCredentialPrompterRequiresInteractiveTerminal(t *testing.T) {
	_, _, err := terminalCredentialPrompter{}.Prompt(strings.NewReader("alice\n"), io.Discard)
	if err == nil {
		t.Fatal("Prompt error = nil, want interactive terminal error")
	}
	if !strings.Contains(err.Error(), "interactive terminal stdin") {
		t.Fatalf("Prompt error = %q, want interactive terminal guidance", err)
	}
}
