// Package runner executes commands as jobs with captured, bounded output.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jtsang4/lanesh/internal/auth"
	"github.com/jtsang4/lanesh/internal/protocol"
)

// Defaults and limits.
const (
	DefaultWait      = 30 * time.Second
	MaxWait          = 10 * time.Minute
	DefaultMaxOutput = 6_500
	DefaultBuffer    = 16 << 20
	DefaultRetention = time.Hour
	DefaultReadLimit = 20 << 10
	MaxReadLimit     = 1 << 20
	MaxReadWait      = time.Minute
	DefaultCoalesce  = 300 * time.Millisecond
	DefaultIdleWait  = 1500 * time.Millisecond
	MaxIdleWait      = 15 * time.Second
	MaxRunning       = 64
	maxMatchScan     = 256 << 10
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
// output exceeds maxOut bytes (or an equivalent wide-character token budget).
func inline(r *ring, stream string, maxOut int) (text string, total int64, truncated bool) {
	data, total, dropped := r.Snapshot()
	if maxOut <= 0 {
		return "", total, len(data) > 0 || dropped > 0
	}
	limit := maxOut
	// Cap wide-character (e.g. CJK) output by estimated tokens too: ~30% of
	// byte budget corresponds to ~1,950 tokens for the 6,500-byte default,
	// avoiding client-side spill-to-disk on dense CJK output while leaving
	// ASCII output governed by the byte budget.
	if maxTokens := max(maxOut*3/10, 100); len(data) <= limit {
		if tok := estimateTokensBytes(data); tok > maxTokens {
			limit = max(len(data)*maxTokens/tok, 64)
		}
	}
	if len(data) <= limit && dropped == 0 {
		return toText(data), total, false
	}
	if len(data) <= limit {
		return fmt.Sprintf("[lanesh] %d earlier bytes were dropped from the job's buffer\n", dropped) + toText(data), total, true
	}
	head, tail := trimHeadTail(data, limit)
	omitted := total - dropped - int64(len(head)) - int64(len(tail))
	note := ""
	if dropped > 0 {
		note = fmt.Sprintf("[lanesh] %d earlier bytes were dropped from the job's buffer\n", dropped)
	}
	return fmt.Sprintf("%s%s\n[lanesh] %d bytes omitted here; read them with job_read stream=%s offset=%d\n%s",
		note, toText(head), omitted, stream, dropped+int64(len(head)), toText(tail)), total, true
}

func (m *Manager) status(j *job, maxOut int) protocol.JobStatus {
	status, code, dur := j.state()
	outBudget, errBudget := splitStreamBudget(j.stdout.Len(), j.stderr.Len(), maxOut)
	out, outTotal, outTrunc := inline(j.stdout, "stdout", outBudget)
	errText, errTotal, errTrunc := inline(j.stderr, "stderr", errBudget)
	return protocol.JobStatus{
		JobID: j.id, Status: status, ExitCode: code, Command: j.command, Cwd: j.cwd, Shell: j.shell,
		StartedAt: j.started.Unix(), DurationMS: dur.Milliseconds(),
		Stdout: out, Stderr: errText, StdoutBytes: outTotal, StderrBytes: errTotal,
		OutputTruncated: outTrunc || errTrunc, Owner: j.owner,
	}
}

// splitStreamBudget divides a combined byte budget across stdout and stderr,
// letting a busy stream borrow any unused share from a quiet one.
func splitStreamBudget(outAvail, errAvail, total int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if outAvail+errAvail <= total {
		return total, total
	}
	half := total / 2
	if outAvail < half {
		return outAvail, total - outAvail
	}
	if errAvail < half {
		return total - errAvail, errAvail
	}
	return half, total - half
}

// trimToTokens caps text bytes when wide characters (e.g. CJK) push estimated
// token count far above a safe inline threshold, preserving head and tail.
func trimHeadTail(data []byte, limit int) (head, tail []byte) {
	half := limit / 2
	head = cutRuneBoundary(data[:half])
	tail = data[len(data)-half:]
	for len(tail) > 0 && tail[0]&0xC0 == 0x80 {
		tail = tail[1:]
	}
	return head, tail
}

func estimateTokensBytes(b []byte) int {
	ascii, wide := 0, 0
	for len(b) > 0 {
		if b[0] < utf8.RuneSelf {
			ascii++
			b = b[1:]
		} else {
			_, n := utf8.DecodeRune(b)
			wide++
			b = b[n:]
		}
	}
	return ascii/4 + wide
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

// Read returns a range of a job's output stream ("stdout", "stderr", or
// "both"). WaitMS / WaitUntil / Match bound waiting up to MaxReadWait (60s).
func (m *Manager) Read(ctx context.Context, p protocol.JobReadParams) (protocol.JobReadResult, error) {
	j, err := m.get(p.JobID)
	if err != nil {
		return protocol.JobReadResult{}, err
	}
	stream := p.Stream
	if stream == "" {
		stream = "stdout"
	}
	switch stream {
	case "stdout", "stderr", "both":
	default:
		return protocol.JobReadResult{}, fmt.Errorf("unknown stream %q (want stdout, stderr, or both)", stream)
	}
	waitUntil := p.WaitUntil
	if waitUntil == "" {
		waitUntil = "output"
	}
	switch waitUntil {
	case "output", "idle", "exit":
	default:
		return protocol.JobReadResult{}, fmt.Errorf("unknown wait_until %q (want output, idle, or exit)", waitUntil)
	}
	var matchRe *regexp.Regexp
	if p.Match != "" {
		if matchRe, err = regexp.Compile(p.Match); err != nil {
			return protocol.JobReadResult{}, fmt.Errorf("invalid match pattern: %w", err)
		}
	}

	limit := p.Limit
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	limit = min(limit, MaxReadLimit)

	outOff := max(p.Offset, 0)
	errOff := max(p.StderrOffset, 0)
	if stream == "stderr" {
		errOff = outOff
	}

	wait := time.Duration(p.WaitMS) * time.Millisecond
	if wait <= 0 && (p.WaitUntil != "" || p.Match != "") {
		wait = DefaultWait
	}
	wait = min(wait, MaxReadWait)

	idleWait := DefaultIdleWait
	if p.IdleMS > 0 {
		idleWait = min(time.Duration(p.IdleMS)*time.Millisecond, MaxIdleWait)
	}

	matched := false
	if wait > 0 {
		matched = j.waitForRead(ctx, stream, outOff, errOff, wait, waitUntil, idleWait, limit, matchRe)
	} else if matchRe != nil {
		matched = j.matchesNew(stream, outOff, errOff, matchRe)
	}

	status, code, _ := j.state()
	if stream == "both" {
		_, _, outTotal, outDropped := j.stdout.ReadAt(outOff, 0)
		_, _, errTotal, errDropped := j.stderr.ReadAt(errOff, 0)
		outAvail := int(max(int64(0), outTotal-max(outOff, outDropped)))
		errAvail := int(max(int64(0), errTotal-max(errOff, errDropped)))
		outLim, errLim := splitStreamBudget(outAvail, errAvail, limit)
		outData, outStart, outTotal, outDropped := readLimited(j.stdout, outOff, outLim)
		errData, errStart, errTotal, errDropped := readLimited(j.stderr, errOff, errLim)
		outNext := outStart + int64(len(outData))
		errNext := errStart + int64(len(errData))
		return protocol.JobReadResult{
			JobID: j.id, Status: status, ExitCode: code, Stream: "both",
			Offset: outStart, NextOffset: outNext, Total: outTotal, Dropped: outDropped, Data: toText(outData),
			StderrOffset: errStart, StderrNextOffset: errNext, StderrTotal: errTotal, StderrDropped: errDropped, StderrData: toText(errData),
			Matched: matched,
			EOF:     status != protocol.JobRunning && outNext >= outTotal && errNext >= errTotal,
		}, nil
	}

	r := j.stdout
	off := outOff
	if stream == "stderr" {
		r = j.stderr
		off = errOff
	}
	data, start, total, dropped := readLimited(r, off, limit)
	next := start + int64(len(data))
	return protocol.JobReadResult{
		JobID: j.id, Status: status, ExitCode: code, Stream: stream,
		Offset: start, NextOffset: next, Total: total, Dropped: dropped,
		Data: toText(data), Matched: matched, EOF: status != protocol.JobRunning && next >= total,
	}, nil
}

func readLimited(r *ring, off int64, limit int) (data []byte, start, total, dropped int64) {
	if limit <= 0 {
		_, start, total, dropped = r.ReadAt(off, 0)
		return nil, start, total, dropped
	}
	data, start, total, dropped = r.ReadAt(off, limit)
	if start+int64(len(data)) < total {
		data = cutRuneBoundary(data)
	}
	return data, start, total, dropped
}

func (j *job) matchesNew(stream string, outOff, errOff int64, re *regexp.Regexp) bool {
	if stream == "stdout" || stream == "both" {
		if data, _, _, _ := j.stdout.ReadAt(outOff, maxMatchScan); len(data) > 0 && re.Match(data) {
			return true
		}
	}
	if stream == "stderr" || stream == "both" {
		if data, _, _, _ := j.stderr.ReadAt(errOff, maxMatchScan); len(data) > 0 && re.Match(data) {
			return true
		}
	}
	return false
}

func (j *job) waitForRead(ctx context.Context, stream string, outOff, errOff int64, wait time.Duration, waitUntil string, idleWait time.Duration, limit int, matchRe *regexp.Regexp) bool {
	watchOut := stream == "stdout" || stream == "both"
	watchErr := stream == "stderr" || stream == "both"

	totals := func() (outTotal, errTotal int64) {
		if watchOut {
			_, _, outTotal, _ = j.stdout.ReadAt(outOff, 0)
		}
		if watchErr {
			_, _, errTotal, _ = j.stderr.ReadAt(errOff, 0)
		}
		return outTotal, errTotal
	}
	newBytes := func(ot, et int64) int64 {
		var n int64
		if watchOut && ot > outOff {
			n += ot - outOff
		}
		if watchErr && et > errOff {
			n += et - errOff
		}
		return n
	}

	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	if matchRe != nil {
		for {
			if j.matchesNew(stream, outOff, errOff, matchRe) {
				return true
			}
			select {
			case <-j.done:
				return j.matchesNew(stream, outOff, errOff, matchRe)
			case <-ctx.Done():
				return false
			case <-deadline.C:
				return j.matchesNew(stream, outOff, errOff, matchRe)
			case <-tick.C:
			}
		}
	}

	if waitUntil == "exit" {
		select {
		case <-j.done:
		case <-ctx.Done():
		case <-deadline.C:
		}
		return false
	}

	if waitUntil == "idle" {
		lastOut, lastErr := outOff, errOff
		var lastChange time.Time
		for {
			ot, et := totals()
			if (watchOut && ot > lastOut) || (watchErr && et > lastErr) {
				lastOut, lastErr = ot, et
				lastChange = time.Now()
			}
			if !lastChange.IsZero() && (time.Since(lastChange) >= idleWait || newBytes(ot, et) >= int64(limit)) {
				return false
			}
			select {
			case <-j.done:
				return false
			case <-ctx.Done():
				return false
			case <-deadline.C:
				return false
			case <-tick.C:
			}
		}
	}

	// Default "output" mode: wait for first new byte, then coalesce briefly so
	// multi-line bursts arrive in one chunk instead of trickling byte-by-byte.
	var firstSeen time.Time
	for {
		ot, et := totals()
		nb := newBytes(ot, et)
		if nb > 0 {
			if nb >= int64(limit) {
				return false
			}
			if firstSeen.IsZero() {
				// Return immediately if the job has already exited.
				select {
				case <-j.done:
					return false
				default:
				}
				firstSeen = time.Now()
			} else if time.Since(firstSeen) >= DefaultCoalesce {
				return false
			}
		}
		select {
		case <-j.done:
			return false
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
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
