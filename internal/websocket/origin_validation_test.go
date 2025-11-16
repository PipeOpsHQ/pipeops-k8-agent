package websocket

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOriginValidation(t *testing.T) {
	tests := []struct {
		name           string
		allowedOrigins []string
		requestOrigin  string
		shouldAllow    bool
	}{
		{
			name:           "no origins configured - allow all",
			allowedOrigins: []string{},
			requestOrigin:  "http://example.com",
			shouldAllow:    true,
		},
		{
			name:           "no origin header - deny if origins configured",
			allowedOrigins: []string{"http://localhost:3000"},
			requestOrigin:  "",
			shouldAllow:    false,
		},
		{
			name:           "exact match - allow",
			allowedOrigins: []string{"http://localhost:3000"},
			requestOrigin:  "http://localhost:3000",
			shouldAllow:    true,
		},
		{
			name:           "wildcard - allow all",
			allowedOrigins: []string{"*"},
			requestOrigin:  "http://example.com",
			shouldAllow:    true,
		},
		{
			name:           "not in list - deny",
			allowedOrigins: []string{"http://localhost:3000"},
			requestOrigin:  "http://malicious.com",
			shouldAllow:    false,
		},
		{
			name:           "multiple origins - match second",
			allowedOrigins: []string{"http://localhost:3000", "https://dashboard.example.com"},
			requestOrigin:  "https://dashboard.example.com",
			shouldAllow:    true,
		},
		{
			name:           "multiple origins - no match",
			allowedOrigins: []string{"http://localhost:3000", "https://dashboard.example.com"},
			requestOrigin:  "http://evil.com",
			shouldAllow:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				AllowedOrigins: tt.allowedOrigins,
			}

			// Create mock request
			req := httptest.NewRequest("GET", "/ws", nil)
			if tt.requestOrigin != "" {
				req.Header.Set("Origin", tt.requestOrigin)
			}

			// Test via config method
			result := cfg.IsOriginAllowed(tt.requestOrigin)
			assert.Equal(t, tt.shouldAllow, result)
		})
	}
}

func TestOriginValidationEdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		allowedOrigins []string
		requestOrigin  string
		shouldAllow    bool
	}{
		{
			name:           "origin with trailing slash",
			allowedOrigins: []string{"http://localhost:3000"},
			requestOrigin:  "http://localhost:3000/",
			shouldAllow:    false, // exact match required
		},
		{
			name:           "origin with different port",
			allowedOrigins: []string{"http://localhost:3000"},
			requestOrigin:  "http://localhost:8080",
			shouldAllow:    false,
		},
		{
			name:           "origin with https vs http",
			allowedOrigins: []string{"http://localhost:3000"},
			requestOrigin:  "https://localhost:3000",
			shouldAllow:    false,
		},
		{
			name:           "whitespace in origin list",
			allowedOrigins: []string{" http://localhost:3000 ", "https://example.com"},
			requestOrigin:  "http://localhost:3000",
			shouldAllow:    true, // config loader trims whitespace
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the LoadFromEnv trimming
			trimmed := make([]string, len(tt.allowedOrigins))
			for i, origin := range tt.allowedOrigins {
				trimmed[i] = strings.TrimSpace(origin)
			}

			cfg := &Config{
				AllowedOrigins: trimmed,
			}

			result := cfg.IsOriginAllowed(tt.requestOrigin)
			assert.Equal(t, tt.shouldAllow, result)
		})
	}
}

func TestOriginValidationCaseSensitivity(t *testing.T) {
	cfg := &Config{
		AllowedOrigins: []string{"http://Example.COM"},
	}

	// Origin comparison should be case-sensitive per spec
	assert.False(t, cfg.IsOriginAllowed("http://example.com"))
	assert.True(t, cfg.IsOriginAllowed("http://Example.COM"))
}
