package mcp

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jtsang4/lanesh/internal/files"
	"github.com/jtsang4/lanesh/internal/protocol"
)

// --- list_machines ---

func (t *Tools) listMachines(ctx context.Context, c caller, _ struct{}) (*text, error) {
	ms, err := t.backend.Machines(ctx, c.laneID)
	if err != nil {
		return nil, err
	}
	var out text
	if len(ms) == 0 {
		out.line("[lanesh] this lane has no machines yet.")
		return &out, nil
	}
	out.line("[lanesh] %s in this lane:", plural(len(ms), "machine", "machines"))
	for _, m := range ms {
		out.raw(machineLine(c, m))
	}
	return &out, nil
}

func machineLine(c caller, m Machine) string {
	name := m.Name
	if c.isSelf(m) {
		name += " (you)"
	}
	parts := []string{m.OS + "/" + m.Arch}
	if m.Shell != "" {
		parts = append(parts, m.Shell)
	}
	if m.Online {
		parts = append(parts, "online")
	} else {
		parts = append(parts, "offline since "+lastSeen(m))
	}
	if m.Policy.ExposeSkills {
		parts = append(parts, plural(m.SkillCount, "skill", "skills"))
	} else {
		parts = append(parts, "skills not shared")
	}
	if !m.Policy.AllowExec {
		parts = append(parts, "no commands or file access")
	}
	return "- " + name + ": " + strings.Join(parts, ", ")
}

// --- describe_machine ---

// DescribeInput selects a machine.
type DescribeInput struct {
	Machine string `json:"machine"`
}

func (t *Tools) describeMachine(ctx context.Context, c caller, in DescribeInput) (*text, error) {
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, err
	}
	d, err := t.describe(ctx, c, m)
	if err != nil {
		return nil, err
	}
	var out text
	token := t.briefings.Token(c.laneID, m.ID, m.Name, d.Skills.Hash)
	out.line("[lanesh] briefing for %q", m.Name)
	out.line("Briefing token: %s (pass it as briefing in calls for this machine)", token)
	writeMachine(&out, m, d)
	return &out, nil
}

// --- find_skills ---

// FindInput searches skills.
type FindInput struct {
	Query   string `json:"query,omitempty"`
	Machine string `json:"machine,omitempty" jsonschema:"limit to one machine"`
}

func (t *Tools) findSkills(ctx context.Context, c caller, in FindInput) (*text, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" && strings.TrimSpace(in.Machine) == "" {
		return nil, errors.New("give query, machine, or both")
	}
	ms, err := t.backend.Machines(ctx, c.laneID)
	if err != nil {
		return nil, err
	}
	if in.Machine != "" {
		m, err := t.backend.Resolve(ctx, c.laneID, in.Machine)
		if err != nil {
			return nil, err
		}
		ms = []Machine{m}
	}
	shared := ms[:0:0]
	for _, m := range ms {
		if m.Policy.ExposeSkills {
			shared = append(shared, m)
		}
	}
	if len(shared) == 0 {
		if in.Machine != "" {
			return nil, fmt.Errorf("machine %q does not share its skills", ms[0].Name)
		}
		return nil, errors.New("no machine in this lane shares skills")
	}
	matches, partial := findSkills(shared, t.backend.LaneSkills(ctx, c.laneID), query)

	var out text
	scope := "all machines"
	if in.Machine != "" {
		scope = fmt.Sprintf("%q", shared[0].Name)
		if !shared[0].Online {
			scope += " (offline; from its last known state)"
		}
	}
	switch {
	case query == "":
		out.line("[lanesh] %s on %s:", plural(len(matches), "skill", "skills"), scope)
	case len(matches) == 0:
		out.line("[lanesh] no skill on %s matches %q (keyword match on names and descriptions).", scope, query)
	case partial:
		out.line("[lanesh] no skill on %s matches most of %q; closest by keyword:", scope, query)
	default:
		out.line("[lanesh] keyword matches for %q on %s (%d):", query, scope, len(matches))
	}
	shown := 0
	for _, sm := range matches {
		line := "- " + sm.name
		if in.Machine == "" {
			line += " (" + machineList(sm) + ")"
		}
		line += ": " + shorten(sm.description, descMaxRunes)
		if out.tokens()+estimateTokens(line) > resultBudget {
			break
		}
		out.raw(line)
		shown++
	}
	if rest := len(matches) - shown; rest > 0 {
		if query == "" {
			out.line("[lanesh] %d more skills not shown (to fit the budget); add a query to narrow them.", rest)
		} else {
			out.line("[lanesh] %d more matches not shown (to fit the budget); refine the query.", rest)
		}
	}
	if query != "" {
		out.line("Keyword matching can miss skills. To choose reliably, use describe_machine, then read_skill.")
	}
	return &out, nil
}

