package relay

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func checkBasicAuth(r *http.Request, wantUser, wantPass string) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(user), []byte(wantUser)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(wantPass)) == 1
}

func checkBearer(r *http.Request, wantToken string) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}

	token := strings.TrimPrefix(auth, prefix)
	return subtle.ConstantTimeCompare([]byte(token), []byte(wantToken)) == 1
}
