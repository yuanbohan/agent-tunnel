package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/buildinfo"
)

var buildinfoTestMu sync.Mutex

func TestVersion(t *testing.T) {
	buildinfoTestMu.Lock()
	t.Cleanup(buildinfoTestMu.Unlock)

	// Set mock build info
	oldVersion := buildinfo.Version
	oldBranch := buildinfo.GitBranch
	oldCommit := buildinfo.GitCommit
	oldTime := buildinfo.BuildTime

	buildinfo.Version = "v1.2.3"
	buildinfo.GitBranch = "main"
	buildinfo.GitCommit = "abcdef123456"
	buildinfo.BuildTime = "2026-04-19T10:00:00Z" // 1776592800 in Unix seconds

	t.Cleanup(func() {
		buildinfo.Version = oldVersion
		buildinfo.GitBranch = oldBranch
		buildinfo.GitCommit = oldCommit
		buildinfo.BuildTime = oldTime
	})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	Version()(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var envelope struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Body    map[string]any `json:"body"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	resp := envelope.Body
	if resp["version"] != "v1.2.3" {
		t.Errorf("version = %v, want %q", resp["version"], "v1.2.3")
	}
	if resp["branch"] != "main" {
		t.Errorf("branch = %v, want %q", resp["branch"], "main")
	}
	if resp["commit"] != "abcdef123456" {
		t.Errorf("commit = %v, want %q", resp["commit"], "abcdef123456")
	}
	// buildTime is now a number in JSON, which unmarshals to float64 in map[string]any
	if got, ok := resp["buildTime"].(float64); !ok || int64(got) != 1776592800 {
		t.Errorf("buildTime = %v, want %d", resp["buildTime"], 1776592800)
	}
}