func machineList(sm *skillMatch) string {
	names := make([]string, len(sm.machines))
	for i, n := range sm.machines {
		names[i] = n
		if sm.offline[n] {
			names[i] += " (offline)"
		}
	}
	s := "on " + strings.Join(names, ", ")
	if len(sm.machines) > 1 {
		if len(sm.hashes) > 1 {
			s += "; versions differ"
		} else {
			s += "; identical"
		}
	}
	return s
}

// --- read_skill ---

// ReadSkillInput selects what to read from a skill.
type ReadSkillInput struct {
	Machine  string   `json:"machine"`
	Briefing string   `json:"briefing" jsonschema:"briefing token"`
	Skill    string   `json:"skill"`
	Path     string   `json:"path,omitempty" jsonschema:"relative file or dir/"`
	Paths    []string `json:"paths,omitempty" jsonschema:"whole files (no offset/limit)"`
	Offset   int64    `json:"offset,omitempty" jsonschema:"byte offset"`
	Limit    int64    `json:"limit,omitempty" jsonschema:"max bytes (default 20480)"`
}

// maxPaths caps read_skill paths per call.
const maxPaths = 20

func (t *Tools) readSkill(ctx context.Context, c caller, in ReadSkillInput) (*text, error) {
	m, notice, err := t.briefed(ctx, c, "read_skill", "briefing", in.Machine, in.Briefing, in.Skill)
	if err != nil {
		return nil, err
	}
	if err := requirePolicy(m, false); err != nil {
		return nil, err
	}
	if in.Skill == "" {
		return nil, errors.New("skill is required")
	}
	read := func(p protocol.SkillReadParams) (protocol.SkillReadResult, error) {
		p.Skill = in.Skill
		var res protocol.SkillReadResult
		err := t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodSkillRead, p, &res)
		return res, protoErr(err)
	}
	var out text
	if notice != "" {
		out.raw(notice)
	}
	if len(in.Paths) > 0 {
		if in.Path != "" || in.Offset != 0 || in.Limit != 0 {
			return nil, errors.New("paths reads whole files from the start; to read part of a file, use path with offset/limit")
		}
		return t.readSkillFiles(&out, m, in, read)
	}

	limit := in.Limit
	if limit <= 0 {
		limit = fileReadBudget
	}
	res, err := read(protocol.SkillReadParams{Path: in.Path, Offset: in.Offset, Limit: limit})
	if err != nil {
		return nil, err
	}
	switch {
	case res.IsDir:
		where := "/" + res.Path
		if res.Path != "" {
			where = res.Path + "/"
		}
		out.line("[lanesh] skill %q on %q, directory %s (skill directory %s)", res.Skill, m.Name, where, res.Dir)
		writeTree(&out, res.Path, res.Files, res.FilesTruncated, maxTreeLines, "grep")
	case in.Path == "":
		out.line("[lanesh] skill %q on %q: SKILL.md and its files (skill directory %s)", res.Skill, m.Name, res.Dir)
		writeFile(&out, res.Path, res.Content, res.Encoding, res.Offset, res.Size, false)
		out.line("Files:")
		writeTree(&out, "", res.Files, res.FilesTruncated, skillTreeLines, "grep")
	default:
		out.line("[lanesh] skill %q on %q (skill directory %s)", res.Skill, m.Name, res.Dir)
		writeFile(&out, res.Path, res.Content, res.Encoding, res.Offset, res.Size, false)
	}
	return &out, nil
}

// readSkillFiles reads several files within one fileReadBudget.
func (t *Tools) readSkillFiles(out *text, m Machine, in ReadSkillInput, read func(protocol.SkillReadParams) (protocol.SkillReadResult, error)) (*text, error) {
	paths := in.Paths
	var skipped []string
	if len(paths) > maxPaths {
		skipped = append(skipped, paths[maxPaths:]...)
		paths = paths[:maxPaths]
	}
	out.line("[lanesh] skill %q on %q: %s", in.Skill, m.Name, plural(len(paths), "file", "files"))
	budget := int64(fileReadBudget)
	for i, p := range paths {
		if budget <= 0 {
			skipped = append(skipped, paths[i:]...)
			break
		}
		res, err := read(protocol.SkillReadParams{Path: p, Limit: budget})
		switch {
		case err != nil:
			out.line("[lanesh] error: %s", err)
			continue
		case res.IsDir:
			out.line("[lanesh] %s is a directory; list it with path=%q", p, strings.TrimSuffix(p, "/")+"/")
			continue
		}
		writeFile(out, res.Path, res.Content, res.Encoding, res.Offset, res.Size, false)
		budget -= int64(len(res.Content))
	}
	if len(skipped) > 0 {
		out.line("[lanesh] not read, to fit the budget: %s. Read them in another call.", strings.Join(skipped, ", "))
	}
	return out, nil
}

