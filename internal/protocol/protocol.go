// Package protocol defines the control protocol spoken between the Lanesh
// server and machine daemons over WebSocket.
//
// Every frame is a JSON-encoded Message. Requests carry an ID and a method
// Type; responses reuse the ID with Type "result" or "error". Events have no
// ID. Within a protocol version, changes must be additive only.
package protocol

import (
	"encoding/json"
	"fmt"
)

// Protocol versions supported by this build.
const (
	Version    = 1
	MinVersion = 1
)

// Message types that are not methods.
const (
	TypeHello   = "hello"
	TypeWelcome = "welcome"
	TypeResult  = "result"
	TypeError   = "error"
	TypeCancel  = "cancel"
)

// Methods sent from the server to a daemon.
const (
	MethodDescribe        = "machine.describe"
	MethodSkillRead       = "skill.read"
	MethodSkillGrep       = "skill.grep"
	MethodExecStart       = "exec.start"
	MethodJobRead         = "job.read"
	MethodJobWrite        = "job.write"
	MethodJobKill         = "job.kill"
	MethodJobList         = "job.list"
	MethodFileRead        = "file.read"
	MethodFileWrite       = "file.write"
	MethodStat            = "file.stat"
	MethodPathInspect     = "path.inspect"
	MethodTransferSend    = "transfer.send"
	MethodTransferReceive = "transfer.receive"
)

// Events sent from a daemon to the server.
const (
	EventSkillsChanged = "skills.changed"
	EventJobExited     = "job.exited"
)

// Error codes.
const (
	CodeInvalid      = "invalid"
	CodeNotFound     = "not_found"
	CodeForbidden    = "forbidden"
	CodeUnsupported  = "unsupported"
	CodeTooLarge     = "too_large"
	CodeExists       = "exists"
	CodeUnavailable  = "unavailable"
	CodeTimeout      = "timeout"
	CodeIncompatible = "incompatible"
	CodeInternal     = "internal"
)

// Message is the envelope of every frame.
type Message struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a protocol-level error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Errorf builds an *Error.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// NewMessage builds a message with a JSON-encoded payload.
func NewMessage(id, typ string, payload any) (Message, error) {
	m := Message{ID: id, Type: typ}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return m, err
		}
		m.Payload = raw
	}
	return m, nil
}

// Policy is what a machine allows a lane to do.
type Policy struct {
	ExposeSkills bool `json:"expose_skills"`
	AllowExec    bool `json:"allow_exec"`
}

// Hello is the first message a daemon sends after connecting.
type Hello struct {
	Protocol   int    `json:"protocol"`
	Version    string `json:"version"`
	MachineID  string `json:"machine_id"`
	Name       string `json:"name"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Hostname   string `json:"hostname"`
	Policy     Policy `json:"policy"`
	SkillsHash string `json:"skills_hash"`
	SkillCount int    `json:"skill_count"`
}

// Welcome is the server's reply to Hello.
type Welcome struct {
	Protocol      int    `json:"protocol"`
	MinProtocol   int    `json:"min_protocol"`
	MaxProtocol   int    `json:"max_protocol"`
	ServerVersion string `json:"server_version"`
	LaneID        string `json:"lane_id"`
	LaneName      string `json:"lane_name"`
	MachineName   string `json:"machine_name"`
}

// Skill describes one installed skill.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Path        string `json:"path"`
	Hash        string `json:"hash"`
}

// ShadowedSkill is a skill hidden by a higher-priority skill with the same name.
type ShadowedSkill struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path"`
}

// SkillIndex is a machine's current skill index.
type SkillIndex struct {
	Skills   []Skill         `json:"skills"`
	Shadowed []ShadowedSkill `json:"shadowed,omitempty"`
	Hash     string          `json:"hash"`
}

// Environment describes a machine's runtime environment.
type Environment struct {
	OS           string   `json:"os"`
	Arch         string   `json:"arch"`
	Hostname     string   `json:"hostname"`
	User         string   `json:"user"`
	Home         string   `json:"home"`
	DefaultShell string   `json:"default_shell"`
	Shells       []string `json:"shells"`
	Version      string   `json:"version"`
}

