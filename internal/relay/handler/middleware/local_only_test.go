package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLocalOnly(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		wantStatus int
	}{
		{
			name:       "local-no-proxy",
			remoteAddr: "127.0.0.1:12345",
			wantStatus: http.StatusOK,
		},
		{
			name:       "local-ipv6-no-proxy",
			remoteAddr: "[::1]:12345",
			wantStatus: http.StatusOK,
		},
		{
			name:       "local-with-proxy-header",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "1.1.1.1"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "local-with-x-real-ip",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string]string{"X-Real-IP": "1.1.1.1"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "local-with-forwarded",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string]string{"Forwarded": "for=1.1.1.1"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "remote-ip",
			remoteAddr: "1.1.1.1:12345",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			_, r := gin.CreateTestContext(w)

			r.GET("/test", LocalOnly(), func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tc.remoteAddr
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}
		})
	}
}
