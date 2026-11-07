//go:build windows

package daemon

import "context"

// CaptureEnv returns the daemon's environment. On Windows a task started at
// logon already inherits the user's environment from the registry.
func CaptureEnv(context.Context) []string { return fallbackEnv() }
