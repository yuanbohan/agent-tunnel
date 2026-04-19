package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/buildinfo"
)

func TestVersion(t *testing.T) {
	// Set mock build info
	oldVersion := buildinfo.Version
	oldBranch := buildinfo.GitBranch
	oldCommit := buildinfo.GitCommit
	oldTime := buildinfo.BuildTime

	buildinfo.Version = "v1.2.3"
	buildinfo.GitBranch = "main"
	buildinfo.GitCommit = "abcdef123456"
	buildinfo.BuildTime = "2026-04-19T10:00:00Z"

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
		Code    int               `json:"code"`
		Message string            `json:"message"`
		Body    map[string]string `json:"body"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	resp := envelope.Body
	if resp["version"] != "v1.2.3" {
		t.Errorf("version = %q, want %q", resp["version"], "v1.2.3")
	}
	if resp["branch"] != "main" {
		t.Errorf("branch = %q, want %q", resp["branch"], "main")
	}
	if resp["commit"] != "abcdef123456" {
		t.Errorf("commit = %q, want %q", resp["commit"], "abcdef123456")
	}
	if resp["buildTime"] != "2026-04-19T10:00:00Z" {
		t.Errorf("buildTime = %q, want %q", resp["buildTime"], "2026-04-19T10:00:00Z")
	}
}
