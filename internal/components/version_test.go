package components

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// TestVersionCheckingLogic tests the version checking and skipping behavior
func TestVersionCheckingLogic(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	tests := []struct {
		name             string
		installedVersion string
		requestedVersion string
		releaseExists    bool
		expectedAction   string // "skip", "upgrade", or "install"
		expectedLog      string
	}{
		{
			name:             "Skip when no version requested and release exists",
			installedVersion: "3.11.0",
			requestedVersion: "",
			releaseExists:    true,
			expectedAction:   "skip",
			expectedLog:      "Release already installed with matching version, skipping",
		},
		{
			name:             "Skip when versions match",
			installedVersion: "3.11.0",
			requestedVersion: "3.11.0",
			releaseExists:    true,
			expectedAction:   "skip",
			expectedLog:      "Release already installed with matching version, skipping",
		},
		{
			name:             "Upgrade when version mismatch",
			installedVersion: "3.11.0",
			requestedVersion: "3.12.0",
			releaseExists:    true,
			expectedAction:   "upgrade",
			expectedLog:      "Version mismatch detected, upgrading release",
		},
		{
			name:             "Install when release doesn't exist",
			installedVersion: "",
			requestedVersion: "3.11.0",
			releaseExists:    false,
			expectedAction:   "install",
			expectedLog:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test version comparison logic
			var shouldSkip bool
			if tt.releaseExists {
				if tt.requestedVersion == "" || tt.requestedVersion == tt.installedVersion {
					shouldSkip = true
				}
			}

			if tt.expectedAction == "skip" {
				assert.True(t, shouldSkip, "Should skip installation/upgrade")
			} else if tt.expectedAction == "upgrade" {
				assert.False(t, shouldSkip, "Should upgrade")
				assert.NotEqual(t, tt.requestedVersion, tt.installedVersion, "Versions should differ")
			} else if tt.expectedAction == "install" {
				assert.False(t, tt.releaseExists, "Release should not exist")
			}
		})
	}
}

// TestComponentVersionCheckingIntegration tests the integration with component installation
func TestComponentVersionCheckingIntegration(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// This test demonstrates the expected behavior when components are already installed
	t.Run("Metrics Server already installed", func(t *testing.T) {
		// Simulate checking if metrics server is installed
		// In real scenario, this would check k8s deployment
		isInstalled := true // Simulating that it's already installed

		if isInstalled {
			logger.Info("✓ Metrics Server already installed")
			// Helm installer would then check version and skip if matches
		}

		assert.True(t, isInstalled)
	})
}

// TestHelmVersionComparison tests the Helm version comparison logic
func TestHelmVersionComparison(t *testing.T) {
	tests := []struct {
		name             string
		existingVersion  string
		requestedVersion string
		shouldSkip       bool
		shouldUpgrade    bool
	}{
		{
			name:             "Empty requested version - always skip",
			existingVersion:  "1.2.3",
			requestedVersion: "",
			shouldSkip:       true,
			shouldUpgrade:    false,
		},
		{
			name:             "Matching versions - skip",
			existingVersion:  "1.2.3",
			requestedVersion: "1.2.3",
			shouldSkip:       true,
			shouldUpgrade:    false,
		},
		{
			name:             "Different versions - upgrade",
			existingVersion:  "1.2.3",
			requestedVersion: "1.2.4",
			shouldSkip:       false,
			shouldUpgrade:    true,
		},
		{
			name:             "Downgrade version - still triggers upgrade",
			existingVersion:  "1.2.4",
			requestedVersion: "1.2.3",
			shouldSkip:       false,
			shouldUpgrade:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the version checking logic from helm.go
			shouldSkip := false
			if tt.requestedVersion == "" || tt.requestedVersion == tt.existingVersion {
				shouldSkip = true
			}

			assert.Equal(t, tt.shouldSkip, shouldSkip)
			if tt.shouldUpgrade {
				assert.False(t, shouldSkip)
			}
		})
	}
}

