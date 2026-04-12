package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
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
	var out handlertypes.AppSessionResponse
	err := c.doJSON(http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": username,
		"password": password,
	}, http.StatusOK, &out)
	return out, err
}

func (c *AppClient) CreateAgentToken(accessToken, name string) (handlertypes.CreatedAgentTokenResponse, error) {
	var out handlertypes.CreatedAgentTokenResponse
	err := c.doJSON(http.MethodPost, "/api/agent-tokens", accessToken, map[string]string{
		"name": name,
	}, http.StatusCreated, &out)
	return out, err
}

func (c *AppClient) ListSessions(accessToken string) ([]protocol.SessionInfo, error) {
	var out []protocol.SessionInfo
	err := c.doJSON(http.MethodGet, "/api/sessions", accessToken, nil, http.StatusOK, &out)
	return out, err
}

func (c *AppClient) GetSessionsStatus(accessToken string) (int, string, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/sessions", nil)
	if err != nil {
		return 0, "", err
	}
	if strings.TrimSpace(accessToken) != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		return 0, "", err
	}
	return resp.StatusCode, body.String(), nil
}

func (c *AppClient) ChangePassword(accessToken, currentPassword, newPassword string) error {
	return c.doJSON(http.MethodPost, "/api/auth/password/change", accessToken, map[string]string{
		"current_password": currentPassword,
		"new_password":     newPassword,
	}, http.StatusNoContent, nil)
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

	if resp.StatusCode != wantStatus {
		var body bytes.Buffer
		if _, readErr := body.ReadFrom(resp.Body); readErr != nil {
			return fmt.Errorf("%s %s returned status %d and body read failed: %v", method, path, resp.StatusCode, readErr)
		}
		return fmt.Errorf("%s %s returned status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(body.String()))
	}

	if responseBody == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(responseBody)
}
