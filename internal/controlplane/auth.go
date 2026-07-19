package controlplane

import (
	"net/http"

	"github.com/pipeops/pipeops-vm-agent/pkg/auth"
)

// AuthHeader builds Authorization: Bearer for control-plane / gateway dials.
// Prefer Bearer; never put the agent token in ?token= query strings.
func AuthHeader(token string) http.Header {
	return auth.BearerHeader(token)
}

// ApplyAuthHeader sets Authorization: Bearer on h.
func ApplyAuthHeader(h http.Header, token string) {
	auth.SetBearer(h, token)
}
