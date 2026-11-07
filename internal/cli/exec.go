package cli

import (
	"context"
	"os/exec"
	"time"
)

func runCmd(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // Runs our own binary.
	return string(out), err
}
