package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	handlertypes "yuanbohan/tunnel/internal/relay/handler/types"
)

type httpOperatorClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type operatorClient interface {
	CreateInvites(ctx context.Context, count int, expiresInDays int) ([]string, error)
	DisableInvite(ctx context.Context, code string) error
	DeleteUser(ctx context.Context, username string) error
}

type operatorAPIError struct {
	StatusCode int
	Reason     string
}

const defaultOperatorHTTPTimeout = 10 * time.Second

func (e operatorAPIError) Error() string {
	if e.Reason != "" {
		return e.Reason
	}
	return fmt.Sprintf("operator API returned status %d", e.StatusCode)
}

func newHTTPOperatorClient(relayAddr, token string, client *http.Client) *httpOperatorClient {
	if client == nil {
		client = &http.Client{Timeout: defaultOperatorHTTPTimeout}
	}
	return &httpOperatorClient{
		baseURL: operatorBaseURL(relayAddr),
		token:   token,
		client:  client,
	}
}

func (c *httpOperatorClient) CreateInvites(ctx context.Context, count int, expiresInDays int) ([]string, error) {
	var resp handlertypes.OperatorCreateInvitesResponse
	if err := c.doJSON(ctx, http.MethodPost, handlertypes.OperatorInviteCodesPath, handlertypes.OperatorCreateInvitesRequest{
		Count:         count,
		ExpiresInDays: expiresInDays,
	}, http.StatusCreated, &resp); err != nil {
		return nil, err
	}
	return resp.Codes, nil
}

func (c *httpOperatorClient) DisableInvite(ctx context.Context, code string) error {
	return c.doJSON(ctx, http.MethodPost, handlertypes.OperatorInviteDisablePath, handlertypes.OperatorDisableInviteRequest{
		Code: code,
	}, http.StatusNoContent, nil)
}

func (c *httpOperatorClient) DeleteUser(ctx context.Context, username string) error {
	return c.doJSON(ctx, http.MethodPost, handlertypes.OperatorDeleteUserPath, handlertypes.OperatorDeleteUserRequest{
		Username: username,
	}, http.StatusNoContent, nil)
}

func (c *httpOperatorClient) doJSON(ctx context.Context, method, path string, requestBody any, wantStatus int, responseBody any) error {
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != wantStatus {
		var errorBody struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errorBody); err == nil && strings.TrimSpace(errorBody.Reason) != "" {
			return operatorAPIError{StatusCode: resp.StatusCode, Reason: errorBody.Reason}
		}
		return operatorAPIError{StatusCode: resp.StatusCode}
	}
	if responseBody == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(responseBody)
}

func operatorBaseURL(relayAddr string) string {
	trimmed := strings.TrimSpace(relayAddr)
	if strings.Contains(trimmed, "://") {
		return strings.TrimRight(trimmed, "/")
	}
	switch {
	case strings.HasPrefix(trimmed, "0.0.0.0:"):
		trimmed = "127.0.0.1:" + strings.TrimPrefix(trimmed, "0.0.0.0:")
	case strings.HasPrefix(trimmed, ":"):
		trimmed = "127.0.0.1" + trimmed
	}
	return "http://" + trimmed
}
