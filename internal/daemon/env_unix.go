//go:build !windows

package daemon

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// CaptureEnv returns the environment of the user's login, interactive shell,
// so that commands see the same PATH (nvm, brew, pipx, ...) as a terminal.
func CaptureEnv(ctx context.Context) []string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", "printf '"+envMarker+"\\000'; env -0") //nolint:gosec // The user's own shell.
	cmd.Stdin = nil
	out, err := cmd.Output()
	env := parseEnv(string(out))
	if len(env) == 0 {
		slog.Warn("could not capture login shell environment; using the daemon environment", "shell", shell, "err", err)
		return fallbackEnv()
	}
	return mergeEnv(env, os.Environ())
}

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// skipEnv are variables that describe the capturing shell rather than the user's environment.
var skipEnv = map[string]bool{"_": true, "SHLVL": true, "PWD": true, "OLDPWD": true, "PS1": true, "TERM_SESSION_ID": true}

const envMarker = "__LANESH_ENV__"

// parseEnv extracts NUL-separated KEY=VALUE pairs printed after envMarker.
func parseEnv(out string) []string {
	i := strings.Index(out, envMarker+"\x00")
	if i < 0 {
		return nil
	}
	var env []string
	for _, kv := range strings.Split(out[i+len(envMarker)+1:], "\x00") {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || !envKey.MatchString(k) || skipEnv[k] {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// mergeEnv adds variables from base that are missing in env.
func mergeEnv(env, base []string) []string {
	have := map[string]bool{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		have[k] = true
	}
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !have[k] && !skipEnv[k] {
			env = append(env, kv)
		}
	}
	return env
}
