// Package version holds build-time identity for the swd binary.
package version

// AppName is the human-facing product name (logs, --help).
const AppName = "Steam Workshop Downloader"

// Version is the semver for the Go rewrite. Override via -ldflags at release.
var Version = "6.0.0-dev"
