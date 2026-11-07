package daemon

import "os"

// fallbackEnv is used when the login environment cannot be captured.
func fallbackEnv() []string { return os.Environ() }