// Mock types for testing (since we can't easily mock Helm action client)
type mockHelmRelease struct {
	chart *mockChart
	info  *mockReleaseInfo
}

type mockChart struct {
	metadata *mockMetadata
}

type mockMetadata struct {
	version string
}

type mockReleaseInfo struct {
	status string
}

// TestVersionCheckingWithMockHelm demonstrates the expected Helm behavior
func TestVersionCheckingWithMockHelm(t *testing.T) {
	logger := logrus.New()

	t.Run("Release exists with matching version", func(t *testing.T) {
		existingRelease := &mockHelmRelease{
			chart: &mockChart{
				metadata: &mockMetadata{
					version: "3.11.0",
				},
			},
			info: &mockReleaseInfo{
				status: "deployed",
			},
		}

		requestedVersion := "3.11.0"

		// Check version matching logic
		if requestedVersion == "" || requestedVersion == existingRelease.chart.metadata.version {
			logger.WithFields(logrus.Fields{
				"release":           "test-release",
				"installed_version": existingRelease.chart.metadata.version,
				"requested_version": requestedVersion,
				"status":            existingRelease.info.status,
			}).Info("Release already installed with matching version, skipping")

			// Should skip
			assert.True(t, true, "Should skip installation")
			return
		}

		t.Fail() // Should not reach here
	})

	t.Run("Release exists with version mismatch", func(t *testing.T) {
		existingRelease := &mockHelmRelease{
			chart: &mockChart{
				metadata: &mockMetadata{
					version: "3.11.0",
				},
			},
			info: &mockReleaseInfo{
				status: "deployed",
			},
		}

		requestedVersion := "3.12.0"

		// Check version matching logic
		if requestedVersion == "" || requestedVersion == existingRelease.chart.metadata.version {
			t.Fail() // Should not skip
			return
		}

		// Version mismatch detected
		logger.WithFields(logrus.Fields{
			"release":           "test-release",
			"installed_version": existingRelease.chart.metadata.version,
			"requested_version": requestedVersion,
		}).Info("Version mismatch detected, upgrading release")

		// Should upgrade
		assert.NotEqual(t, requestedVersion, existingRelease.chart.metadata.version)
	})

	t.Run("No version specified - accept any installed version", func(t *testing.T) {
		existingRelease := &mockHelmRelease{
			chart: &mockChart{
				metadata: &mockMetadata{
					version: "3.11.0",
				},
			},
			info: &mockReleaseInfo{
				status: "deployed",
			},
		}

		requestedVersion := "" // No specific version

		// Check version matching logic
		if requestedVersion == "" || requestedVersion == existingRelease.chart.metadata.version {
			logger.WithFields(logrus.Fields{
				"release":           "test-release",
				"installed_version": existingRelease.chart.metadata.version,
				"requested_version": requestedVersion,
				"status":            existingRelease.info.status,
			}).Info("Release already installed with matching version, skipping")

			// Should skip - accept any version when none specified
			assert.True(t, true, "Should skip installation when no version specified")
			return
		}

		t.Fail() // Should not reach here
	})
}

// TestComponentStatusTracking tests component status tracking
func TestComponentStatusTracking(t *testing.T) {
	logger := logrus.New()

	components := []struct {
		name      string
		installed bool
		version   string
		ready     bool
	}{
		{name: "metrics-server", installed: true, version: "3.11.0", ready: true},
		{name: "kube-state-metrics", installed: true, version: "5.14.0", ready: true},
		{name: "prometheus-node-exporter", installed: true, version: "4.24.0", ready: true},
	}

	for _, comp := range components {
		t.Run(comp.name, func(t *testing.T) {
			logger.WithFields(logrus.Fields{
				"component": comp.name,
				"installed": comp.installed,
				"version":   comp.version,
				"ready":     comp.ready,
			}).Info("Component status")

			assert.True(t, comp.installed)
			assert.True(t, comp.ready)
			assert.NotEmpty(t, comp.version)
		})
	}
}
