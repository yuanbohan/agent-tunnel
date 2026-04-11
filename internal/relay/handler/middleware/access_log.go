package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/logx"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
)

func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/healthz" {
			c.Next()
			return
		}

		startedAt := time.Now()
		c.Next()

		fields := []logx.Field{
			logx.String("method", c.Request.Method),
			logx.String("path", c.Request.URL.Path),
			logx.String("target", httpx.RequestTarget(c.Request)),
			logx.Int("status", c.Writer.Status()),
			logx.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
		}
		fields = append(fields, httpx.RequestLogFields(c.Request)...)
		if c.Request.ContentLength >= 0 {
			fields = append(fields, logx.Int64("request_bytes", c.Request.ContentLength))
		}
		if size := c.Writer.Size(); size >= 0 {
			fields = append(fields, logx.Int64("response_bytes", int64(size)))
		}

		logx.Info("http_request_completed", fields...)
	}
}