// writeFile writes a file section. Binary content arrives as base64; it is
// shown only with showBinary, otherwise just described.
func writeFile(out *text, name, content, encoding string, offset, total int64, showBinary bool) {
	n := int64(len(content))
	if encoding == protocol.EncodingBase64 {
		raw := n/4*3 - int64(strings.Count(content[max(0, len(content)-2):], "="))
		if !showBinary {
			out.line("[lanesh] %s is binary (%s); not shown. Copy it with transfer if you need it.", name, size(total))
			return
		}
		n = raw
	}
	span := ""
	end := offset + n
	if offset > 0 || end < total {
		span = fmt.Sprintf(", bytes %d-%d", offset, end)
	}
	out.line("[lanesh] begin %s (%s%s)", name, size(total), span)
	if encoding == protocol.EncodingBase64 {
		out.line("(base64)")
	}
	out.raw(content)
	out.line("[lanesh] end %s", name)
	if end < total {
		out.line("[lanesh] %s continues: %d more bytes; read on with offset=%d", name, total-end, end)
	}
}

func writeTree(out *text, root string, entries []protocol.FileEntry, truncated bool, maxLines int, grepTool string) {
	lines, collapsed, omitted := renderTree(root, entries, maxLines)
	if len(lines) == 0 {
		out.line("(empty)")
	}
	for _, l := range lines {
		out.raw(l)
	}
	if len(collapsed) > 0 {
		out.line("[lanesh] %s collapsed to fit; list one with its path, e.g. path=%q.", plural(len(collapsed), "directory", "directories"), collapsed[0])
	}
	if omitted > 0 {
		out.line("[lanesh] %d more entries not shown; use %s or read files by path.", omitted, grepTool)
	}
	if truncated {
		out.line("[lanesh] the listing stopped at %d entries; list a subdirectory to see the rest.", len(entries))
	}
}

// --- exec ---

// ExecInput runs a command.
type ExecInput struct {
	Machine    string            `json:"machine"`
	Briefing   string            `json:"briefing" jsonschema:"briefing token"`
	Command    string            `json:"command"`
	Cwd        string            `json:"cwd,omitempty" jsonschema:"working dir (relative to skill or home)"`
	Skill      string            `json:"skill,omitempty" jsonschema:"run in this skill's dir"`
	Shell      string            `json:"shell,omitempty" jsonschema:"override shell (bash, pwsh, cmd)"`
	Env        map[string]string `json:"env,omitempty"`
	Stdin      string            `json:"stdin,omitempty"`
	TimeoutMS  int               `json:"timeout_ms,omitempty" jsonschema:"wait ms (default 30000, max 600000)"`
	Background bool              `json:"background,omitempty" jsonschema:"return job_id immediately"`
}

func (t *Tools) exec(ctx context.Context, c caller, in ExecInput) (*text, error) {
	m, notice, err := t.briefed(ctx, c, "exec", "briefing", in.Machine, in.Briefing, in.Skill)
	if err != nil {
		return nil, err
	}
	if err := requirePolicy(m, true); err != nil {
		return nil, err
	}
	var st protocol.JobStatus
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodExecStart, protocol.ExecParams{
		Command: in.Command, Cwd: in.Cwd, Skill: in.Skill, Shell: in.Shell, Env: in.Env, Stdin: in.Stdin,
		TimeoutMS: in.TimeoutMS, Background: in.Background, Owner: c.keyID, MaxOutput: execMaxOutput,
	}, &st)
	detail := map[string]any{"command": truncate(in.Command, 1000), "cwd": in.Cwd, "skill": in.Skill}
	if err == nil {
		detail["job_id"], detail["status"] = st.JobID, st.Status
		if st.ExitCode != nil {
			detail["exit_code"] = *st.ExitCode
		}
	}
	t.audit(ctx, c, "exec", m, detail, err)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	if notice != "" {
		out.raw(notice)
	}
	head := fmt.Sprintf("[lanesh] on %q: %s", m.Name, jobState(st.Status, st.ExitCode, st.DurationMS))
	if st.Status == protocol.JobRunning || st.OutputTruncated {
		head += " (job " + st.JobID + ")"
	}
	out.raw(head)
	out.line("cwd %s, shell %s", st.Cwd, st.Shell)
	writeStream(&out, "stdout", st.Stdout, st.StdoutBytes)
	writeStream(&out, "stderr", st.Stderr, st.StderrBytes)
	if st.Status == protocol.JobRunning {
		out.line("[lanesh] follow with job_read job_id=%q stream=\"both\" offset=%d stderr_offset=%d wait_ms=30000 (or wait_until=\"idle\"|\"exit\"); stop with job_kill.",
			st.JobID, st.StdoutBytes, st.StderrBytes)
	}
	return &out, nil
}

