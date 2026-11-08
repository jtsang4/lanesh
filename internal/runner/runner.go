// Package runner executes commands as jobs with captured, bounded output.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jtsang4/lanesh/internal/auth"
	"github.com/jtsang4/lanesh/internal/protocol"
)

// Defaults and limits.
const (
	DefaultWait      = 30 * time.Second
	MaxWait          = 10 * time.Minute
	DefaultMaxOutput = 10_000
	DefaultBuffer    = 16 << 20
	DefaultRetention = time.Hour
	DefaultReadLimit = 20 << 10
	MaxReadLimit     = 1 << 20
	MaxReadWait      = time.Minute
	MaxRunning       = 64
)

// Errors returned by the manager.
var (
	ErrNotFound    = errors.New("job not found")
	ErrUnsupported = errors.New("pty is not supported yet")
	ErrNotRunning  = errors.New("job is not running")
	ErrTooMany     = fmt.Errorf("too many running jobs (limit %d); stop some with job_kill first", MaxRunning)
)

// Options configures a Manager.
type Options struct {
	// Env returns the base environment for new processes.
	Env func() []string
	// BufferSize is the per-stream output buffer size.
	BufferSize int
	// Retention is how long finished jobs are kept.
	Retention time.Duration
	// OnExit is called when a job finishes.
	OnExit func(protocol.JobExitedEvent)
}

// Manager owns running and recently finished jobs.
type Manager struct {
	opts Options
	mu   sync.Mutex
	jobs map[string]*job
}

// NewManager creates a Manager.
func NewManager(opts Options) *Manager {
	if opts.Env == nil {
		opts.Env = os.Environ
	}
	if opts.BufferSize <= 0 {
		opts.BufferSize = DefaultBuffer
	}
	if opts.Retention <= 0 {
		opts.Retention = DefaultRetention
	}
	return &Manager{opts: opts, jobs: map[string]*job{}}
}

type job struct {
	id, owner, lane, command, cwd, shell string
	cmd                                  *exec.Cmd
	started                              time.Time

	stdout, stderr *ring
	stdin          io.WriteCloser
	stdinMu        sync.Mutex
	group          *procGroup
	done           chan struct{}

	mu       sync.Mutex
	status   string
	exitCode *int
	ended    time.Time
	killed   bool
}

func (j *job) state() (status string, code *int, dur time.Duration) {
	j.mu.Lock()
	defer j.mu.Unlock()
	end := j.ended
	if end.IsZero() {
		end = time.Now()
	}
	return j.status, j.exitCode, end.Sub(j.started)
}

// Start launches params.Command in dir and waits until it exits or the wait
// time elapses, returning the job status with inline output.
func (m *Manager) Start(ctx context.Context, p protocol.ExecParams, dir string) (protocol.JobStatus, error) {
	if p.PTY {
		return protocol.JobStatus{}, ErrUnsupported
	}
	if strings.TrimSpace(p.Command) == "" {
		return protocol.JobStatus{}, errors.New("command is empty")
	}
	if m.running() >= MaxRunning {
		return protocol.JobStatus{}, ErrTooMany
	}
	sh, err := ResolveShell(p.Shell)
	if err != nil {
		return protocol.JobStatus{}, err
	}
	argv := sh.Args(p.Command)
	// Jobs outlive the request that started them, so they are not bound to ctx.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), argv[0], argv[1:]...) //nolint:gosec // Executing commands is the purpose of the daemon.
	cmd.Dir = dir
	cmd.Env = m.opts.Env()
	for k, v := range p.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	prepare(cmd)
	setCommandLine(cmd, sh, p.Command)
	// Descendants that leave the process group can keep the output pipes
	// open; stop waiting for them shortly after the command itself exits.
	cmd.WaitDelay = 5 * time.Second

	j := &job{
		id: auth.NewID("job_"), owner: p.Owner, lane: p.Lane, cmd: cmd, command: p.Command, cwd: dir, shell: sh.Name,
		stdout: newRing(m.opts.BufferSize), stderr: newRing(m.opts.BufferSize),
		done: make(chan struct{}), status: protocol.JobRunning,
	}
	cmd.Stdout, cmd.Stderr = j.stdout, j.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return protocol.JobStatus{}, err
	}
	j.stdin = stdin

	j.started = time.Now()
	if err := cmd.Start(); err != nil {
		return protocol.JobStatus{}, fmt.Errorf("start: %w", err)
	}
	if g, err := attach(cmd); err == nil {
		j.group = g
	}

	m.mu.Lock()
	m.gcLocked()
	m.jobs[j.id] = j
	m.mu.Unlock()

	go m.wait(j, cmd)

	// Stdin is written asynchronously: a command that does not read it must
	// not block the request beyond its timeout.
	go func() {
		if p.Stdin != "" {
			j.stdinMu.Lock()
			if j.stdin != nil {
				_, _ = io.WriteString(j.stdin, p.Stdin)
			}
			j.stdinMu.Unlock()
		}
		if !p.Background {
			// Foreground commands get EOF on stdin so that they cannot hang on input.
			j.closeStdin()
		}
	}()

	maxOut := p.MaxOutput
	if maxOut <= 0 {
		maxOut = DefaultMaxOutput
	}
	if p.Background {
		return m.status(j, maxOut), nil
	}
	wait := DefaultWait
	if p.TimeoutMS > 0 {
		wait = time.Duration(p.TimeoutMS) * time.Millisecond
	}
	wait = min(wait, MaxWait)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-j.done:
	case <-timer.C:
	case <-ctx.Done():
	}
	return m.status(j, maxOut), nil
}

