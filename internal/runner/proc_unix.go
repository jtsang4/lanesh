//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

type procGroup struct{ pid int }

func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attach(cmd *exec.Cmd) (*procGroup, error) {
	return &procGroup{pid: cmd.Process.Pid}, nil
}

func signalByName(name string) syscall.Signal {
	switch name {
	case "INT", "SIGINT":
		return syscall.SIGINT
	case "KILL", "SIGKILL":
		return syscall.SIGKILL
	case "HUP", "SIGHUP":
		return syscall.SIGHUP
	default:
		return syscall.SIGTERM
	}
}

// signal sends sig to the whole process group.
func (g *procGroup) signal(name string) error {
	return syscall.Kill(-g.pid, signalByName(name))
}

func (g *procGroup) kill() error { return syscall.Kill(-g.pid, syscall.SIGKILL) }

func (g *procGroup) close() {}

func setCommandLine(*exec.Cmd, Shell, string) {}