// jobState describes a job's state, such as "exited 0 in 1.2s".
func jobState(status string, code *int, durMS int64) string {
	dur := time.Duration(durMS) * time.Millisecond
	if dur < time.Second {
		dur = dur.Round(time.Millisecond)
	} else {
		dur = dur.Round(100 * time.Millisecond)
	}
	switch {
	case status == protocol.JobRunning:
		return "still running after " + dur.String()
	case status == protocol.JobExited && code != nil:
		return fmt.Sprintf("exited %d in %s", *code, dur)
	case status == protocol.JobLost:
		return "lost (the daemon restarted) after " + dur.String()
	}
	return status + " after " + dur.String()
}

func writeStream(out *text, name, data string, total int64) {
	if total == 0 {
		out.line("%s: empty", name)
		return
	}
	out.line("[lanesh] begin %s (%s)", name, size(total))
	out.raw(data)
	out.line("[lanesh] end %s", name)
}

// --- jobs ---

// JobReadInput reads job output.
type JobReadInput struct {
	Machine      string `json:"machine"`
	JobID        string `json:"job_id"`
	Stream       string `json:"stream,omitempty" jsonschema:"stdout (default), stderr, or both"`
	Offset       int64  `json:"offset,omitempty" jsonschema:"byte offset (stdout if both)"`
	StderrOffset int64  `json:"stderr_offset,omitempty" jsonschema:"stderr byte offset (if both)"`
	Limit        int    `json:"limit,omitempty" jsonschema:"max bytes (default 20480)"`
	WaitMS       int    `json:"wait_ms,omitempty" jsonschema:"max wait ms (cap 60000)"`
	WaitUntil    string `json:"wait_until,omitempty" jsonschema:"output (default), idle, or exit"`
	IdleMS       int    `json:"idle_ms,omitempty" jsonschema:"quiet ms for idle (default 1500)"`
	Match        string `json:"match,omitempty" jsonschema:"Go regex to wake on"`
}

func (t *Tools) jobRead(ctx context.Context, c caller, in JobReadInput) (*text, error) {
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, err
	}
	usesV2 := in.Stream == "both" || in.StderrOffset != 0 || in.WaitUntil != "" || in.IdleMS != 0 || in.Match != ""
	if usesV2 && !m.HasFeature(protocol.FeatureJobReadWait) {
		return nil, errors.New(olderDaemonMsg)
	}
	in.Offset = max(in.Offset, 0)
	in.StderrOffset = max(in.StderrOffset, 0)
	var res protocol.JobReadResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodJobRead,
		protocol.JobReadParams{
			JobID: in.JobID, Stream: in.Stream, Offset: in.Offset, StderrOffset: in.StderrOffset,
			Limit: cmp.Or(in.Limit, fileReadBudget), WaitMS: in.WaitMS,
			WaitUntil: in.WaitUntil, IdleMS: in.IdleMS, Match: in.Match,
		}, &res)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	state := res.Status
	if res.Status == protocol.JobExited && res.ExitCode != nil {
		state = fmt.Sprintf("exited %d", *res.ExitCode)
	}
	matched := ""
	if res.Matched {
		matched = ", matched"
	}
	if res.Stream == "both" {
		out.line("[lanesh] job %s on %q (%s%s): stdout bytes %d-%d of %d, stderr bytes %d-%d of %d",
			res.JobID, m.Name, state, matched,
			res.Offset, res.NextOffset, res.Total,
			res.StderrOffset, res.StderrNextOffset, res.StderrTotal)
		if res.Offset > in.Offset {
			out.line("[lanesh] stdout bytes %d-%d were dropped from the job's buffer and cannot be read.", in.Offset, res.Offset)
		}
		if res.StderrOffset > in.StderrOffset {
			out.line("[lanesh] stderr bytes %d-%d were dropped from the job's buffer and cannot be read.", in.StderrOffset, res.StderrOffset)
		}
		if res.Data != "" {
			out.line("[lanesh] begin stdout")
			out.raw(res.Data)
			out.line("[lanesh] end stdout")
		}
		if res.StderrData != "" {
			out.line("[lanesh] begin stderr")
			out.raw(res.StderrData)
			out.line("[lanesh] end stderr")
		}
		switch {
		case res.NextOffset < res.Total || res.StderrNextOffset < res.StderrTotal:
			out.line("[lanesh] more output available; continue with stream=\"both\" offset=%d stderr_offset=%d", res.NextOffset, res.StderrNextOffset)
		case res.EOF:
			out.line("[lanesh] end of output; the job has finished.")
		default:
			out.line("[lanesh] no more output yet; the job is running. Continue with stream=\"both\" offset=%d stderr_offset=%d (wait_ms/wait_until waits for more).",
				res.NextOffset, res.StderrNextOffset)
		}
		return &out, nil
	}

	out.line("[lanesh] job %s on %q (%s%s): %s bytes %d-%d of %d", res.JobID, m.Name, state, matched, res.Stream, res.Offset, res.NextOffset, res.Total)
	if res.Offset > in.Offset {
		out.line("[lanesh] bytes %d-%d were dropped from the job's buffer and cannot be read.", in.Offset, res.Offset)
	}
	if res.Data != "" {
		out.line("[lanesh] begin %s", res.Stream)
		out.raw(res.Data)
		out.line("[lanesh] end %s", res.Stream)
	}
	switch {
	case res.NextOffset < res.Total:
		out.line("[lanesh] %d more bytes; continue with offset=%d", res.Total-res.NextOffset, res.NextOffset)
	case res.EOF:
		out.line("[lanesh] end of %s; the job has finished.", res.Stream)
	default:
		out.line("[lanesh] no more %s yet; the job is running. Continue with offset=%d (wait_ms waits for more).", res.Stream, res.NextOffset)
	}
	return &out, nil
}