func (j *job) closeStdin() {
	j.stdinMu.Lock()
	defer j.stdinMu.Unlock()
	if j.stdin != nil {
		_ = j.stdin.Close()
		j.stdin = nil
	}
}

func (m *Manager) wait(j *job, cmd *exec.Cmd) {
	err := cmd.Wait()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else if err != nil {
		code = -1
	}
	j.closeStdin()
	if j.group != nil {
		j.group.close()
	}
	j.mu.Lock()
	j.ended = time.Now()
	j.exitCode = &code
	if j.killed {
		j.status = protocol.JobKilled
	} else {
		j.status = protocol.JobExited
	}
	status, dur := j.status, j.ended.Sub(j.started)
	j.mu.Unlock()
	close(j.done)

	if m.opts.OnExit != nil {
		m.opts.OnExit(protocol.JobExitedEvent{
			JobID: j.id, Status: status, ExitCode: &code, DurationMS: dur.Milliseconds(),
			Command: j.command, Owner: j.owner, Lane: j.lane,
		})
	}
}

// inline renders a stream for an exec result, keeping head and tail when the
// output exceeds maxOut bytes.
func inline(r *ring, stream string, maxOut int) (text string, total int64, truncated bool) {
	data, total, dropped := r.Snapshot()
	if len(data) <= maxOut && dropped == 0 {
		return toText(data), total, false
	}
	if len(data) <= maxOut {
		return fmt.Sprintf("[lanesh] %d earlier bytes were dropped from the job's buffer\n", dropped) + toText(data), total, true
	}
	half := maxOut / 2
	head := cutRuneBoundary(data[:half])
	tail := data[len(data)-half:]
	for len(tail) > 0 && tail[0]&0xC0 == 0x80 {
		tail = tail[1:]
	}
	omitted := total - dropped - int64(len(head)) - int64(len(tail))
	hint := ""
	if stream != "stdout" {
		hint = " stream=" + stream
	}
	note := ""
	if dropped > 0 {
		note = fmt.Sprintf("[lanesh] %d earlier bytes were dropped from the job's buffer\n", dropped)
	}
	return fmt.Sprintf("%s%s\n[lanesh] %d bytes omitted here; read them with job_read%s offset=%d\n%s",
		note, toText(head), omitted, hint, dropped+int64(len(head)), toText(tail)), total, true
}

func (m *Manager) status(j *job, maxOut int) protocol.JobStatus {
	status, code, dur := j.state()
	out, outTotal, outTrunc := inline(j.stdout, "stdout", maxOut)
	errText, errTotal, errTrunc := inline(j.stderr, "stderr", maxOut)
	return protocol.JobStatus{
		JobID: j.id, Status: status, ExitCode: code, Command: j.command, Cwd: j.cwd, Shell: j.shell,
		StartedAt: j.started.Unix(), DurationMS: dur.Milliseconds(),
		Stdout: out, Stderr: errText, StdoutBytes: outTotal, StderrBytes: errTotal,
		OutputTruncated: outTrunc || errTrunc, Owner: j.owner,
	}
}

func (m *Manager) get(id string) (*job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	return j, nil
}

// Read returns a range of a job's output stream ("stdout" or "stderr"). With
// p.WaitMS it first waits until there is output beyond p.Offset or the job
// has finished.
func (m *Manager) Read(ctx context.Context, p protocol.JobReadParams) (protocol.JobReadResult, error) {
	j, err := m.get(p.JobID)
	if err != nil {
		return protocol.JobReadResult{}, err
	}
	if p.WaitMS > 0 {
		j.waitForOutput(ctx, p.Stream, p.Offset, min(time.Duration(p.WaitMS)*time.Millisecond, MaxReadWait))
	}
	stream := p.Stream
	if stream == "" {
		stream = "stdout"
	}
	var r *ring
	switch stream {
	case "stdout":
		r = j.stdout
	case "stderr":
		r = j.stderr
	default:
		return protocol.JobReadResult{}, fmt.Errorf("unknown stream %q", stream)
	}
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	limit = min(limit, MaxReadLimit)
	data, start, total, dropped := r.ReadAt(p.Offset, limit)
	if start+int64(len(data)) < total {
		data = cutRuneBoundary(data)
	}
	status, code, _ := j.state()
	next := start + int64(len(data))
	return protocol.JobReadResult{
		JobID: j.id, Status: status, ExitCode: code, Stream: stream,
		Offset: start, NextOffset: next, Total: total, Dropped: dropped,
		Data: toText(data), EOF: status != protocol.JobRunning && next >= total,
	}, nil
}

