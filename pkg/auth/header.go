package auth

import (
	"fmt"
	"net/http"
	"strings"
)

// BearerHeader returns Authorization: Bearer <token>.
// Prefer this over putting tokens in URL query strings (?token=).
// The control plane accepts query tokens for legacy agents; new code must use Bearer.
func BearerHeader(token string) http.Header {
	h := make(http.Header)
	SetBearer(h, token)
	return h
}

// SetBearer sets Authorization: Bearer on h.
func SetBearer(h http.Header, token string) {
	if h == nil {
		return
	}
	t := strings.TrimSpace(token)
	if t == "" {
		return
	}
	t = strings.TrimSpace(strings.TrimPrefix(t, "Bearer "))
	t = strings.TrimSpace(strings.TrimPrefix(t, "bearer "))
	h.Set("Authorization", fmt.Sprintf("Bearer %s", t))
}