// JobKillInput stops a job.
type JobKillInput struct {
	Machine string `json:"machine"`
	JobID   string `json:"job_id"`
	Signal  string `json:"signal,omitempty" jsonschema:"TERM, INT, or KILL (default)"`
}

func (t *Tools) jobKill(ctx context.Context, c caller, in JobKillInput) (*text, error) {
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, err
	}
	var st protocol.JobStatus
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodJobKill, protocol.JobKillParams{JobID: in.JobID, Signal: in.Signal}, &st)
	t.audit(ctx, c, "job.kill", m, map[string]any{"job_id": in.JobID, "signal": in.Signal}, err)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	out.line("[lanesh] job %s: %s", st.JobID, jobState(st.Status, st.ExitCode, st.DurationMS))
	return &out, nil
}

// JobListInput lists jobs.
type JobListInput struct {
	Machine         string `json:"machine"`
	All             bool   `json:"all,omitempty" jsonschema:"all access keys"`
	IncludeFinished bool   `json:"include_finished,omitempty" jsonschema:"include completed"`
	Limit           int    `json:"limit,omitempty" jsonschema:"default 20, max 100"`
}

func (t *Tools) jobList(ctx context.Context, c caller, in JobListInput) (*text, error) {
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, err
	}
	owner := c.keyID
	if in.All {
		owner = ""
	}
	var res protocol.JobListResult
	if err := t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodJobList, protocol.JobListParams{Owner: owner}, &res); err != nil {
		return nil, protoErr(err)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 100)

	var filtered []protocol.JobStatus
	finishedOmitted := 0
	for _, j := range res.Jobs {
		if !in.IncludeFinished && j.Status != protocol.JobRunning {
			finishedOmitted++
			continue
		}
		filtered = append(filtered, j)
	}
	capped := 0
	if len(filtered) > limit {
		capped = len(filtered) - limit
		filtered = filtered[:limit]
	}

	var out text
	label := plural(len(filtered), "running job", "running jobs")
	if in.IncludeFinished {
		label = plural(len(filtered), "job", "jobs")
	}
	out.line("[lanesh] %s on %q:", label, m.Name)
	for _, j := range filtered {
		cmdLine := strings.Join(strings.Fields(j.Command), " ")
		out.line("- %s: %s; %s (cwd %s)", j.JobID, jobState(j.Status, j.ExitCode, j.DurationMS), shorten(cmdLine, 160), j.Cwd)
	}
	if capped > 0 {
		out.line("[lanesh] %d more jobs not shown; raise limit (up to 100).", capped)
	}
	if finishedOmitted > 0 {
		out.line("[lanesh] %s omitted; pass include_finished=true to see them.", plural(finishedOmitted, "finished job", "finished jobs"))
	}
	return &out, nil
}

// --- files ---

// ReadFileInput reads a file.
type ReadFileInput struct {
	Machine   string `json:"machine"`
	Briefing  string `json:"briefing" jsonschema:"briefing token"`
	Path      string `json:"path" jsonschema:"abs or home-relative"`
	StartLine int    `json:"start_line,omitempty" jsonschema:"1-based start line"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"1-based end line (inclusive)"`
	Offset    int64  `json:"offset,omitempty" jsonschema:"byte offset"`
	Limit     int64  `json:"limit,omitempty" jsonschema:"max bytes (default 20480)"`
}

