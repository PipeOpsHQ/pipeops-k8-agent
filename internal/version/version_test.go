package version

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetBuildInfo(t *testing.T) {
	info := GetBuildInfo()

	assert.NotNil(t, info)
	assert.NotEmpty(t, info.Version)
	assert.NotEmpty(t, info.GitCommit)
	assert.NotEmpty(t, info.BuildDate)
	assert.NotEmpty(t, info.GoVersion)
	assert.Equal(t, runtime.Version(), info.GoVersion)
}

func TestGetVersion(t *testing.T) {
	// Save original version
	originalVersion := Version
	defer func() { Version = originalVersion }()

	tests := []struct {
		name     string
		version  string
		expected string
	}{
		{
			name:     "dev version",
			version:  "dev",
			expected: "1.0.0-dev",
		},
		{
			name:     "production version",
			version:  "v1.2.3",
			expected: "v1.2.3",
		},
		{
			name:     "custom version",
			version:  "2.0.0-beta",
			expected: "2.0.0-beta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.version
			result := GetVersion()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetFullVersion(t *testing.T) {
	// Save original values
	originalVersion := Version
	originalGitCommit := GitCommit
	defer func() {
		Version = originalVersion
		GitCommit = originalGitCommit
	}()

	tests := []struct {
		name      string
		version   string
		gitCommit string
		contains  []string
	}{
		{
			name:      "dev version",
			version:   "dev",
			gitCommit: "unknown",
			contains:  []string{"1.0.0-dev", "unknown", "go"},
		},
		{
			name:      "production version",
			version:   "v1.2.3",
			gitCommit: "abc123def456",
			contains:  []string{"v1.2.3", "abc123def456", "go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.version
			GitCommit = tt.gitCommit
			result := GetFullVersion()

			for _, substr := range tt.contains {
				assert.Contains(t, result, substr)
			}
		})
	}
}

func TestVersionVariables(t *testing.T) {
	// Test that version variables are accessible
	assert.NotEmpty(t, Version)
	assert.NotEmpty(t, GitCommit)
	assert.NotEmpty(t, BuildDate)
	assert.NotEmpty(t, GoVersion)
}

func TestBuildInfoStructure(t *testing.T) {
	info := BuildInfo{
		Version:   "1.0.0",
		GitCommit: "abc123",
		BuildDate: "2025-10-15",
		GoVersion: "go1.21.0",
	}

	assert.Equal(t, "1.0.0", info.Version)
	assert.Equal(t, "abc123", info.GitCommit)
	assert.Equal(t, "2025-10-15", info.BuildDate)
	assert.Equal(t, "go1.21.0", info.GoVersion)
}

func TestVersionConsistency(t *testing.T) {
	// Verify that GetBuildInfo and individual variables match
	info := GetBuildInfo()

	assert.Equal(t, Version, info.Version)
	assert.Equal(t, GitCommit, info.GitCommit)
	assert.Equal(t, BuildDate, info.BuildDate)
	assert.Equal(t, GoVersion, info.GoVersion)
}
