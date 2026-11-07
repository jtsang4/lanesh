package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jtsang4/lanesh/internal/daemon"
	"github.com/jtsang4/lanesh/internal/service"
	"github.com/jtsang4/lanesh/internal/skills"
	"github.com/jtsang4/lanesh/internal/update"
	"github.com/jtsang4/lanesh/internal/version"
)

type checker struct {
	w        io.Writer
	problems int
}

func (c *checker) ok(format string, a ...any)   { fmt.Fprintf(c.w, "  ✓ "+format+"\n", a...) }
func (c *checker) warn(format string, a ...any) { fmt.Fprintf(c.w, "  ! "+format+"\n", a...) }
func (c *checker) fail(format string, a ...any) {
	c.problems++
	fmt.Fprintf(c.w, "  ✗ "+format+"\n", a...)
}

func newDoctorCmd() *cobra.Command {
	var refreshEnv bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the local installation, service and lane connections",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			c := &checker{w: cmd.OutOrStdout()}
			ctx := cmd.Context()

			fmt.Fprintln(c.w, "Installation")
			if mv := managedVersion(p); mv != "" {
				c.ok("managed binary %s (%s)", p.Binary(), mv)
			} else {
				c.warn("no managed binary at %s (it is installed by `lanesh join`)", p.Binary())
			}
			if !update.SameFile(p.Binary()) {
				c.warn("this command runs from %s, not the managed binary", absPath(os.Args[0]))
			}

			fmt.Fprintln(c.w, "Service")
			m, err := serviceManager()
			var svc service.Status
			if err != nil {
				c.fail("%v", err)
			} else {
				svc, _ = m.Status()
				switch {
				case !svc.Installed:
					c.warn("not installed")
				case svc.Running:
					c.ok("running under %s (pid %d)", svc.Manager, svc.PID)
				default:
					c.fail("installed but not running (%s); try `lanesh start` and `lanesh logs`", svc.Detail)
				}
				if runtime.GOOS == "linux" {
					if on, err := service.LingerEnabled(); err == nil && !on {
						c.warn("linger is off: the daemon stops when you log out (fix: sudo loginctl enable-linger $USER)")
					}
				}
			}

			fmt.Fprintln(c.w, "Configuration")
			cfg, err := daemon.LoadConfig(p)
			if err != nil {
				c.fail("%v", err)
			}
			creds, _ := daemon.LoadCredentials(p)
			if len(cfg.Lanes) == 0 {
				c.warn("no lanes joined")
			}
			st, live := liveState(p)
			for _, l := range cfg.Lanes {
				name := l.LaneName + " (" + l.Server + ")"
				if creds[l.Key()] == "" {
					c.fail("%s: missing credential; run lanesh join again", name)
					continue
				}
				if v, err := serverVersion(ctx, l.Server); err != nil {
					c.fail("%s: server unreachable: %v", name, err)
				} else {
					c.ok("%s: server reachable, version %s", name, v)
					if update.IsRelease(v) && update.Compare(v, version.Version) > 0 {
						c.warn("%s: server is newer than this lanesh (%s); the daemon updates itself automatically", name, version.Version)
					}
				}
				if live {
					for _, ls := range st.Lanes {
						if ls.LaneID != l.LaneID {
							continue
						}
						if ls.Connected {
							c.ok("%s: daemon connected as %s", name, ls.MachineName)
						} else {
							c.fail("%s: daemon not connected: %s", name, ls.LastError)
						}
					}
				}
			}

			fmt.Fprintln(c.w, "Skills")
			home, _ := os.UserHomeDir()
			srcs := skills.DefaultSources(home)
			idx := skills.Scan(srcs)
			for _, s := range srcs {
				if _, err := os.Stat(s.Dir); err == nil {
					c.ok("%s exists", s.Dir)
				} else {
					c.warn("%s does not exist", s.Dir)
				}
			}
			c.ok("%d skills (%d shadowed by name)", len(idx.Skills), len(idx.Shadowed))

			fmt.Fprintln(c.w, "Environment")
			env := daemon.CaptureEnv(ctx)
			path := ""
			for _, kv := range env {
				if k, v, _ := strings.Cut(kv, "="); strings.EqualFold(k, "PATH") {
					path = v
				}
			}
			if path == "" {
				c.fail("PATH is empty in the captured environment")
			} else {
				c.ok("commands run with PATH=%s", ellipsis(path, 160))
			}
			if refreshEnv && svc.Installed {
				if err := m.Restart(); err != nil {
					c.fail("restart service: %v", err)
				} else {
					c.ok("restarted the daemon to refresh its environment")
				}
			}

			if c.problems > 0 {
				fmt.Fprintf(c.w, "\n%d problem(s) found.\n", c.problems)
				return exitError{code: 1}
			}
			fmt.Fprintln(c.w, "\nNo problems found.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&refreshEnv, "refresh-env", false, "restart the daemon so it captures your current shell environment")
	return cmd
}

func ellipsis(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func serverVersion(ctx context.Context, server string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(server, "/")+"/healthz", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("unexpected response: %s", resp.Status)
	}
	return body.Version, nil
}
