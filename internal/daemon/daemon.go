package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/user"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/runner"
	"github.com/jtsang4/lanesh/internal/skills"
	"github.com/jtsang4/lanesh/internal/version"
)

// Options configures a Daemon.
type Options struct {
	Paths Paths
	// Env overrides login-environment capture (used in tests).
	Env []string
	// Home overrides the user's home directory (used in tests).
	Home string
	// OnServerVersion is called with the version of each server connected to.
	OnServerVersion func(string)
	// ReconcileInterval controls how often the config file is re-read.
	ReconcileInterval time.Duration
}

// Daemon connects this machine to its lanes and serves their requests.
type Daemon struct {
	opts      Options
	machineID string
	home      string
	hostname  string
	sources   []skills.Source
	jobs      *runner.Manager
	audit     *auditLog

	mu    sync.RWMutex
	env   []string
	index protocol.SkillIndex
	cfg   Config
	conns map[string]*laneConn
}

// New prepares a daemon.
func New(ctx context.Context, opts Options) (*Daemon, error) {
	if opts.ReconcileInterval <= 0 {
		opts.ReconcileInterval = 3 * time.Second
	}
	id, err := MachineID(opts.Paths)
	if err != nil {
		return nil, err
	}
	home := opts.Home
	if home == "" {
		if home, err = os.UserHomeDir(); err != nil {
			return nil, err
		}
	}
	hostname, _ := os.Hostname()
	d := &Daemon{
		opts: opts, machineID: id, home: home, hostname: hostname,
		sources: skills.DefaultSources(home), conns: map[string]*laneConn{},
		audit: newAuditLog(opts.Paths.AuditLog()),
	}
	d.env = opts.Env
	if d.env == nil {
		d.env = CaptureEnv(ctx)
	}
	d.index = skills.Scan(d.sources)
	d.jobs = runner.NewManager(runner.Options{Env: d.environ, OnExit: d.onJobExit})
	return d, nil
}

func (d *Daemon) environ() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]string(nil), d.env...)
}

func (d *Daemon) skillIndex() protocol.SkillIndex {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.index
}

// Run serves until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	slog.Info("lanesh daemon starting", "version", version.Version, "machine", d.machineID, "skills", len(d.index.Skills))
	go skills.Watch(ctx, d.sources, d.skillIndex(), time.Minute, d.onSkillsChanged)

	t := time.NewTicker(d.opts.ReconcileInterval)
	defer t.Stop()
	for {
		d.reconcile(ctx)
		select {
		case <-ctx.Done():
			d.stopAll()
			d.jobs.Shutdown()
			_ = os.Remove(d.opts.Paths.State())
			return nil
		case <-t.C:
		}
	}
}

// reconcile starts and stops lane connections to match the config file.
func (d *Daemon) reconcile(ctx context.Context) {
	cfg, err := LoadConfig(d.opts.Paths)
	if err != nil {
		slog.Warn("reading config failed", "err", err)
		return
	}
	creds, err := LoadCredentials(d.opts.Paths)
	if err != nil {
		slog.Warn("reading credentials failed", "err", err)
		return
	}
	d.mu.Lock()
	globalChanged := cfg.MachineName != d.cfg.MachineName || !reflect.DeepEqual(cfg.Policy, d.cfg.Policy)
	d.cfg = cfg
	want := map[string]bool{}
	for _, lane := range cfg.Lanes {
		key := lane.Key()
		want[key] = true
		cred := creds[key]
		policy := cfg.EffectivePolicy(lane)
		if c := d.conns[key]; c != nil {
			if c.cred == cred && c.policy == policy && !globalChanged {
				continue
			}
			c.stop()
			delete(d.conns, key)
		}
		if cred == "" {
			slog.Warn("no credential for lane; run lanesh join again", "lane", key)
			continue
		}
		c := newLaneConn(d, lane, cred, policy)
		d.conns[key] = c
		go c.run(ctx)
	}
	for key, c := range d.conns {
		if !want[key] {
			c.stop()
			delete(d.conns, key)
		}
	}
	d.mu.Unlock()
	d.writeState()
}

func (d *Daemon) stopAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, c := range d.conns {
		c.stop()
		delete(d.conns, key)
	}
}

func (d *Daemon) connections() []*laneConn {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]*laneConn, 0, len(d.conns))
	for _, c := range d.conns {
		out = append(out, c)
	}
	return out
}

func (d *Daemon) onSkillsChanged(idx protocol.SkillIndex) {
	d.mu.Lock()
	d.index = idx
	d.mu.Unlock()
	slog.Info("skills changed", "count", len(idx.Skills))
	for _, c := range d.connections() {
		if c.policy.ExposeSkills {
			c.event(protocol.EventSkillsChanged, idx)
		}
	}
}

func (d *Daemon) onJobExit(e protocol.JobExitedEvent) {
	d.audit.write("job.exited", map[string]any{"job_id": e.JobID, "status": e.Status, "exit_code": e.ExitCode, "duration_ms": e.DurationMS})
	for _, c := range d.connections() {
		if c.ownsJob(e.JobID) {
			c.event(protocol.EventJobExited, e)
		}
	}
}

func (d *Daemon) machineName() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.cfg.MachineName != "" {
		return d.cfg.MachineName
	}
	return d.hostname
}

func (d *Daemon) environment() protocol.Environment {
	username := os.Getenv("USER")
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	return protocol.Environment{
		OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: d.hostname, User: username, Home: d.home,
		DefaultShell: runner.DefaultShell().Name, Shells: runner.AvailableShells(), Version: version.Version,
	}
}

// State is written to state.json for `lanesh status`.
type State struct {
	PID       int         `json:"pid"`
	Version   string      `json:"version"`
	MachineID string      `json:"machine_id"`
	StartedAt time.Time   `json:"started_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Skills    int         `json:"skills"`
	Lanes     []LaneState `json:"lanes"`
}

// LaneState is the connection state of one lane.
type LaneState struct {
	Server        string    `json:"server"`
	LaneID        string    `json:"lane_id"`
	LaneName      string    `json:"lane_name"`
	MachineName   string    `json:"machine_name"`
	Connected     bool      `json:"connected"`
	Since         time.Time `json:"since"`
	LastError     string    `json:"last_error,omitempty"`
	ServerVersion string    `json:"server_version,omitempty"`
}

var startedAt = time.Now()

func (d *Daemon) writeState() {
	st := State{
		PID: os.Getpid(), Version: version.Version, MachineID: d.machineID, StartedAt: startedAt,
		UpdatedAt: time.Now(), Skills: len(d.skillIndex().Skills), Lanes: []LaneState{},
	}
	for _, c := range d.connections() {
		st.Lanes = append(st.Lanes, c.state())
	}
	raw, _ := json.MarshalIndent(st, "", "  ")
	_ = writePrivate(d.opts.Paths.State(), raw, 0o600)
}

// ReadState reads the state file written by a running daemon.
func ReadState(p Paths) (State, error) {
	var st State
	raw, err := os.ReadFile(p.State())
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(raw, &st)
	return st, err
}
