package agent

import (
	"testing"
)

func TestValidateServiceName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{"valid service name", "my-service", false},
		{"valid with numbers", "service-123", false},
		{"empty name", "", true},
		{"uppercase letters", "MyService", true},
		{"starts with hyphen", "-service", true},
		{"ends with hyphen", "service-", true},
		{"contains underscore", "my_service", true},
		{"contains dots", "my.service", true},
		{"too long", string(make([]byte, 254)), true},
		{"special characters", "service@123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServiceName(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("validateServiceName(%q) error = %v, wantError %v", tt.input, err, tt.wantError)
			}
		})
	}
}

func TestValidateNamespace(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{"valid namespace", "default", false},
		{"valid with hyphens", "my-namespace", false},
		{"valid with numbers", "namespace-123", false},
		{"empty namespace", "", true},
		{"uppercase letters", "MyNamespace", true},
		{"starts with hyphen", "-namespace", true},
		{"ends with hyphen", "namespace-", true},
		{"contains underscore", "my_namespace", true},
		{"too long", string(make([]byte, 254)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNamespace(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("validateNamespace(%q) error = %v, wantError %v", tt.input, err, tt.wantError)
			}
		})
	}
}

func TestValidateServicePort(t *testing.T) {
	tests := []struct {
		name      string
		input     int32
		wantError bool
	}{
		{"valid port 80", 80, false},
		{"valid port 443", 443, false},
		{"valid port 8080", 8080, false},
		{"minimum valid port", 1, false},
		{"maximum valid port", 65535, false},
		{"port 0", 0, true},
		{"negative port", -1, true},
		{"port too high", 65536, true},
		{"port too high 2", 70000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServicePort(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("validateServicePort(%d) error = %v, wantError %v", tt.input, err, tt.wantError)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{"valid path /", "/", false},
		{"valid path /api", "/api", false},
		{"valid path /api/v1/users", "/api/v1/users", false},
		{"empty path", "", false}, // Empty path is allowed
		{"path without leading slash", "api", true},
		{"path with ..", "/api/../etc", true},
		{"path with .. at start", "/../etc/passwd", true},
		{"path with protocol", "/api://malicious", true},
		{"path with http://", "/test/http://evil.com", true},
		{"path with https://", "https://evil.com/test", true},
		{"valid path with query-like string", "/api?param=value", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("validatePath(%q) error = %v, wantError %v", tt.input, err, tt.wantError)
			}
		})
	}
}
