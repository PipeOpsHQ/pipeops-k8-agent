package k8s

import (
	"fmt"
	"os"
	"strings"
)

const (
	// ServiceAccountTokenPath is the standard Kubernetes ServiceAccount token mount path
	ServiceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

// GetServiceAccountToken reads the Kubernetes ServiceAccount token from the standard
// mounted location. This token is automatically created by Kubernetes when a pod uses
// a ServiceAccount, and is used to authenticate with the K8s API server.
//
// The token is needed by the control plane to access the cluster through the tunnel.
func GetServiceAccountToken() (string, error) {
	tokenBytes, err := os.ReadFile(ServiceAccountTokenPath)
	if err != nil {
		return "", fmt.Errorf("failed to read ServiceAccount token: %w", err)
	}

	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return "", fmt.Errorf("ServiceAccount token is empty")
	}

	return token, nil
}

// HasServiceAccountToken checks if a ServiceAccount token is available
// (useful for determining if running inside a Kubernetes pod)
func HasServiceAccountToken() bool {
	_, err := os.Stat(ServiceAccountTokenPath)
	return err == nil
}