// DescribeResult answers MethodDescribe.
type DescribeResult struct {
	Environment Environment `json:"environment"`
	Policy      Policy      `json:"policy"`
	Skills      SkillIndex  `json:"skills"`
}

// FileEntry is one entry of a file tree.
type FileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Dir  bool   `json:"dir,omitempty"`
}

// SkillReadParams requests a skill's SKILL.md, file tree, or a file inside it.
type SkillReadParams struct {
	Skill  string `json:"skill"`
	Path   string `json:"path,omitempty"`
	Offset int64  `json:"offset,omitempty"`
	Limit  int64  `json:"limit,omitempty"`
}

// SkillReadResult answers MethodSkillRead.
type SkillReadResult struct {
	Skill     string      `json:"skill"`
	Dir       string      `json:"dir"`
	Path      string      `json:"path"`
	Content   string      `json:"content"`
	Encoding  string      `json:"encoding"`
	Size      int64       `json:"size"`
	Offset    int64       `json:"offset"`
	Truncated bool        `json:"truncated,omitempty"`
	Files     []FileEntry `json:"files,omitempty"`
	// IsDir is set when Path names a directory; Files then lists it.
	IsDir bool `json:"is_dir,omitempty"`
	// FilesTruncated is set when Files stopped at the listing limit.
	FilesTruncated bool `json:"files_truncated,omitempty"`
}

// SkillGrepParams searches the text files of a skill.
type SkillGrepParams struct {
	Skill      string `json:"skill"`
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	Regex      bool   `json:"regex,omitempty"`
	IgnoreCase bool   `json:"ignore_case,omitempty"`
	MaxMatches int    `json:"max_matches,omitempty"`
}

// GrepMatch is one matching line.
type GrepMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// SkillGrepResult answers MethodSkillGrep.
type SkillGrepResult struct {
	Skill         string      `json:"skill"`
	Matches       []GrepMatch `json:"matches"`
	FilesSearched int         `json:"files_searched"`
	FilesMatched  int         `json:"files_matched"`
	Truncated     bool        `json:"truncated,omitempty"`
	Skipped       []string    `json:"skipped,omitempty"`
}

// ExecParams starts a job.
type ExecParams struct {
	Command    string            `json:"command"`
	Cwd        string            `json:"cwd,omitempty"`
	Skill      string            `json:"skill,omitempty"`
	Shell      string            `json:"shell,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Stdin      string            `json:"stdin,omitempty"`
	TimeoutMS  int               `json:"timeout_ms,omitempty"`
	Background bool              `json:"background,omitempty"`
	PTY        bool              `json:"pty,omitempty"`
	Owner      string            `json:"owner,omitempty"`
	MaxOutput  int               `json:"max_output,omitempty"`
	// Lane is set by the daemon (never taken from the server) to scope the job.
	Lane string `json:"-"`
}

// Job statuses.
const (
	JobRunning = "running"
	JobExited  = "exited"
	JobKilled  = "killed"
	JobLost    = "lost"
)

// JobStatus describes a job and, for exec results, its inline output.
type JobStatus struct {
	JobID           string `json:"job_id"`
	Status          string `json:"status"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	Command         string `json:"command"`
	Cwd             string `json:"cwd"`
	Shell           string `json:"shell"`
	StartedAt       int64  `json:"started_at"`
	DurationMS      int64  `json:"duration_ms"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutBytes     int64  `json:"stdout_bytes"`
	StderrBytes     int64  `json:"stderr_bytes"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	Owner           string `json:"owner,omitempty"`
}

