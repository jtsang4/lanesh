package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jtsang4/lanesh/internal/daemon"
	"github.com/jtsang4/lanesh/internal/service"
	"github.com/jtsang4/lanesh/internal/update"
	"github.com/jtsang4/lanesh/internal/version"
)

func newDaemonCmd() *cobra.Command {
	var supervise bool
	cmd := &cobra.Command{
		Use:    "daemon",
		Short:  "Run the machine daemon in the foreground (normally started by the background service)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if supervise {
				bin := p.Binary()
				if _, err := os.Stat(bin); err != nil {
					bin, _ = os.Executable()
				}
				return service.Supervise(ctx, bin, p.Home)
			}
			return runDaemon(ctx, p)
		},
	}
	cmd.Flags().BoolVar(&supervise, "supervise", false, "restart the daemon whenever it exits (used on Windows)")
	return cmd
}

func runDaemon(ctx context.Context, p daemon.Paths) error {
	logFile, err := daemon.OpenLog(p)
	if err != nil {
		return err
	}
	defer logFile.Close()
	out := io.Writer(logFile)
	if term.IsTerminal(int(os.Stderr.Fd())) { //nolint:gosec // File descriptors fit in int.
		out = io.MultiWriter(logFile, os.Stderr)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo})))

	unlock, err := daemon.Lock(p.Lock())
	if err != nil {
		return err
	}
	defer unlock()
	update.Cleanup(p.Binary())

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var restart, updating atomic.Bool
	var failedMu sync.Mutex
	failedAt := map[string]time.Time{}
	onVersion := func(serverVersion string) {
		cfg, _ := daemon.LoadConfig(p)
		if !cfg.AutoUpdate() || !update.IsRelease(serverVersion) || update.Compare(serverVersion, version.Version) <= 0 {
			return
		}
		if !update.SameFile(p.Binary()) {
			slog.Info("server runs a newer version, but this daemon is not the managed binary; skipping self-update",
				"server", serverVersion, "binary", p.Binary())
			return
		}
		failedMu.Lock()
		recent := time.Since(failedAt[serverVersion]) < 15*time.Minute
		failedMu.Unlock()
		if recent || !updating.CompareAndSwap(false, true) {
			return
		}
		defer updating.Store(false)
		slog.Info("updating lanesh", "from", version.Version, "to", serverVersion)
		uctx, ucancel := context.WithTimeout(ctx, 10*time.Minute)
		defer ucancel()
		if err := update.Apply(uctx, cfg.Update.Source, serverVersion, p.Binary()); err != nil {
			slog.Error("self-update failed; will retry later", "err", err)
			failedMu.Lock()
			failedAt[serverVersion] = time.Now()
			failedMu.Unlock()
			return
		}
		slog.Info("update installed; restarting")
		restart.Store(true)
		cancel()
	}

	d, err := daemon.New(ctx, daemon.Options{Paths: p, OnServerVersion: onVersion})
	if err != nil {
		return err
	}
	if err := d.Run(ctx); err != nil {
		return err
	}
	if restart.Load() {
		return exitError{code: service.ExitRestart}
	}
	return nil
}

// ensureManagedBinary copies the running executable to the managed location
// so that the background service runs a binary Lanesh can update.
func ensureManagedBinary(p daemon.Paths) error {
	if update.SameFile(p.Binary()) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if cur := managedVersion(p); cur != "" && update.Compare(cur, version.Version) > 0 {
		return nil // keep a newer managed binary (e.g. after a self-update)
	}
	data, err := os.ReadFile(exe) //nolint:gosec // Our own executable.
	if err != nil {
		return err
	}
	return update.Install(data, p.Binary())
}