func (t *Tools) readFile(ctx context.Context, c caller, in ReadFileInput) (*text, error) {
	m, notice, err := t.briefed(ctx, c, "read_file", "briefing", in.Machine, in.Briefing, "")
	if err != nil {
		return nil, err
	}
	if err := requirePolicy(m, true); err != nil {
		return nil, err
	}
	byLines := in.StartLine != 0 || in.EndLine != 0
	if byLines && !m.HasFeature(protocol.FeatureFileLines) {
		return nil, errors.New(olderDaemonMsg)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = fileReadBudget
	}
	var res protocol.FileReadResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodFileRead,
		protocol.FileReadParams{
			Path: in.Path, Offset: in.Offset, Limit: limit,
			StartLine: in.StartLine, EndLine: in.EndLine,
		}, &res)
	t.audit(ctx, c, "file.read", m, map[string]any{"path": in.Path, "bytes": len(res.Content)}, err)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	if notice != "" {
		out.raw(notice)
	}
	if res.MIMEType != "" {
		if res.Truncated {
			out.line("[lanesh] image %s on %q is %s (%s), above the %s inline image limit; copy it with transfer if you need it.",
				res.Path, m.Name, size(res.Size), res.MIMEType, size(files.MaxImageInline))
			return &out, nil
		}
		raw, err := base64.StdEncoding.DecodeString(res.Content)
		if err != nil {
			return nil, fmt.Errorf("decode image: %w", err)
		}
		out.line("[lanesh] image on %q: %s (%s, %s)", m.Name, res.Path, size(res.Size), res.MIMEType)
		out.addImage(raw, res.MIMEType)
		return &out, nil
	}
	if byLines {
		out.line("[lanesh] file on %q", m.Name)
		writeFileLines(&out, res)
		return &out, nil
	}
	out.line("[lanesh] file on %q", m.Name)
	writeFile(&out, res.Path, res.Content, res.Encoding, res.Offset, res.Size, true)
	return &out, nil
}

func writeFileLines(out *text, res protocol.FileReadResult) {
	if res.EndLine == 0 {
		if res.TotalLines == 0 {
			out.line("[lanesh] %s is empty (0 lines).", res.Path)
		} else {
			out.line("[lanesh] %s has %d lines; start_line=%d is past the end.", res.Path, res.TotalLines, res.StartLine)
		}
		return
	}
	span := fmt.Sprintf("lines %d-%d", res.StartLine, res.EndLine)
	if res.TotalLines > 0 {
		span = fmt.Sprintf("lines %d-%d of %d", res.StartLine, res.EndLine, res.TotalLines)
	}
	out.line("[lanesh] begin %s (%s, %s)", res.Path, size(res.Size), span)
	content := strings.TrimSuffix(res.Content, "\n")
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		out.line("%d: %s", res.StartLine+i, strings.TrimSuffix(line, "\r"))
	}
	out.line("[lanesh] end %s", res.Path)
	if res.Truncated || (res.TotalLines > 0 && res.EndLine < res.TotalLines) {
		if res.TotalLines > 0 {
			out.line("[lanesh] %s continues: %d more lines; read on with start_line=%d", res.Path, res.TotalLines-res.EndLine, res.EndLine+1)
		} else {
			out.line("[lanesh] %s continues; read on with start_line=%d", res.Path, res.EndLine+1)
		}
	}
}

// EditFileInput edits a UTF-8 file by unique replacement(s) or writes whole content.
type EditFileInput struct {
	Machine   string                `json:"machine"`
	Briefing  string                `json:"briefing" jsonschema:"briefing token"`
	Path      string                `json:"path" jsonschema:"abs or home-relative"`
	OldText   string                `json:"old_text,omitempty" jsonschema:"unique text to replace"`
	NewText   string                `json:"new_text,omitempty" jsonschema:"replacement"`
	Edits     []protocol.FileEditOp `json:"edits,omitempty" jsonschema:"atomic [{old_text, new_text}]"`
	Content   string                `json:"content,omitempty" jsonschema:"write full file (creates dirs)"`
	Overwrite bool                  `json:"overwrite,omitempty" jsonschema:"with content, replace existing"`
}

