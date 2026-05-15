package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	handlertypes "yuanbohan/tunnel/internal/relay/handler/types"
)

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type relayAuthAPI struct {
	baseURL string
	client  httpDoer
}

var newHTTPClient = func() httpDoer {
	return &http.Client{Timeout: 10 * time.Second}
}

func newRelayAuthAPI(baseURL string) *relayAuthAPI {
	return &relayAuthAPI{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  newHTTPClient(),
	}
}

type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Body    json.RawMessage `json:"body"`
}

func (api *relayAuthAPI) login(ctx context.Context, username, password string) (handlertypes.AppSessionResponse, error) {
	var out handlertypes.AppSessionResponse
	err := api.doJSON(ctx, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": username,
		"password": password,
	}, http.StatusOK, &out)
	return out, err
}

func (api *relayAuthAPI) createAgentToken(ctx context.Context, accessToken, name string) (handlertypes.CreatedAgentTokenResponse, error) {
	var out handlertypes.CreatedAgentTokenResponse
	err := api.doJSON(ctx, http.MethodPost, "/api/agent-tokens", accessToken, map[string]string{
		"name": name,
	}, http.StatusCreated, &out)
	return out, err
}

func (api *relayAuthAPI) doJSON(ctx context.Context, method, path, accessToken string, requestBody any, wantStatus int, out any) error {
	var body io.Reader
	if requestBody != nil {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, api.baseURL+path, body)
	if err != nil {
		return err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(accessToken) != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := api.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("%s %s returned status %d and invalid envelope response: %w", method, path, resp.StatusCode, err)
	}
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("%s %s returned status %d code %d: %s", method, path, resp.StatusCode, envelope.Code, envelope.Message)
	}
	if envelope.Code != 0 {
		return fmt.Errorf("%s %s returned error code %d: %s", method, path, envelope.Code, envelope.Message)
	}
	if out == nil {
		return nil
	}
	if len(envelope.Body) == 0 {
		return fmt.Errorf("%s %s returned status %d with empty body", method, path, resp.StatusCode)
	}
	if err := json.Unmarshal(envelope.Body, out); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}
