package relay

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

func bearerTokenFromRequest(r *http.Request) (string, bool) {
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

func staticBearerAuth(r *http.Request, wantToken string) bool {
	token, ok := bearerTokenFromRequest(r)
	if !ok {
		return false
	}
	wantToken = strings.TrimSpace(wantToken)
	if wantToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(wantToken)) == 1
}

func requestRemoteIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			forwarded = forwarded[:comma]
		}
		return strings.TrimSpace(forwarded)
	}
	return directRequestRemoteIP(r)
}

func directRequestRemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func isLoopbackRequest(r *http.Request) bool {
	ip := net.ParseIP(directRequestRemoteIP(r))
	return ip != nil && ip.IsLoopback()
}

func hasForwardedProxyHeaders(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("X-Forwarded-For")) != "" ||
		strings.TrimSpace(r.Header.Get("X-Real-IP")) != "" ||
		strings.TrimSpace(r.Header.Get("Forwarded")) != ""
}