func managedVersion(p daemon.Paths) string {
	if _, err := os.Stat(p.Binary()); err != nil {
		return ""
	}
	out, err := runQuiet(p.Binary(), "version", "--short")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func installService(cmd *cobra.Command, p daemon.Paths) error {
	if err := ensureManagedBinary(p); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	m, err := serviceManager()
	if err != nil {
		return err
	}
	if err := m.Install(service.Config{Binary: p.Binary(), Home: p.Home, LogFile: p.DaemonLog()}); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Background service installed (%s).\n", m.Name())
	if on, err := service.LingerEnabled(); err == nil && !on {
		fmt.Fprintln(cmd.OutOrStdout(), "Note: run `sudo loginctl enable-linger $USER` so the daemon keeps running after you log out.")
	}
	return nil
}

func newJoinCmd() *cobra.Command {
	var key, name string
	var noExec, noService bool
	cmd := &cobra.Command{
		Use:   "join <lane-url>",
		Short: "Join this machine to a lane and start the background service",
		Example: `  lanesh join https://lanesh.example.com/lanes/ln_abc123 --key lsj_...
  lanesh join https://lanesh.example.com/lanes/ln_abc123 --key lsj_... --name build-box --no-exec`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if key == "" {
				return errors.New("--key is required (create a join key in the web UI or with `lanesh key create`)")
			}
			p, err := paths()
			if err != nil {
				return err
			}
			o := daemon.JoinOptions{LaneURL: args[0], Key: key, Name: name}
			if noExec {
				f := false
				o.AllowExec = &f
			}
			res, err := daemon.Join(cmd.Context(), p, o)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Joined lane %q as %q.\n", res.Lane.LaneName, res.Lane.MachineName)
			if res.Policy.AllowExec {
				fmt.Fprintln(w, "\nThis machine allows agents with an access key for this lane to run commands and read or write files")
				fmt.Fprintln(w, "as your user. Only share access keys with agents you trust. Disable with `lanesh config set policy.allow_exec false`.")
			} else {
				fmt.Fprintln(w, "Command execution and file access are disabled for this lane; agents can only read skills.")
			}
			if noService {
				fmt.Fprintln(w, "\nSkipped the background service. Run `lanesh daemon` to connect.")
				return nil
			}
			fmt.Fprintln(w)
			if err := installService(cmd, p); err != nil {
				fmt.Fprintf(w, "Could not install the background service: %v\nRun `lanesh daemon` in a terminal or under your own supervisor instead.\n", err)
				return nil
			}
			waitConnected(cmd, p, res.Lane.LaneID)
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "join key for the lane")
	cmd.Flags().StringVar(&name, "name", "", "machine name shown in the lane (default: host name)")
	cmd.Flags().BoolVar(&noExec, "no-exec", false, "share skills only; do not allow command execution or file access")
	cmd.Flags().BoolVar(&noService, "no-service", false, "do not install the background service")
	return cmd
}

func waitConnected(cmd *cobra.Command, p daemon.Paths, laneID string) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if st, err := daemon.ReadState(p); err == nil {
			for _, l := range st.Lanes {
				if l.LaneID == laneID && l.Connected {
					fmt.Fprintln(cmd.OutOrStdout(), "Connected.")
					return
				}
				if l.LaneID == laneID && l.LastError != "" && time.Since(st.StartedAt) > 5*time.Second {
					fmt.Fprintf(cmd.OutOrStdout(), "Not connected yet: %s\n", l.LastError)
					return
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "The daemon has not connected yet; check `lanesh status` and `lanesh logs`.")
}

func newLeaveCmd() *cobra.Command {
	var keepService bool
	cmd := &cobra.Command{
		Use:   "leave <lane>",
		Short: "Leave a lane (by lane ID, name or URL)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			lane, err := daemon.Leave(cmd.Context(), p, args[0])
			if lane.LaneID == "" {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Left lane %q.\n", lane.LaneName)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "Warning:", err)
			}
			cfg, _ := daemon.LoadConfig(p)
			if len(cfg.Lanes) == 0 && !keepService {
				if m, err := serviceManager(); err == nil {
					if st, _ := m.Status(); st.Installed {
						if err := m.Uninstall(); err != nil {
							return err
						}
						fmt.Fprintln(cmd.OutOrStdout(), "No lanes left; removed the background service.")
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepService, "keep-service", false, "keep the background service even when no lanes remain")
	return cmd
}

func newStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the daemon, service and lane connection status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			cfg, err := daemon.LoadConfig(p)
			if err != nil {
				return err
			}
			var svc service.Status
			if m, err := serviceManager(); err == nil {
				svc, _ = m.Status()
			}
			st, fresh := liveState(p)
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"service": svc, "state": st, "config": cfg})
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "lanesh %s\n", version.Version)
			switch {
			case !svc.Installed:
				fmt.Fprintln(w, "Service:  not installed")
			case svc.Running:
				fmt.Fprintf(w, "Service:  running (%s, pid %d)\n", svc.Manager, svc.PID)
			default:
				fmt.Fprintf(w, "Service:  stopped (%s: %s)\n", svc.Manager, svc.Detail)
			}
			if fresh {
				fmt.Fprintf(w, "Daemon:   %s, %d skills, up since %s\n", st.Version, st.Skills, st.StartedAt.Local().Format(time.DateTime))
			}
			if len(cfg.Lanes) == 0 {
				fmt.Fprintln(w, "Lanes:    none (join one with `lanesh join`)")
				return nil
			}
			fmt.Fprintln(w, "Lanes:")
			for _, l := range cfg.Lanes {
				line := fmt.Sprintf("  %-20s %s as %s", l.LaneName, l.Server, l.MachineName)
				state := "daemon not running (start it with `lanesh start` or `lanesh daemon`)"
				if fresh {
					state = "not connected"
					for _, ls := range st.Lanes {
						if ls.LaneID == l.LaneID && ls.Server == l.Server {
							if ls.Connected {
								state = "connected since " + ls.Since.Local().Format(time.DateTime)
							} else if ls.LastError != "" {
								state = "disconnected: " + ls.LastError
							}
						}
					}
				}
				pol := cfg.EffectivePolicy(l)
				fmt.Fprintf(w, "%s\n    %s; exec %s\n", line, state, map[bool]string{true: "allowed", false: "disabled"}[pol.AllowExec])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func newLogsCmd() *cobra.Command {
	var follow bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the daemon log",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			f, err := os.Open(p.DaemonLog())
			if err != nil {
				return fmt.Errorf("no log yet at %s", p.DaemonLog())
			}
			defer f.Close()
			if err := tail(f, lines, cmd.OutOrStdout()); err != nil {
				return err
			}
			if !follow {
				return nil
			}
			r := bufio.NewReader(f)
			for {
				line, err := r.ReadString('\n')
				if len(line) > 0 {
					fmt.Fprint(cmd.OutOrStdout(), line)
				}
				if errors.Is(err, io.EOF) {
					select {
					case <-cmd.Context().Done():
						return nil
					case <-time.After(300 * time.Millisecond):
					}
					continue
				}
				if err != nil {
					return err
				}
			}
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines")
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "number of lines to show")
	return cmd
}

// tail prints the last n lines of f and leaves f positioned at its end.
func tail(f *os.File, n int, w io.Writer) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	const chunk = 64 << 10
	start := max(info.Size()-chunk*4, 0)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if start > 0 && len(all) > 0 {
		all = all[1:]
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for _, l := range all {
		if l != "" {
			fmt.Fprintln(w, l)
		}
	}
	return nil
}

func serviceAction(use, short string, act func(service.Manager) error, done string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := serviceManager()
			if err != nil {
				return err
			}
			if st, _ := m.Status(); !st.Installed {
				return errors.New("the background service is not installed (it is set up by `lanesh join`)")
			}
			if err := act(m); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), done)
			return nil
		},
	}
}

