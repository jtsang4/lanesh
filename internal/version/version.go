// Package version exposes build information injected at link time.
package version

import (
	"fmt"
	"runtime"
)

// These variables are set via -ldflags at build time.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human-readable description of the build.
func String() string {
	return fmt.Sprintf("lanesh %s (commit %s, built %s, %s/%s)",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH)
}
