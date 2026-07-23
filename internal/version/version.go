// Package version exposes the agent version embedded during the build.
package version

// Value is overridden by the Go linker for release builds.
// A variable is required here because -ldflags -X cannot set a constant.
var Value = "dev"
