package k8s

import (
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
)

const (
	// ServiceAccountTokenPath is the standard Kubernetes ServiceAccount token mount path
	ServiceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	// ServiceAccountCAPath is the standard Kubernetes ServiceAccount CA certificate path
	ServiceAccountCAPath = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
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

// GetServiceAccountCACertData reads the ServiceAccount CA certificate, validates it,
// and returns it base64 encoded.
func GetServiceAccountCACertData() (string, error) {
	caBytes, err := os.ReadFile(ServiceAccountCAPath)
	if err != nil {
		return "", fmt.Errorf("failed to read ServiceAccount CA certificate: %w", err)
	}
	if len(caBytes) == 0 {
		return "", fmt.Errorf("ServiceAccount CA certificate is empty")
	}

	// Validate that the certificate is in valid PEM format
	block, _ := pem.Decode(caBytes)
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block from CA certificate")
	}
	if block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("invalid CA certificate type: expected CERTIFICATE, got %s", block.Type)
	}

	encoded := base64.StdEncoding.EncodeToString(caBytes)
	if encoded == "" {
		return "", fmt.Errorf("failed to encode ServiceAccount CA certificate")
	}

	return encoded, nil
}
