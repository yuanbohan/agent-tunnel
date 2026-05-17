package httpx

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
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
		parts := strings.Split(forwarded, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			if candidate := strings.TrimSpace(parts[i]); candidate != "" {
				return candidate
			}
		}
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
	redacted := *r.URL
	redacted.RawQuery = RedactSensitiveQuery(redacted.Query()).Encode()
	return redacted.RequestURI()
}

func RedactSensitiveQuery(values url.Values) url.Values {
	if len(values) == 0 {
		return values
	}
	redacted := make(url.Values, len(values))
	for key, value := range values {
		copied := append([]string(nil), value...)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "token", "access_token", "refresh_token":
			for i := range copied {
				copied[i] = "<redacted>"
			}
		}
		redacted[key] = copied
	}
	return redacted
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
