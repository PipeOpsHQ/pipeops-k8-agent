package ingress

import (
	"testing"
)

func TestValidateBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{"valid https URL", "https://api.pipeops.io", false},
		{"valid http URL", "http://api.pipeops.io", false},
		{"localhost development", "http://localhost:8080", false},
		{"127.0.0.1 development", "http://127.0.0.1:8080", false},
		{"empty URL", "", true},
		{"private IP 10.x", "http://10.0.0.1", false},
		{"private IP 192.168.x", "http://192.168.1.1", false},
		{"private IP 172.16.x", "http://172.16.0.1", false},
		{"private IP 172.31.x", "http://172.31.255.255", false},
		{"link-local IP", "http://169.254.169.254", false},
		{"invalid scheme ftp", "ftp://api.example.com", true},
		{"invalid scheme file", "file:///etc/passwd", true},
		{"no scheme", "api.pipeops.io", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBaseURL(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("validateBaseURL(%q) error = %v, wantError %v", tt.input, err, tt.wantError)
			}
		})
	}
}

func TestValidateRequestURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{"valid https URL", "https://api.pipeops.io/v1/routes", false},
		{"valid http URL", "http://api.pipeops.io/v1/routes", false},
		{"localhost URL", "http://localhost:8080/api", false},
		{"empty URL", "", true},
		{"private IP", "http://192.168.1.1/api", false},
		{"metadata service AWS", "http://169.254.169.254/latest/meta-data", false},
		{"invalid scheme", "javascript:alert(1)", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequestURL(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("validateRequestURL(%q) error = %v, wantError %v", tt.input, err, tt.wantError)
			}
		})
	}
}

func TestIsPrivateOrLocalhost(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"localhost", "localhost", true},
		{"127.0.0.1", "127.0.0.1", true},
		{"127.255.255.255", "127.255.255.255", true},
		{"10.0.0.1", "10.0.0.1", true},
		{"192.168.0.1", "192.168.0.1", true},
		{"172.16.0.1", "172.16.0.1", true},
		{"172.31.255.255", "172.31.255.255", true},
		{"169.254.169.254", "169.254.169.254", true},
		{"public IP", "8.8.8.8", false},
		{"public domain", "api.pipeops.io", false},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPrivateOrLocalhost(tt.input)
			if got != tt.want {
				t.Errorf("isPrivateOrLocalhost(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