func (t *Tools) editFile(ctx context.Context, c caller, in EditFileInput) (*text, error) {
	m, notice, err := t.briefed(ctx, c, "edit_file", "briefing", in.Machine, in.Briefing, "")
	if err != nil {
		return nil, err
	}
	if err := requirePolicy(m, true); err != nil {
		return nil, err
	}
	hasEdits := len(in.Edits) > 0 || in.OldText != "" || in.NewText != ""
	if in.Content != "" || (in.Overwrite && !hasEdits) {
		if hasEdits {
			return nil, errors.New("use content, or old_text/new_text/edits, not both")
		}
		var res protocol.FileWriteResult
		err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodFileWrite, protocol.FileWriteParams{
			Path: in.Path, Content: in.Content, Overwrite: in.Overwrite, CreateDirs: true,
		}, &res)
		t.audit(ctx, c, "file.write", m, map[string]any{"path": in.Path, "bytes": res.Size, "overwrite": in.Overwrite}, err)
		if err != nil {
			return nil, protoErr(err)
		}
		var out text
		if notice != "" {
			out.raw(notice)
		}
		out.line("[lanesh] wrote %s to %s on %q.", size(res.Size), res.Path, m.Name)
		return &out, nil
	}
	if in.Overwrite {
		return nil, errors.New("overwrite is only used with content")
	}
	if len(in.Edits) > 0 && (in.OldText != "" || in.NewText != "") {
		return nil, errors.New("use old_text/new_text, or edits, not both")
	}
	ops := in.Edits
	if len(ops) == 0 {
		if in.OldText == "" {
			return nil, errors.New("give old_text/new_text, edits, or content")
		}
		ops = []protocol.FileEditOp{{OldText: in.OldText, NewText: in.NewText}}
	}
	var res protocol.FileEditResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodFileEdit,
		protocol.FileEditParams{Path: in.Path, Edits: ops}, &res)
	t.audit(ctx, c, "file.edit", m, map[string]any{"path": in.Path, "edits": len(ops), "bytes": res.Size}, err)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	if notice != "" {
		out.raw(notice)
	}
	if res.Diff == "" {
		out.line("[lanesh] %s on %q unchanged (%s).", res.Path, m.Name, size(res.Size))
		return &out, nil
	}
	out.line("[lanesh] edited %s on %q (%s applied, %s):", res.Path, m.Name, plural(res.Applied, "edit", "edits"), size(res.Size))
	out.line("[lanesh] begin diff")
	out.raw(res.Diff)
	out.line("[lanesh] end diff")
	return &out, nil
}

// ListDirInput lists a directory tree.
type ListDirInput struct {
	Machine       string `json:"machine"`
	Briefing      string `json:"briefing" jsonschema:"briefing token"`
	Path          string `json:"path,omitempty" jsonschema:"default: home"`
	MaxDepth      int    `json:"max_depth,omitempty" jsonschema:"default 4, max 16"`
	IncludeHidden bool   `json:"include_hidden,omitempty"`
}

func (t *Tools) listDir(ctx context.Context, c caller, in ListDirInput) (*text, error) {
	m, notice, err := t.briefed(ctx, c, "list_dir", "briefing", in.Machine, in.Briefing, "")
	if err != nil {
		return nil, err
	}
	if err := requirePolicy(m, true); err != nil {
		return nil, err
	}
	var res protocol.FileListResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodFileList,
		protocol.FileListParams{Path: in.Path, MaxDepth: in.MaxDepth, IncludeHidden: in.IncludeHidden}, &res)
	t.audit(ctx, c, "file.list", m, map[string]any{"path": in.Path, "entries": len(res.Entries)}, err)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	if notice != "" {
		out.raw(notice)
	}
	out.line("[lanesh] directory %s on %q:", res.Path, m.Name)
	writeTree(&out, "", res.Entries, res.Truncated, maxTreeLines, "grep")
	return &out, nil
}