// JobReadParams reads a range of a job's output. With WaitMS, the read waits
// for output beyond Offset (or for the job to finish) before returning.
type JobReadParams struct {
	JobID  string `json:"job_id"`
	Stream string `json:"stream,omitempty"`
	Offset int64  `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	WaitMS int    `json:"wait_ms,omitempty"`
}

// JobReadResult answers MethodJobRead.
type JobReadResult struct {
	JobID      string `json:"job_id"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	Stream     string `json:"stream"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	Total      int64  `json:"total"`
	Dropped    int64  `json:"dropped,omitempty"`
	Data       string `json:"data"`
	EOF        bool   `json:"eof"`
}

// JobWriteParams writes to a job's stdin.
type JobWriteParams struct {
	JobID      string `json:"job_id"`
	Data       string `json:"data,omitempty"`
	CloseStdin bool   `json:"close_stdin,omitempty"`
}

// JobKillParams terminates a job.
type JobKillParams struct {
	JobID  string `json:"job_id"`
	Signal string `json:"signal,omitempty"`
}

// JobListParams lists jobs, optionally filtered by owner.
type JobListParams struct {
	Owner string `json:"owner,omitempty"`
}

// JobListResult answers MethodJobList.
type JobListResult struct {
	Jobs []JobStatus `json:"jobs"`
}

// JobExitedEvent is emitted when a job finishes.
type JobExitedEvent struct {
	JobID      string `json:"job_id"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Command    string `json:"command"`
	Owner      string `json:"owner,omitempty"`
	Lane       string `json:"-"`
}

// Encodings for file content.
const (
	EncodingUTF8   = "utf-8"
	EncodingBase64 = "base64"
)

// FileReadParams reads a file inline.
type FileReadParams struct {
	Path     string `json:"path"`
	Offset   int64  `json:"offset,omitempty"`
	Limit    int64  `json:"limit,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

// FileReadResult answers MethodFileRead.
type FileReadResult struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Encoding  string `json:"encoding"`
	Size      int64  `json:"size"`
	Offset    int64  `json:"offset"`
	Truncated bool   `json:"truncated,omitempty"`
}

// FileWriteParams writes a file inline.
type FileWriteParams struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Encoding   string `json:"encoding,omitempty"`
	Overwrite  bool   `json:"overwrite,omitempty"`
	CreateDirs bool   `json:"create_dirs,omitempty"`
	Mode       uint32 `json:"mode,omitempty"`
}

// FileWriteResult answers MethodFileWrite.
type FileWriteResult struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// StatParams inspects a path.
type StatParams struct {
	Path string `json:"path"`
}

// StatResult answers MethodStat.
type StatResult struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Dir    bool   `json:"dir"`
	Size   int64  `json:"size"`
}

// PathInspectParams asks for facts about directories, such as the locations
// of a project.
type PathInspectParams struct {
	Paths []string `json:"paths"`
}

// PathInfo describes one inspected path. Path is as given; Resolved is the
// absolute path it refers to on the machine.
type PathInfo struct {
	Path     string `json:"path"`
	Resolved string `json:"resolved,omitempty"`
	Exists   bool   `json:"exists"`
	Dir      bool   `json:"dir,omitempty"`
	// Git is set when the path is inside a Git working tree; Branch is its
	// checked-out branch (or "detached at <commit>"), and Worktree is set for
	// a linked worktree (git worktree add).
	Git      bool   `json:"git,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Worktree bool   `json:"worktree,omitempty"`
	// AgentFiles lists agent instruction files in the directory, such as
	// "AGENTS.md" or "CLAUDE.md -> AGENTS.md" when one links to the other.
	AgentFiles []string `json:"agent_files,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// PathInspectResult answers MethodPathInspect, in the order of the request.
type PathInspectResult struct {
	Paths []PathInfo `json:"paths"`
}

// TransferParams instructs a daemon to upload (send) or download (receive)
// one side of a transfer through the server's data plane.
type TransferParams struct {
	TransferID string `json:"transfer_id"`
	URL        string `json:"url"`
	Ticket     string `json:"ticket"`
	Path       string `json:"path"`
	Overwrite  bool   `json:"overwrite,omitempty"`
}

// TransferResult answers transfer methods once the stream is complete.
// Bytes is the total size of the transferred files; SHA256 covers the stream.
type TransferResult struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Files  int    `json:"files"`
	SHA256 string `json:"sha256"`
}
