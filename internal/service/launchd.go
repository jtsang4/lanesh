//go:build darwin

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

const launchdLabel = "dev.lanesh.daemon"

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Binary}}</string>
		<string>daemon</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>LANESH_HOME</key>
		<string>{{.Home}}</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>5</integer>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>{{.LogFile}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogFile}}</string>
</dict>
</plist>
`))

type launchd struct{}

// New returns the service manager for this OS.
func New() (Manager, error) { return launchd{}, nil }

func (launchd) Name() string { return "launchd" }

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func target() string { return domain() + "/" + launchdLabel }

func (l launchd) Install(c Config) error {
	if err := os.MkdirAll(filepath.Dir(plistPath()), 0o755); err != nil { //nolint:gosec // Standard LaunchAgents directory.
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.LogFile), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(plistPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644) //nolint:gosec // LaunchAgents must be readable by launchd.
	if err != nil {
		return err
	}
	err = plistTemplate.Execute(f, map[string]string{"Label": launchdLabel, "Binary": c.Binary, "Home": c.Home, "LogFile": c.LogFile})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	_, _ = run("launchctl", "bootout", target())
	_, err = run("launchctl", "bootstrap", domain(), plistPath())
	return err
}

func (launchd) Uninstall() error {
	_, _ = run("launchctl", "bootout", target())
	if err := os.Remove(plistPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func loaded() bool {
	_, err := run("launchctl", "print", target())
	return err == nil
}

func (launchd) Start() error {
	if _, err := os.Stat(plistPath()); err != nil {
		return fmt.Errorf("service is not installed")
	}
	if !loaded() {
		_, err := run("launchctl", "bootstrap", domain(), plistPath())
		return err
	}
	_, err := run("launchctl", "kickstart", target())
	return err
}

// Stop unloads the agent so that KeepAlive does not restart it.
func (launchd) Stop() error {
	if !loaded() {
		return nil
	}
	_, err := run("launchctl", "bootout", target())
	return err
}

func (l launchd) Restart() error {
	if !loaded() {
		return l.Start()
	}
	_, err := run("launchctl", "kickstart", "-k", target())
	return err
}

func (launchd) Status() (Status, error) {
	st := Status{Manager: "launchd"}
	if _, err := os.Stat(plistPath()); err != nil {
		return st, nil
	}
	st.Installed = true
	out, err := run("launchctl", "print", target())
	if err != nil {
		st.Detail = "not loaded"
		return st, nil
	}
	st.Running, st.PID, st.Detail = parseLaunchctlPrint(out)
	return st, nil
}

// parseLaunchctlPrint reads the service's own state and pid, which are
// indented by exactly one tab; nested sections repeat these keys.
func parseLaunchctlPrint(out string) (running bool, pid int, state string) {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "\tstate = "); ok && state == "" {
			state = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "\tpid = "); ok && pid == 0 {
			pid, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	return state == "running", pid, state
}
