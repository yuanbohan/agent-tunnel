package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/buildinfo"
)

// Version returns a Gin handler that responds with the application build information.
func Version() gin.HandlerFunc {
	return func(c *gin.Context) {
		WriteJSON(c.Writer, http.StatusOK, map[string]string{
			"version":   buildinfo.Version,
			"branch":    buildinfo.GitBranch,
			"commit":    buildinfo.GitCommit,
			"buildTime": buildinfo.BuildTime,
		})
	}
}
