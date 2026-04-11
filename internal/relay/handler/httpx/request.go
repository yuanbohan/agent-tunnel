package httpx

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"yuanbohan/tunnel/internal/logx"
)

func BearerTokenFromRequest(r *http.Request) (string, bool) {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}

func StaticBearerAuth(r *http.Request, wantToken string) bool {
	token, ok := BearerTokenFromRequest(r)
	if !ok {
		return false
	}
	wantToken = strings.TrimSpace(wantToken)
	if wantToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(wantToken)) == 1
}

func RequestRemoteIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			forwarded = forwarded[:comma]
		}
		return strings.TrimSpace(forwarded)
	}
	return DirectRequestRemoteIP(r)
}

func DirectRequestRemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func IsLoopbackRequest(r *http.Request) bool {
	ip := net.ParseIP(DirectRequestRemoteIP(r))
	return ip != nil && ip.IsLoopback()
}

func HasForwardedProxyHeaders(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("X-Forwarded-For")) != "" ||
		strings.TrimSpace(r.Header.Get("X-Real-IP")) != "" ||
		strings.TrimSpace(r.Header.Get("Forwarded")) != ""
}

func RequestTarget(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return r.URL.RequestURI()
}

func RequestLogFields(r *http.Request) []logx.Field {
	fields := []logx.Field{
		logx.String("remote_addr", r.RemoteAddr),
		logx.String("user_agent", r.UserAgent()),
	}
	if requestID := RequestIDFromRequest(r); requestID != "" {
		fields = append(fields, logx.String("request_id", requestID))
	}
	return fields
}

func RequestIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get("X-Request-Id")
}
