//go:build windows

package service

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"unicode/utf16"
)

const taskName = "Lanesh Daemon"

var taskTemplate = template.Must(template.New("task").Parse(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Connects this machine to its Lanesh lanes.</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>{{.User}}</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>{{.User}}</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>{{.Command}}</Command>
      <Arguments>{{.Arguments}}</Arguments>
    </Exec>
  </Actions>
</Task>
`))

type schtasks struct{}

// New returns the service manager for this OS.
func New() (Manager, error) { return schtasks{}, nil }

func (schtasks) Name() string { return "Task Scheduler" }

func xmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (schtasks) Install(c Config) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	// conhost --headless runs the console program without showing a window.
	command, args := c.Binary, `daemon --supervise`
	conhost := filepath.Join(os.Getenv("SystemRoot"), "System32", "conhost.exe")
	if _, err := os.Stat(conhost); err == nil { //nolint:gosec // %SystemRoot% is a trusted system path.
		command, args = conhost, `--headless "`+c.Binary+`" daemon --supervise`
	}
	var buf bytes.Buffer
	if err := taskTemplate.Execute(&buf, map[string]string{
		"User": xmlEscape(u.Username), "Command": xmlEscape(command), "Arguments": xmlEscape(args),
	}); err != nil {
		return err
	}
	xmlPath := filepath.Join(c.Home, "task.xml")
	if err := os.WriteFile(xmlPath, utf16le(buf.String()), 0o600); err != nil { //nolint:gosec // Path is inside the Lanesh home.
		return err
	}
	defer os.Remove(xmlPath) //nolint:errcheck
	if _, err := run("schtasks", "/Create", "/TN", taskName, "/XML", xmlPath, "/F"); err != nil {
		return err
	}
	_ = stopProcesses(c.Home)
	_, err = run("schtasks", "/Run", "/TN", taskName)
	return err
}

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, 2+2*len(u))
	out[0], out[1] = 0xFF, 0xFE
	for i, v := range u {
		binary.LittleEndian.PutUint16(out[2+2*i:], v)
	}
	return out
}

func (s schtasks) Uninstall() error {
	_ = s.Stop()
	if st, _ := s.Status(); !st.Installed {
		return nil
	}
	_, err := run("schtasks", "/Delete", "/TN", taskName, "/F")
	return err
}

func (schtasks) Start() error {
	_, err := run("schtasks", "/Run", "/TN", taskName)
	return err
}

func (schtasks) Stop() error {
	_, _ = run("schtasks", "/End", "/TN", taskName)
	return stopProcesses(laneshHome())
}

func (s schtasks) Restart() error {
	_ = s.Stop()
	return s.Start()
}

func (schtasks) Status() (Status, error) {
	st := Status{Manager: "Task Scheduler"}
	out, err := run("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-ScheduledTask -TaskName '"+taskName+"' -ErrorAction Stop).State")
	if err != nil {
		return st, nil
	}
	st.Installed = true
	st.Detail = strings.TrimSpace(out)
	st.Running = st.Detail == "Running"
	if pid, ok := readPID(filepath.Join(laneshHome(), supervisorPIDFile)); ok {
		st.PID = pid
	}
	return st, nil
}

func laneshHome() string {
	if h := os.Getenv("LANESH_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".lanesh")
}

func readPID(path string) (int, bool) {
	raw, err := os.ReadFile(path) //nolint:gosec // Path inside the Lanesh home.
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid, err == nil && pid > 0
}

// stopProcesses terminates the supervisor (whose job object then ends the daemon).
func stopProcesses(home string) error {
	pid, ok := readPID(filepath.Join(home, supervisorPIDFile))
	if !ok {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := p.Kill(); err != nil {
		return fmt.Errorf("stop daemon: %w", err)
	}
	_ = os.Remove(filepath.Join(home, supervisorPIDFile))
	return nil
}
