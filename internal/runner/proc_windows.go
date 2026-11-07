//go:build windows

package runner

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type procGroup struct{ job windows.Handle }

func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}

// attach places the process in a job object that kills the whole tree when
// terminated or when the daemon exits.
func attach(cmd *exec.Cmd) (*procGroup, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)) /* #nosec G103 -- required by the Win32 API */, uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)) //nolint:gosec // PIDs fit in uint32.
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(h) //nolint:errcheck
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	return &procGroup{job: job}, nil
}

// signal terminates the tree; Windows has no portable equivalent of SIGTERM
// for console processes started without a console.
func (g *procGroup) signal(string) error { return g.kill() }

func (g *procGroup) kill() error { return windows.TerminateJobObject(g.job, 1) }

func (g *procGroup) close() { _ = windows.CloseHandle(g.job) }

// setCommandLine builds cmd.exe's command line by hand: cmd.exe does not
// understand the backslash escaping Go applies to arguments, so commands with
// double quotes would otherwise be mangled.
func setCommandLine(cmd *exec.Cmd, sh Shell, command string) {
	if sh.Name != "cmd" {
		return
	}
	cmd.SysProcAttr.CmdLine = `"` + sh.Path + `" /d /s /c "chcp 65001>nul & ` + command + `"`
}