func newStartCmd() *cobra.Command {
	return serviceAction("start", "Start the background service", service.Manager.Start, "Started.")
}

func newStopCmd() *cobra.Command {
	return serviceAction("stop", "Stop the background service", service.Manager.Stop, "Stopped.")
}

func newRestartCmd() *cobra.Command {
	return serviceAction("restart", "Restart the background service", service.Manager.Restart, "Restarted.")
}

func newUpdateCmd() *cobra.Command {
	var target string
	var check bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update lanesh to the version of your servers (or the latest release)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			explicit := target != ""
			if !explicit {
				if st, err := daemon.ReadState(p); err == nil {
					for _, l := range st.Lanes {
						if update.IsRelease(l.ServerVersion) && update.Compare(l.ServerVersion, target) > 0 {
							target = l.ServerVersion
						}
					}
				}
				if target == "" {
					if target, err = update.Latest(cmd.Context()); err != nil {
						return err
					}
				}
			}
			target = strings.TrimPrefix(target, "v")
			current := managedVersion(p)
			if current == "" {
				current = version.Version
			}
			if !explicit && update.Compare(target, current) <= 0 {
				fmt.Fprintf(w, "lanesh %s is up to date.\n", current)
				return nil
			}
			if check {
				fmt.Fprintf(w, "Update available: %s -> %s\n", current, target)
				return nil
			}
			cfg, _ := daemon.LoadConfig(p)
			fmt.Fprintf(w, "Installing lanesh %s...\n", target)
			if err := update.Apply(cmd.Context(), cfg.Update.Source, target, p.Binary()); err != nil {
				return err
			}
			if m, err := serviceManager(); err == nil {
				if st, _ := m.Status(); st.Installed {
					if err := m.Restart(); err != nil {
						return err
					}
					fmt.Fprintln(w, "Restarted the background service.")
				}
			}
			fmt.Fprintf(w, "Installed %s.\n", p.Binary())
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "version", "", "install a specific version (may downgrade)")
	cmd.Flags().BoolVar(&check, "check", false, "only report whether an update is available")
	return cmd
}

// liveState returns the state written by a running daemon, whether it runs
// as the background service or in the foreground.
func liveState(p daemon.Paths) (daemon.State, bool) {
	st, err := daemon.ReadState(p)
	if err != nil {
		return st, false
	}
	return st, processAlive(st.PID) && time.Since(st.UpdatedAt) < time.Minute
}

func runQuiet(name string, args ...string) (string, error) {
	return runCmd(name, args...)
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
