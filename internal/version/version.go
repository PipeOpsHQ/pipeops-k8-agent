package version

import (
	"runtime"
)

var (
	// These variables are set at build time using ldflags
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
	GoVersion = runtime.Version()
)

// BuildInfo represents build and version information
type BuildInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
}

// GetBuildInfo returns the current build information
func GetBuildInfo() *BuildInfo {
	return &BuildInfo{
		Version:   Version,
		GitCommit: GitCommit,
		BuildDate: BuildDate,
		GoVersion: GoVersion,
	}
}

// GetVersion returns the current version string
func GetVersion() string {
	if Version == "dev" {
		return "1.0.0-dev"
	}
	return Version
}

// GetFullVersion returns a detailed version string
func GetFullVersion() string {
	if Version == "dev" {
		return "1.0.0-dev (unknown, " + GoVersion + ")"
	}
	return Version + " (" + GitCommit + ", " + GoVersion + ")"
}
