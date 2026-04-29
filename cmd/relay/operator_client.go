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

type httpOperatorClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type operatorClient interface {
	CreateInvites(ctx context.Context, count int, expiresInDays int) ([]string, error)
	DisableInvite(ctx context.Context, code string) error
	DeleteUser(ctx context.Context, username string) error
	ListInvites(ctx context.Context) ([]handlertypes.OperatorInviteCodeListEntry, error)
	SetUserTier(ctx context.Context, username string, tier string) (handlertypes.OperatorSetUserTierResponse, error)
}

type operatorAPIError struct {
	StatusCode int
	Code       int
	Message    string
	Reason     string
}

type operatorAPIResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Body    json.RawMessage `json:"body"`
}

const defaultOperatorHTTPTimeout = 10 * time.Second

func (e operatorAPIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Reason != "" {
		return e.Reason
	}
	if e.Code != 0 {
		return fmt.Sprintf("operator API returned status %d with code %d", e.StatusCode, e.Code)
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
	}, http.StatusOK, nil)
}

func (c *httpOperatorClient) DeleteUser(ctx context.Context, username string) error {
	return c.doJSON(ctx, http.MethodPost, handlertypes.OperatorDeleteUserPath, handlertypes.OperatorDeleteUserRequest{
		Username: username,
	}, http.StatusOK, nil)
}

func (c *httpOperatorClient) ListInvites(ctx context.Context) ([]handlertypes.OperatorInviteCodeListEntry, error) {
	var resp handlertypes.OperatorInviteCodesListResponse
	if err := c.doJSON(ctx, http.MethodPost, handlertypes.OperatorInviteListPath, nil, http.StatusOK, &resp); err != nil {
		return nil, err
	}
	return resp.Invites, nil
}

func (c *httpOperatorClient) SetUserTier(ctx context.Context, username string, tier string) (handlertypes.OperatorSetUserTierResponse, error) {
	var resp handlertypes.OperatorSetUserTierResponse
	if err := c.doJSON(ctx, http.MethodPost, handlertypes.OperatorUserTierPath, handlertypes.OperatorSetUserTierRequest{
		Username: username,
		Tier:     tier,
	}, http.StatusOK, &resp); err != nil {
		return handlertypes.OperatorSetUserTierResponse{}, err
	}
	return resp, nil
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

	responsePayload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != wantStatus {
		return parseOperatorAPIError(resp.StatusCode, responsePayload)
	}
	return parseOperatorAPISuccess(responsePayload, responseBody)
}

func parseOperatorAPISuccess(responsePayload []byte, responseBody any) error {
	trimmed := strings.TrimSpace(string(responsePayload))
	if trimmed == "" {
		return fmt.Errorf("operator API returned empty success body")
	}

	var envelope operatorAPIResponse
	if err := json.Unmarshal(responsePayload, &envelope); err != nil {
		return fmt.Errorf("operator API returned invalid envelope success body: %w", err)
	}
	if envelope.Code != 0 {
		return operatorAPIError{
			StatusCode: http.StatusOK,
			Code:       envelope.Code,
			Message:    envelope.Message,
		}
	}
	if envelope.Message != "success" {
		return fmt.Errorf("operator API returned invalid envelope success body: unexpected success message %q", envelope.Message)
	}
	if responseBody == nil {
		if !isNullJSON(envelope.Body) {
			return fmt.Errorf("operator API returned success response with non-null body")
		}
		return nil
	}
	if len(strings.TrimSpace(string(envelope.Body))) == 0 || envelope.Body == nil {
		return fmt.Errorf("operator API returned success response without body")
	}
	if isNullJSON(envelope.Body) {
		return fmt.Errorf("operator API returned success response with null body")
	}
	return json.Unmarshal(envelope.Body, responseBody)
}

func parseOperatorAPIError(statusCode int, responsePayload []byte) error {
	trimmed := strings.TrimSpace(string(responsePayload))
	if trimmed == "" {
		return operatorAPIError{StatusCode: statusCode}
	}

	var envelope operatorAPIResponse
	if err := json.Unmarshal(responsePayload, &envelope); err != nil {
		return fmt.Errorf("operator API returned invalid envelope error body for status %d: %w", statusCode, err)
	}
	if envelope.Code == 0 || strings.TrimSpace(envelope.Message) == "" || !isNullJSON(envelope.Body) {
		return fmt.Errorf("operator API returned invalid envelope error body for status %d", statusCode)
	}
	return operatorAPIError{
		StatusCode: statusCode,
		Code:       envelope.Code,
		Message:    envelope.Message,
	}
}

func isNullJSON(payload json.RawMessage) bool {
	return strings.TrimSpace(string(payload)) == "null"
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
