package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func Healthz() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write([]byte("ok"))
	}
}
