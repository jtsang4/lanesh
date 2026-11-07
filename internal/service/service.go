// Package service installs the Lanesh daemon as a per-user background service:
// a launchd agent on macOS, a systemd user unit on Linux and a logon-triggered
// scheduled task on Windows.
package service

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ExitRestart is the exit code the daemon uses to ask for a restart (after a
// self-update). Service managers restart it on any exit.
const ExitRestart = 75

// Config describes the service to install.
type Config struct {
	// Binary is the absolute path of the lanesh executable.
	Binary string
	// Home is the Lanesh home directory (LANESH_HOME), passed to the daemon.
	Home string
	// LogFile receives the daemon's stdout and stderr where supported.
	LogFile string
}

// Status describes the installed service.
type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	Manager   string `json:"manager"`
	Detail    string `json:"detail,omitempty"`
}

// Manager controls the service.
type Manager interface {
	Name() string
	Install(Config) error
	Uninstall() error
	Start() error
	Stop() error
	Restart() error
	Status() (Status, error)
}

// ErrUnsupported is returned when no supported service manager is available.
var ErrUnsupported = errors.New("no supported service manager found")

func run(name string, args ...string) (string, error) { //nolint:unparam // Each OS uses a different command.
	cmd := exec.Command(name, args...) //nolint:gosec,noctx // Fixed service-manager commands.
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, text)
		}
		return text, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return text, nil
}
