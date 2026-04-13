package api

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

func Healthz() gin.HandlerFunc {
	return func(c *gin.Context) {
		WriteJSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"})
	}
}
