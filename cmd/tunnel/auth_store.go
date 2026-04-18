package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	authSchemaVersion = 1
)

var (
	errStoredAuthNotFound = errors.New("stored auth not found")
	userHomeDir           = os.UserHomeDir
	osEnv                 = os.Getenv
)

type storedAuth struct {
	Version        int    `json:"version"`
	Username       string `json:"username"`
	TokenName      string `json:"token_name"`
	Token          string `json:"token"`
	TokenID        string `json:"token_id,omitempty"`
	TokenCreatedAt int64  `json:"token_created_at"`
	StoredAt       int64  `json:"stored_at"`
}

type authStore interface {
	AuthFilePath() (string, error)
	Load() (storedAuth, error)
	Save(storedAuth) error
	Clear() error
}

type fileAuthStore struct{}

func defaultAuthStore() authStore {
	return fileAuthStore{}
}

func (fileAuthStore) AuthFilePath() (string, error) {
	paths, err := resolveLocalStatePaths()
	if err != nil {
		return "", err
	}
	return paths.AuthFile, nil
}

func (store fileAuthStore) Load() (storedAuth, error) {
	path, err := store.AuthFilePath()
	if err != nil {
		return storedAuth{}, err
	}
	if err := rejectSymlinkPath(path, "auth store"); err != nil {
		return storedAuth{}, err
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return storedAuth{}, errStoredAuthNotFound
		}
		return storedAuth{}, fmt.Errorf("read auth store %s: %w", path, err)
	}

	var out storedAuth
	if err := json.Unmarshal(payload, &out); err != nil {
		return storedAuth{}, fmt.Errorf("parse auth store %s: %w", path, err)
	}
	if strings.TrimSpace(out.Token) == "" {
		return storedAuth{}, fmt.Errorf("parse auth store %s: missing token", path)
	}
	if out.Version == 0 {
		out.Version = authSchemaVersion
	}
	return out, nil
}

func (store fileAuthStore) Save(record storedAuth) error {
	path, err := store.AuthFilePath()
	if err != nil {
		return err
	}
	record.Version = authSchemaVersion
	if err := writePrivateJSONFile(path, record); err != nil {
		return fmt.Errorf("save auth store %s: %w", path, err)
	}
	return nil
}

func (store fileAuthStore) Clear() error {
	path, err := store.AuthFilePath()
	if err != nil {
		return err
	}
	if err := rejectSymlinkPath(path, "auth store"); err != nil {
		return err
	}
	err = os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove auth store %s: %w", path, err)
}

type authSource string

const (
	authSourceNone authSource = "none"
	authSourceEnv  authSource = "env"
	authSourceFile authSource = "file"
)

type resolvedAuth struct {
	Source authSource
	Token  string
	File   *storedAuth
}

func resolveRuntimeAuth(store authStore, getenv func(string) string) (resolvedAuth, error) {
	token := strings.TrimSpace(getenv(tunnelAuthTokenEnv))
	if token != "" {
		return resolvedAuth{Source: authSourceEnv, Token: token}, nil
	}

	record, err := store.Load()
	if err != nil {
		if errors.Is(err, errStoredAuthNotFound) {
			return resolvedAuth{}, fmt.Errorf("no auth token available; set %s or run `tunnel auth login`", tunnelAuthTokenEnv)
		}
		return resolvedAuth{}, err
	}
	return resolvedAuth{
		Source: authSourceFile,
		Token:  record.Token,
		File:   &record,
	}, nil
}

type authStatusOutput struct {
	LoggedIn     bool              `json:"logged_in"`
	ActiveSource authSource        `json:"active_source"`
	Sources      authStatusSources `json:"sources"`
}

type authStatusSources struct {
	Env  authEnvStatus  `json:"env"`
	File authFileStatus `json:"file"`
}

type authEnvStatus struct {
	Available bool `json:"available"`
	Active    bool `json:"active"`
}

type authFileStatus struct {
	Available      bool   `json:"available"`
	Active         bool   `json:"active"`
	Shadowed       bool   `json:"shadowed"`
	Path           string `json:"path,omitempty"`
	Username       string `json:"username,omitempty"`
	TokenName      string `json:"token_name,omitempty"`
	TokenCreatedAt int64  `json:"token_created_at,omitempty"`
	StoredAt       int64  `json:"stored_at,omitempty"`
}

func buildAuthStatus(store authStore, getenv func(string) string) (authStatusOutput, error) {
	authPath, err := store.AuthFilePath()
	if err != nil {
		return authStatusOutput{}, err
	}

	output := authStatusOutput{
		ActiveSource: authSourceNone,
		Sources: authStatusSources{
			File: authFileStatus{Path: authPath},
		},
	}

	envToken := strings.TrimSpace(getenv(tunnelAuthTokenEnv))
	if envToken != "" {
		output.Sources.Env.Available = true
		output.Sources.Env.Active = true
		output.ActiveSource = authSourceEnv
		output.LoggedIn = true
	}

	record, err := store.Load()
	if err != nil && !errors.Is(err, errStoredAuthNotFound) {
		return authStatusOutput{}, err
	}
	if err == nil {
		output.Sources.File.Available = true
		output.Sources.File.Username = record.Username
		output.Sources.File.TokenName = record.TokenName
		output.Sources.File.TokenCreatedAt = record.TokenCreatedAt
		output.Sources.File.StoredAt = record.StoredAt
		if output.ActiveSource == authSourceNone {
			output.ActiveSource = authSourceFile
			output.Sources.File.Active = true
			output.LoggedIn = true
		} else {
			output.Sources.File.Shadowed = true
		}
	}

	return output, nil
}

func newStoredAuth(username, tokenName, tokenID, token string, tokenCreatedAt int64, now time.Time) storedAuth {
	return storedAuth{
		Version:        authSchemaVersion,
		Username:       username,
		TokenName:      tokenName,
		Token:          token,
		TokenID:        tokenID,
		TokenCreatedAt: tokenCreatedAt,
		StoredAt:       now.UTC().Unix(),
	}
}
