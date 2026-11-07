package service

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const supervisorPIDFile = "supervisor.pid"

// Supervise runs `binary daemon` and restarts it whenever it exits, until ctx
// is cancelled. It is used on Windows, where Task Scheduler does not restart
// tasks that exit. The binary path is re-read on every start so that a
// self-update takes effect.
func Supervise(ctx context.Context, binary, home string) error {
	_ = os.WriteFile(filepath.Join(home, supervisorPIDFile), []byte(strconv.Itoa(os.Getpid())), 0o600)
	defer os.Remove(filepath.Join(home, supervisorPIDFile)) //nolint:errcheck

	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		cmd := exec.CommandContext(ctx, binary, "daemon") //nolint:gosec // Our own binary.
		cmd.Env = append(os.Environ(), "LANESH_HOME="+home)
		prepareChild(cmd)
		err := cmd.Start()
		if err == nil {
			bindChild(cmd)
			err = cmd.Wait()
		}
		if ctx.Err() != nil {
			return nil
		}
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		if code == ExitRestart {
			backoff = time.Second
			slog.Info("daemon requested a restart")
			continue
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		slog.Warn("daemon exited; restarting", "code", code, "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
	return nil
}
