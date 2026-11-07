package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Shell describes how to run a command string.
type Shell struct {
	Name string // short name, e.g. "zsh", "pwsh", "cmd"
	Path string
}

// knownShells lists shells worth reporting, in preference order per OS.
func knownShells() []string {
	if runtime.GOOS == "windows" {
		return []string{"pwsh", "powershell", "cmd", "bash"}
	}
	return []string{"bash", "zsh", "sh", "fish", "pwsh"}
}

// AvailableShells returns the short names of shells found on PATH.
func AvailableShells() []string {
	var out []string
	for _, s := range knownShells() {
		if _, err := exec.LookPath(s); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// DefaultShell returns the user's default shell.
func DefaultShell() Shell {
	if runtime.GOOS == "windows" {
		for _, s := range []string{"pwsh", "powershell"} {
			if p, err := exec.LookPath(s); err == nil {
				return Shell{Name: s, Path: p}
			}
		}
		return Shell{Name: "cmd", Path: "cmd.exe"}
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		if _, err := os.Stat(sh); err == nil { //nolint:gosec // $SHELL is the user's own configuration.
			return Shell{Name: filepath.Base(sh), Path: sh}
		}
	}
	return Shell{Name: "sh", Path: "/bin/sh"}
}

// ResolveShell maps a requested shell name or path to a Shell; empty means default.
func ResolveShell(name string) (Shell, error) {
	if name == "" {
		return DefaultShell(), nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return Shell{}, fmt.Errorf("shell %q not found", name)
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(p)), ".exe")
	return Shell{Name: base, Path: p}, nil
}

// powershellPrelude makes PowerShell emit UTF-8 regardless of the console code page.
const powershellPrelude = "[Console]::OutputEncoding=[System.Text.Encoding]::UTF8;$OutputEncoding=[System.Text.Encoding]::UTF8;"

// Args returns the argv that runs command with this shell.
func (s Shell) Args(command string) []string {
	switch s.Name {
	case "pwsh", "powershell":
		return []string{s.Path, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", powershellPrelude + command}
	case "cmd":
		return []string{s.Path, "/d", "/s", "/c", "chcp 65001>nul & " + command}
	default:
		return []string{s.Path, "-c", command}
	}
}