func (j *job) waitForOutput(ctx context.Context, stream string, offset int64, wait time.Duration) {
	r := j.stdout
	if stream == "stderr" {
		r = j.stderr
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, _, total, _ := r.ReadAt(offset, 0); total > offset {
			return
		}
		select {
		case <-j.done:
			return
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
		}
	}
}

// Write sends data to a job's stdin and optionally closes it.
func (m *Manager) Write(p protocol.JobWriteParams) error {
	j, err := m.get(p.JobID)
	if err != nil {
		return err
	}
	j.stdinMu.Lock()
	w := j.stdin
	if w == nil {
		j.stdinMu.Unlock()
		if p.Data == "" && p.CloseStdin {
			return nil
		}
		return errors.New("stdin is closed")
	}
	if p.Data != "" {
		if _, err := io.WriteString(w, p.Data); err != nil {
			j.stdinMu.Unlock()
			return err
		}
	}
	j.stdinMu.Unlock()
	if p.CloseStdin {
		j.closeStdin()
	}
	return nil
}

// Kill terminates a job's process tree.
func (m *Manager) Kill(p protocol.JobKillParams) (protocol.JobStatus, error) {
	j, err := m.get(p.JobID)
	if err != nil {
		return protocol.JobStatus{}, err
	}
	if status, _, _ := j.state(); status != protocol.JobRunning {
		return m.status(j, DefaultMaxOutput), nil
	}
	j.mu.Lock()
	j.killed = true
	j.mu.Unlock()
	hard := p.Signal == "" || p.Signal == "KILL" || p.Signal == "SIGKILL"
	switch {
	case j.group != nil && hard:
		_ = j.group.kill()
	case j.group != nil:
		_ = j.group.signal(p.Signal)
	default:
		_ = j.cmd.Process.Kill()
	}
	select {
	case <-j.done:
	case <-time.After(5 * time.Second):
		if j.group != nil {
			_ = j.group.kill()
		}
		_ = j.cmd.Process.Kill()
		select {
		case <-j.done:
		case <-time.After(10 * time.Second):
		}
	}
	return m.status(j, DefaultMaxOutput), nil
}

// Lane returns the lane a job belongs to.
func (m *Manager) Lane(id string) (string, bool) {
	j, err := m.get(id)
	if err != nil {
		return "", false
	}
	return j.lane, true
}

// KillLane kills the running jobs of a lane.
func (m *Manager) KillLane(lane string) {
	m.mu.Lock()
	var ids []string
	for id, j := range m.jobs {
		if j.lane == lane {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		_, _ = m.Kill(protocol.JobKillParams{JobID: id})
	}
}

// List returns a lane's jobs, newest first, optionally filtered by owner.
func (m *Manager) List(lane, owner string) []protocol.JobStatus {
	m.mu.Lock()
	m.gcLocked()
	jobs := make([]*job, 0, len(m.jobs))
	for _, j := range m.jobs {
		if j.lane == lane && (owner == "" || j.owner == owner) {
			jobs = append(jobs, j)
		}
	}
	m.mu.Unlock()
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].started.After(jobs[b].started) })
	out := make([]protocol.JobStatus, 0, len(jobs))
	for _, j := range jobs {
		st := m.status(j, 0)
		st.Stdout, st.Stderr, st.OutputTruncated = "", "", false
		out = append(out, st)
	}
	return out
}

// Shutdown kills all running jobs.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	jobs := make([]*job, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, j)
	}
	m.mu.Unlock()
	for _, j := range jobs {
		if status, _, _ := j.state(); status == protocol.JobRunning && j.group != nil {
			_ = j.group.kill()
		}
	}
}

func (m *Manager) running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, j := range m.jobs {
		if status, _, _ := j.state(); status == protocol.JobRunning {
			n++
		}
	}
	return n
}

func (m *Manager) gcLocked() {
	cutoff := time.Now().Add(-m.opts.Retention)
	for id, j := range m.jobs {
		j.mu.Lock()
		expired := !j.ended.IsZero() && j.ended.Before(cutoff)
		j.mu.Unlock()
		if expired {
			delete(m.jobs, id)
		}
	}
}
