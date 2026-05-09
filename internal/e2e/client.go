package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/relay/handler/response"
	handlertypes "yuanbohan/tunnel/internal/relay/handler/types"
)

type AppClient struct {
	baseURL string
	wsURL   string
	http    *http.Client
	dialer  *websocket.Dialer
}

type RegisterResponse struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
}

type APIEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Body    json.RawMessage `json:"body"`
}

func newAppClient(baseURL string) *AppClient {
	return &AppClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		wsURL:   "ws" + strings.TrimPrefix(strings.TrimRight(baseURL, "/"), "http"),
		http:    &http.Client{Timeout: 5 * time.Second},
		dialer:  websocket.DefaultDialer,
	}
}

func (c *AppClient) Register(inviteCode, username, password string) (RegisterResponse, error) {
	var out RegisterResponse
	err := c.doJSON(http.MethodPost, "/api/auth/register", "", map[string]string{
		"invite_code": inviteCode,
		"username":    username,
		"password":    password,
	}, http.StatusCreated, &out)
	return out, err
}

func (c *AppClient) Login(username, password string) (handlertypes.AppSessionResponse, error) {
	return c.LoginWithClientFingerprint(username, password, "")
}

func (c *AppClient) LoginWithDeviceFingerprint(username, password, deviceFingerprint string) (handlertypes.AppSessionResponse, error) {
	return c.loginWithFingerprint(username, password, "device_fingerprint", deviceFingerprint)
}

func (c *AppClient) LoginWithClientFingerprint(username, password, clientFingerprint string) (handlertypes.AppSessionResponse, error) {
	return c.loginWithFingerprint(username, password, "client_fingerprint", clientFingerprint)
}

func (c *AppClient) loginWithFingerprint(username, password, key, fingerprint string) (handlertypes.AppSessionResponse, error) {
	var out handlertypes.AppSessionResponse
	req := map[string]string{
		"username": username,
		"password": password,
	}
	if strings.TrimSpace(fingerprint) != "" {
		req[key] = fingerprint
	}
	err := c.doJSON(http.MethodPost, "/api/auth/login", "", req, http.StatusOK, &out)
	return out, err
}

func (c *AppClient) RefreshWithDeviceFingerprint(refreshToken, deviceFingerprint string) (handlertypes.AppSessionResponse, error) {
	var out handlertypes.AppSessionResponse
	req := map[string]string{
		"refresh_token": refreshToken,
	}
	if strings.TrimSpace(deviceFingerprint) != "" {
		req["device_fingerprint"] = deviceFingerprint
	}
	err := c.doJSON(http.MethodPost, "/api/auth/refresh", "", req, http.StatusOK, &out)
	return out, err
}

func (c *AppClient) AccountPolicy(accessToken string) (handlertypes.AccountPolicyResponse, error) {
	var out handlertypes.AccountPolicyResponse
	err := c.doJSON(http.MethodGet, "/api/account/policy", accessToken, nil, http.StatusOK, &out)
	return out, err
}

func (c *AppClient) CreateAgentToken(accessToken, name string) (handlertypes.CreatedAgentTokenResponse, error) {
	var out handlertypes.CreatedAgentTokenResponse
	err := c.doJSON(http.MethodPost, "/api/agent-tokens", accessToken, map[string]string{
		"name": name,
	}, http.StatusCreated, &out)
	return out, err
}

func (c *AppClient) SubmitPairingResponse(accessToken string, response protocol.ConnectivityPairingResponse) error {
	var out map[string]string
	if err := c.doJSON(http.MethodPost, "/api/pairing/responses", accessToken, response, http.StatusOK, &out); err != nil {
		return err
	}
	if out["status"] != "forwarded" {
		return fmt.Errorf("POST /api/pairing/responses returned status %q, want forwarded", out["status"])
	}
	return nil
}

func (c *AppClient) ListSessions(accessToken string) ([]protocol.SessionInfo, error) {
	var out []protocol.SessionInfo
	err := c.doJSON(http.MethodGet, "/api/sessions", accessToken, nil, http.StatusOK, &out)
	return out, err
}

func (c *AppClient) GetSessionsStatus(accessToken string) (int, APIEnvelope, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/sessions", nil)
	if err != nil {
		return 0, APIEnvelope{}, err
	}
	if strings.TrimSpace(accessToken) != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, APIEnvelope{}, err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, APIEnvelope{}, err
	}
	var envelope APIEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return 0, APIEnvelope{}, fmt.Errorf("GET /api/sessions returned status %d and invalid envelope response: %w", resp.StatusCode, err)
	}
	return resp.StatusCode, envelope, nil
}

func (c *AppClient) ChangePassword(accessToken, currentPassword, newPassword string) error {
	return c.doJSON(http.MethodPost, "/api/auth/password/change", accessToken, map[string]string{
		"current_password": currentPassword,
		"new_password":     newPassword,
	}, http.StatusOK, nil)
}

func (c *AppClient) Attach(accessToken, sessionID string) (*websocket.Conn, error) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+accessToken)
	conn, resp, err := c.dialer.Dial(c.wsURL+"/api/sessions/"+sessionID+"/attach/ws", headers)
	if err != nil {
		if resp == nil {
			return nil, err
		}
		return nil, fmt.Errorf("attach websocket status %d: %w", resp.StatusCode, err)
	}
	return conn, nil
}

func (c *AppClient) doJSON(method, path, accessToken string, requestBody any, wantStatus int, responseBody any) error {
	var bodyReader *bytes.Reader
	if requestBody == nil {
		bodyReader = bytes.NewReader(nil)
	} else {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(accessToken) != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Body    json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("%s %s returned status %d and invalid envelope response: %w", method, path, resp.StatusCode, err)
	}
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("%s %s returned status %d code %d: %s", method, path, resp.StatusCode, envelope.Code, envelope.Message)
	}
	if envelope.Code != response.CodeSuccess {
		return fmt.Errorf("%s %s returned error code %d: %s", method, path, envelope.Code, envelope.Message)
	}
	if responseBody == nil {
		if strings.TrimSpace(string(envelope.Body)) != "null" {
			return fmt.Errorf("%s %s returned success response with non-null body", method, path)
		}
		return nil
	}
	if len(envelope.Body) == 0 {
		return fmt.Errorf("%s %s returned status %d with empty body", method, path, resp.StatusCode)
	}
	if err := json.Unmarshal(envelope.Body, responseBody); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}