// GrepInput searches text files under a path or inside a skill.
type GrepInput struct {
	Machine       string `json:"machine"`
	Briefing      string `json:"briefing" jsonschema:"briefing token"`
	Pattern       string `json:"pattern"`
	Path          string `json:"path,omitempty" jsonschema:"file/dir (relative to skill or home)"`
	Skill         string `json:"skill,omitempty" jsonschema:"search inside this skill"`
	Regex         bool   `json:"regex,omitempty" jsonschema:"Go regex"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	Glob          string `json:"glob,omitempty" jsonschema:"filename glob, e.g. *.go"`
	MaxMatches    int    `json:"max_matches,omitempty" jsonschema:"default 100, max 500"`
}

func (t *Tools) grep(ctx context.Context, c caller, in GrepInput) (*text, error) {
	m, notice, err := t.briefed(ctx, c, "grep", "briefing", in.Machine, in.Briefing, in.Skill)
	if err != nil {
		return nil, err
	}
	if in.Pattern == "" {
		return nil, errors.New("pattern is required")
	}
	var (
		header        string
		matches       []protocol.GrepMatch
		filesMatched  int
		filesSearched int
		truncated     bool
		skipped       []string
	)
	if in.Skill != "" {
		if err := requirePolicy(m, false); err != nil {
			return nil, err
		}
		var res protocol.SkillGrepResult
		err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodSkillGrep, protocol.SkillGrepParams{
			Skill: in.Skill, Pattern: in.Pattern, Path: in.Path, Regex: in.Regex,
			IgnoreCase: !in.CaseSensitive, Glob: in.Glob, MaxMatches: in.MaxMatches,
		}, &res)
		if err != nil {
			return nil, protoErr(err)
		}
		header = fmt.Sprintf("[lanesh] %q in skill %q on %q: %s in %d of %s", in.Pattern, res.Skill, m.Name,
			plural(len(res.Matches), "match", "matches"), res.FilesMatched, plural(res.FilesSearched, "file", "files"))
		matches, filesMatched, filesSearched, truncated, skipped = res.Matches, res.FilesMatched, res.FilesSearched, res.Truncated, res.Skipped
	} else {
		if err := requirePolicy(m, true); err != nil {
			return nil, err
		}
		var res protocol.FileGrepResult
		err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodFileGrep, protocol.FileGrepParams{
			Path: in.Path, Pattern: in.Pattern, Regex: in.Regex, IgnoreCase: !in.CaseSensitive,
			Glob: in.Glob, MaxMatches: in.MaxMatches,
		}, &res)
		t.audit(ctx, c, "file.grep", m, map[string]any{"path": in.Path, "matches": len(res.Matches)}, err)
		if err != nil {
			return nil, protoErr(err)
		}
		header = fmt.Sprintf("[lanesh] %q in %s on %q: %s in %d of %s", in.Pattern, res.Path, m.Name,
			plural(len(res.Matches), "match", "matches"), res.FilesMatched, plural(res.FilesSearched, "file", "files"))
		matches, filesMatched, filesSearched, truncated, skipped = res.Matches, res.FilesMatched, res.FilesSearched, res.Truncated, res.Skipped
	}
	_, _ = filesMatched, filesSearched

	var out text
	if notice != "" {
		out.raw(notice)
	}
	out.raw(header)
	shown := 0
	for _, g := range matches {
		line := fmt.Sprintf("%s:%d: %s", g.Path, g.Line, g.Text)
		if out.tokens()+estimateTokens(line) > resultBudget {
			break
		}
		out.raw(line)
		shown++
	}
	if rest := len(matches) - shown; rest > 0 {
		out.line("[lanesh] %d more matches not shown (to fit the budget); narrow pattern or path.", rest)
	}
	if truncated {
		out.line("[lanesh] the search stopped at %d matches; there are more. Narrow pattern or path, or raise max_matches (up to 500).", len(matches))
	}
	if len(skipped) > 0 {
		out.line("[lanesh] not searched: %s", strings.Join(skipped, ", "))
	}
	return &out, nil
}

// TransferInput copies between machines.
type TransferInput struct {
	FromMachine  string `json:"from_machine"`
	FromBriefing string `json:"from_briefing,omitempty" jsonschema:"optional for (you)"`
	FromPath     string `json:"from_path"`
	ToMachine    string `json:"to_machine"`
	ToBriefing   string `json:"to_briefing,omitempty" jsonschema:"optional for (you)"`
	ToPath       string `json:"to_path"`
	Overwrite    bool   `json:"overwrite,omitempty"`
}

func (t *Tools) briefedTransferSide(ctx context.Context, c caller, param, ref, token string) (Machine, string, error) {
	m, err := t.online(ctx, c, ref)
	if err != nil {
		return Machine{}, "", err
	}
	if c.isSelf(m) {
		return m, "", nil
	}
	return t.briefed(ctx, c, "transfer", param, ref, token, "")
}

func (t *Tools) transfer(ctx context.Context, c caller, in TransferInput) (*text, error) {
	// Check both machines so that one reply carries every briefing needed.
	from, fromNotice, fromErr := t.briefedTransferSide(ctx, c, "from_briefing", in.FromMachine, in.FromBriefing)
	to, toNotice, toErr := t.briefedTransferSide(ctx, c, "to_briefing", in.ToMachine, in.ToBriefing)
	if err := joinBriefings(fromErr, toErr); err != nil {
		return nil, err
	}
	for _, m := range []Machine{from, to} {
		if err := requirePolicy(m, true); err != nil {
			return nil, err
		}
	}
	res, err := t.backend.Transfer(ctx, c.laneID, from, to, in.FromPath, in.ToPath, in.Overwrite)
	t.audit(ctx, c, "transfer", from, map[string]any{
		"from_path": in.FromPath, "to_machine": to.Name, "to_path": in.ToPath, "bytes": res.Bytes, "files": res.Files,
	}, err)
	if err != nil {
		return nil, fmt.Errorf("transfer failed: %w", protoErr(err))
	}
	var out text
	if fromNotice != "" {
		out.raw(fromNotice)
	}
	if toNotice != "" {
		out.raw(toNotice)
	}
	dest := res.Path
	if dest == "" {
		dest = in.ToPath
	}
	sha := ""
	if res.SHA256 != "" {
		sha = " (sha256 " + res.SHA256 + ")"
	}
	out.line("[lanesh] copied %s (%s) from %s on %q to %s on %q%s.", plural(res.Files, "file", "files"), size(res.Bytes),
		in.FromPath, from.Name, dest, to.Name, sha)
	return &out, nil
}
