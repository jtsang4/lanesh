//go:build linux

package service

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

const unitName = "lanesh.service"

var unitTemplate = template.Must(template.New("unit").Parse(`[Unit]
Description=Lanesh daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart="{{.Binary}}" daemon
Environment="LANESH_HOME={{.Home}}"
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
`))

type systemd struct{}

// New returns the service manager for this OS.
func New() (Manager, error) {
	if _, err := run("systemctl", "--user", "--version"); err != nil {
		return nil, fmt.Errorf("%w: systemd user services are not available (run `lanesh daemon` under your own supervisor instead)", ErrUnsupported)
	}
	return systemd{}, nil
}

func (systemd) Name() string { return "systemd" }

func unitPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "systemd", "user", unitName)
}

func (systemd) Install(c Config) error {
	if err := os.MkdirAll(filepath.Dir(unitPath()), 0o755); err != nil { //nolint:gosec // Standard systemd user unit directory.
		return err
	}
	f, err := os.OpenFile(unitPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644) //nolint:gosec // Units must be readable by systemd.
	if err != nil {
		return err
	}
	err = unitTemplate.Execute(f, c)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if _, err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if _, err := run("systemctl", "--user", "enable", unitName); err != nil {
		return err
	}
	_, err = run("systemctl", "--user", "restart", unitName)
	return err
}

func (systemd) Uninstall() error {
	_, _ = run("systemctl", "--user", "disable", "--now", unitName)
	if err := os.Remove(unitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, _ = run("systemctl", "--user", "daemon-reload")
	return nil
}

func (systemd) Start() error {
	_, err := run("systemctl", "--user", "start", unitName)
	return err
}

func (systemd) Stop() error {
	_, err := run("systemctl", "--user", "stop", unitName)
	return err
}

func (systemd) Restart() error {
	_, err := run("systemctl", "--user", "restart", unitName)
	return err
}

func (systemd) Status() (Status, error) {
	st := Status{Manager: "systemd"}
	if _, err := os.Stat(unitPath()); err != nil {
		return st, nil
	}
	st.Installed = true
	out, err := run("systemctl", "--user", "show", unitName, "-p", "ActiveState", "-p", "MainPID")
	if err != nil {
		st.Detail = err.Error()
		return st, nil
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "ActiveState="); ok {
			st.Running, st.Detail = v == "active", v
		}
		if v, ok := strings.CutPrefix(line, "MainPID="); ok {
			st.PID, _ = strconv.Atoi(v)
		}
	}
	return st, nil
}

// LingerEnabled reports whether the user's services keep running after logout.
func LingerEnabled() (bool, error) {
	u, err := user.Current()
	if err != nil {
		return false, err
	}
	out, err := run("loginctl", "show-user", u.Username, "-p", "Linger")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "Linger=yes", nil
}
