package agent

import "testing"

func TestBuildPublicIPIntelURL(t *testing.T) {
	tests := []struct {
		name      string
		apiURL    string
		ip        string
		want      string
		shouldErr bool
	}{
		{
			name:   "https base url",
			apiURL: "https://api.pipeops.sh",
			ip:     "197.253.58.228",
			want:   "https://api.pipeops.sh/api/v1/public/ip-intel/197.253.58.228",
		},
		{
			name:   "ws scheme converts to http",
			apiURL: "ws://localhost:8080",
			ip:     "10.0.0.1",
			want:   "http://localhost:8080/api/v1/public/ip-intel/10.0.0.1",
		},
		{
			name:   "base path is preserved",
			apiURL: "https://example.com/control",
			ip:     "2001:db8::1",
			want:   "https://example.com/control/api/v1/public/ip-intel/2001:db8::1",
		},
		{
			name:      "empty url",
			apiURL:    "",
			ip:        "1.1.1.1",
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildPublicIPIntelURL(tt.apiURL, tt.ip)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("expected error, got nil (url=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestParsePublicIPIntelResponse(t *testing.T) {
	t.Run("wrapped response", func(t *testing.T) {
		raw := []byte(`{
			"success": true,
			"data": {
				"ip": "197.253.58.228",
				"country_code": "ng",
				"region": "Lagos",
				"cloud_provider": "onpremise"
			}
		}`)

		data, err := parsePublicIPIntelResponse(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if data.CountryCode != "NG" {
			t.Fatalf("expected country code NG, got %q", data.CountryCode)
		}
		if data.CloudProvider != "bare-metal" {
			t.Fatalf("expected provider bare-metal, got %q", data.CloudProvider)
		}
	})

	t.Run("direct response", func(t *testing.T) {
		raw := []byte(`{
			"ip": "197.253.58.228",
			"country_code": "NG",
			"region": "Lagos",
			"cloud_provider": "aws"
		}`)

		data, err := parsePublicIPIntelResponse(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if data.CloudProvider != "aws" {
			t.Fatalf("expected provider aws, got %q", data.CloudProvider)
		}
	})

	t.Run("invalid response", func(t *testing.T) {
		raw := []byte(`{"success":true,"data":{}}`)
		_, err := parsePublicIPIntelResponse(raw)
		if err == nil {
			t.Fatal("expected parse error for empty payload")
		}
	})
}

func TestNormalizeCloudProvider(t *testing.T) {
	tests := map[string]string{
		"onpremise":    "bare-metal",
		"on-premises":  "bare-metal",
		"baremetal":    "bare-metal",
		"  Azure  ":    "azure",
		"GOOGLE_CLOUD": "google-cloud",
		"unknown":      "",
		"agent":        "",
		"":             "",
	}

	for input, expected := range tests {
		got := normalizeCloudProvider(input)
		if got != expected {
			t.Fatalf("normalizeCloudProvider(%q): expected %q, got %q", input, expected, got)
		}
	}
}
