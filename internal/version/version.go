// Package version provides version information for otun.
package version

// These variables are set at build time via ldflags.
var (
	// Version is the semantic version (e.g., "0.1.0")
	Version = "dev"

	// Commit is the git commit hash
	Commit = "unknown"

	// Date is the build date
	Date = "unknown"
)

// String returns the full version string.
func String() string {
	return Version
}

// Full returns the full version string with commit and date.
func Full() string {
	return Version + " (commit: " + Commit + ", built: " + Date + ")"
}
