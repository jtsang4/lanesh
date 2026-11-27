package cli

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jtsang4/lanesh/internal/files"
	"github.com/jtsang4/lanesh/internal/mcp"
	"github.com/jtsang4/lanesh/internal/protocol"
)

func newConnectCmd() *cobra.Command {
	var key, machine string
	cmd := &cobra.Command{
		Use:   "connect <lane-url>",
		Short: "Save a lane URL and access key for client commands",
		Example: "  lanesh connect https://lanesh.example.com/lanes/ln_xxx --key lsk_xxx\n" +
			"  lanesh connect https://lanesh.example.com/lanes/ln_xxx/mcp --key lsk_xxx",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if key == "" {
				fmt.Fprint(cmd.ErrOrStderr(), "Access key: ")
				if term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // File descriptors fit in int.
					b, err := term.ReadPassword(int(os.Stdin.Fd())) //nolint:gosec // See above.
					fmt.Fprintln(cmd.ErrOrStderr())
					if err != nil {
						return err
					}
					key = string(b)
				} else {
					line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
					key = line
				}
			}
			c, err := newLaneClient(args[0], key, machine)
			if err != nil {
				return err
			}
			var ms []clientMachine
			if err := c.do(cmd.Context(), http.MethodGet, "/machines", nil, &ms); err != nil {
				return fmt.Errorf("connect failed: %w", err)
			}
			if c.machine == "" {
				c.machine = c.detectLocalMachine(cmd.Context(), ms)
			}
			p, err := paths()
			if err != nil {
				return err
			}
			raw, err := toml.Marshal(clientConfig{LaneURL: c.laneURL(), AccessKey: c.accessKey, Machine: c.machine}) //nolint:gosec // Written with 0600 permissions to client.toml.
			if err != nil {
				return err
			}
			if err := os.MkdirAll(p.Home, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(p.ClientConfig(), raw, 0o600); err != nil {
				return err
			}
			online := 0
			for _, m := range ms {
				if m.Online {
					online++
				}
			}
			selfNote := ""
			if c.machine != "" {
				selfNote = fmt.Sprintf(" as %q", c.machine)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Connected to %s%s (%d/%d machines online).\n", c.laneURL(), selfNote, online, len(ms))
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "lane access key (prompted when omitted)")
	cmd.Flags().StringVar(&machine, "machine", "", "this machine's name in the lane (auto-detected if joined)")
	return cmd
}

func newDisconnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disconnect",
		Short: "Forget the saved lane access key",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths()
			if err != nil {
				return err
			}
			if err := os.Remove(p.ClientConfig()); err != nil && !os.IsNotExist(err) {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Disconnected.")
			return nil
		},
	}
}

func newMachinesCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "machines",
		Short: "List machines in the connected lane",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			var ms []clientMachine
			if err := c.do(cmd.Context(), http.MethodGet, "/machines", nil, &ms); err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), ms)
			}
			self := c.detectLocalMachine(cmd.Context(), ms)
			rows := make([][]string, 0, len(ms))
			for _, m := range ms {
				name := m.Name
				if self != "" && strings.EqualFold(self, m.Name) {
					name += " (you)"
				}
				state := "online"
				if !m.Online {
					state = "offline since " + m.LastSeen.Local().Format(time.DateTime)
				}
				shell := m.Environment.DefaultShell
				if shell == "" {
					shell = "-"
				}
				rows = append(rows, []string{
					name, m.OS + "/" + m.Arch, shell, fmt.Sprint(m.SkillCount),
					map[bool]string{true: "yes", false: "no"}[m.Policy.AllowExec], state,
				})
			}
			table(cmd.OutOrStdout(), "NAME\tPLATFORM\tSHELL\tSKILLS\tEXEC\tSTATE", rows)
			return nil
		},
	}
	addClientFlags(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func newDescribeCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "describe <machine>",
		Short: "Show a machine's environment, permissions and skills",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			var res protocol.DescribeResult
			if err := c.do(cmd.Context(), http.MethodGet, "/machines/"+url.PathEscape(args[0])+"/describe", nil, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), res)
			}
			w := cmd.OutOrStdout()
			env := res.Environment
			fmt.Fprintf(w, "Machine: %s (%s/%s), user %s, home %s\n", args[0], env.OS, env.Arch, env.User, env.Home)
			fmt.Fprintf(w, "Shell:   %s", env.DefaultShell)
			if len(env.Shells) > 1 {
				fmt.Fprintf(w, " (all: %s)", strings.Join(env.Shells, ", "))
			}
			fmt.Fprintln(w)
			fmt.Fprintf(w, "Policy:  expose_skills=%v, allow_exec=%v\n", res.Policy.ExposeSkills, res.Policy.AllowExec)
			if len(res.Skills.Skills) == 0 {
				fmt.Fprintln(w, "Skills:  none")
				return nil
			}
			fmt.Fprintf(w, "Skills (%d):\n", len(res.Skills.Skills))
			for _, sk := range res.Skills.Skills {
				fmt.Fprintf(w, "  - %s: %s\n", sk.Name, sk.Description)
			}
			return nil
		},
	}
	addClientFlags(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func newSkillsCmd() *cobra.Command {
	var machine string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "skills [query]",
		Short: "Search, read or grep skills across machines",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			q := url.Values{}
			if len(args) == 1 {
				q.Set("q", args[0])
			}
			if machine != "" {
				q.Set("machine", machine)
			}
			path := "/skills"
			if enc := q.Encode(); enc != "" {
				path += "?" + enc
			}
			var items []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Machine     string `json:"machine"`
				Online      bool   `json:"online"`
				Source      string `json:"source"`
			}
			if err := c.do(cmd.Context(), http.MethodGet, path, nil, &items); err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), items)
			}
			rows := make([][]string, 0, len(items))
			for _, it := range items {
				m := it.Machine
				if !it.Online {
					m += " (offline)"
				}
				desc := it.Description
				if len(desc) > 80 {
					desc = desc[:79] + "…"
				}
				rows = append(rows, []string{it.Name, m, desc})
			}
			table(cmd.OutOrStdout(), "SKILL\tMACHINE\tDESCRIPTION", rows)
			return nil
		},
	}
	addClientFlags(cmd)
	cmd.Flags().StringVar(&machine, "machine", "", "only list skills on this machine")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	var offset, limit int64
	var readJSON bool
	readCmd := &cobra.Command{
		Use:   "read <machine> <skill> [path]",
		Short: "Read a skill's SKILL.md, file, or subdirectory",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			p := protocol.SkillReadParams{Skill: args[1], Offset: offset, Limit: limit}
			if len(args) == 3 {
				p.Path = args[2]
			}
			var res protocol.SkillReadResult
			if err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(args[0])+"/skills/read", p, &res); err != nil {
				return err
			}
			if readJSON {
				return printJSON(cmd.OutOrStdout(), res)
			}
			w := cmd.OutOrStdout()
			if res.IsDir {
				for _, f := range res.Files {
					suffix := ""
					if f.Dir {
						suffix = "/"
					}
					fmt.Fprintf(w, "%s%s\n", f.Path, suffix)
				}
				return nil
			}
			fmt.Fprint(w, res.Content)
			if len(res.Content) > 0 && !strings.HasSuffix(res.Content, "\n") {
				fmt.Fprintln(w)
			}
			return nil
		},
	}
	readCmd.Flags().Int64Var(&offset, "offset", 0, "byte offset")
	readCmd.Flags().Int64Var(&limit, "limit", 0, "max bytes to read")
	readCmd.Flags().BoolVar(&readJSON, "json", false, "output JSON")

	var grepPath string
	var regex, caseSensitive, grepJSON bool
	var maxMatches int
	grepCmd := &cobra.Command{
		Use:   "grep <machine> <skill> <pattern>",
		Short: "Search text files inside a skill",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			p := protocol.SkillGrepParams{
				Skill: args[1], Pattern: args[2], Path: grepPath,
				Regex: regex, IgnoreCase: !caseSensitive, MaxMatches: maxMatches,
			}
			var res protocol.SkillGrepResult
			if err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(args[0])+"/skills/grep", p, &res); err != nil {
				return err
			}
			if grepJSON {
				return printJSON(cmd.OutOrStdout(), res)
			}
			for _, m := range res.Matches {
				fmt.Fprintf(cmd.OutOrStdout(), "%s:%d: %s\n", m.Path, m.Line, m.Text)
			}
			return nil
		},
	}
	grepCmd.Flags().StringVar(&grepPath, "path", "", "limit search to this path inside the skill")
	grepCmd.Flags().BoolVar(&regex, "regex", false, "treat pattern as a Go regular expression")
	grepCmd.Flags().BoolVarP(&caseSensitive, "case-sensitive", "s", false, "case-sensitive match")
	grepCmd.Flags().IntVar(&maxMatches, "max", 100, "maximum matches")
	grepCmd.Flags().BoolVar(&grepJSON, "json", false, "output JSON")

	cmd.AddCommand(readCmd, grepCmd)
	return cmd
}

const cliExecChunkWait = 30 * time.Second

func newExecCmd() *cobra.Command {
	var cwd, skill, shell string
	var envPairs []string
	var timeout time.Duration
	var bg, readStdin, asJSON bool
	cmd := &cobra.Command{
		Use:   "exec <machine> [flags] -- <command...>",
		Short: "Run a shell command on a machine and stream its output",
		Example: "  lanesh exec devbox -- uname -a\n" +
			"  lanesh exec devbox --cwd ~/repo -- go test ./...\n" +
			"  lanesh exec devbox --bg -- ./scripts/serve.sh",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			machine := args[0]
			command := strings.Join(args[1:], " ")

			var envMap map[string]string
			if len(envPairs) > 0 {
				envMap = make(map[string]string, len(envPairs))
				for _, kv := range envPairs {
					k, v, ok := strings.Cut(kv, "=")
					if !ok || k == "" {
						return fmt.Errorf("invalid --env %q: expected KEY=VALUE", kv)
					}
					envMap[k] = v
				}
			}

			var stdinData string
			if readStdin {
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				stdinData = string(raw)
			}

			// Cap each HTTP round trip at 30s so multi-minute commands stay well
			// below reverse proxy timeouts (such as Cloudflare's 100s origin timeout).
			firstWait := cliExecChunkWait
			var deadline time.Time
			if timeout > 0 {
				deadline = time.Now().Add(timeout)
				if timeout < firstWait {
					firstWait = timeout
				}
			}
			req := protocol.ExecParams{
				Command:    command,
				Cwd:        cwd,
				Skill:      skill,
				Shell:      shell,
				Env:        envMap,
				Stdin:      stdinData,
				Background: bg,
				TimeoutMS:  int(firstWait.Milliseconds()),
			}
			var st protocol.JobStatus
			if err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(machine)+"/exec", req, &st); err != nil {
				return err
			}
			if bg {
				if asJSON {
					return printJSON(cmd.OutOrStdout(), st)
				}
				fmt.Fprintln(cmd.OutOrStdout(), st.JobID)
				return nil
			}

			var outBuf, errBuf bytes.Buffer
			writeOut := func(s string) {
				if s == "" {
					return
				}
				if asJSON {
					outBuf.WriteString(s)
				} else {
					fmt.Fprint(cmd.OutOrStdout(), s)
				}
			}
			writeErr := func(s string) {
				if s == "" {
					return
				}
				if asJSON {
					errBuf.WriteString(s)
				} else {
					fmt.Fprint(cmd.ErrOrStderr(), s)
				}
			}

			var outOff, errOff int64
			needRead := st.Status == protocol.JobRunning || st.OutputTruncated
			if !st.OutputTruncated {
				writeOut(st.Stdout)
				writeErr(st.Stderr)
				outOff, errOff = st.StdoutBytes, st.StderrBytes
			}

			status := st.Status
			exitCode := st.ExitCode
			for needRead {
				wait := cliExecChunkWait
				if !deadline.IsZero() {
					rem := time.Until(deadline)
					if rem <= 0 && status == protocol.JobRunning {
						fmt.Fprintf(cmd.ErrOrStderr(), "timed out after %s; job %s is still running on %s\n", timeout, st.JobID, machine)
						fmt.Fprintf(cmd.ErrOrStderr(), "follow with: lanesh jobs read %s %s --follow\n", machine, st.JobID)
						return exitError{code: 124}
					}
					if rem > 0 && rem < wait {
						wait = rem
					}
				}
				var rr protocol.JobReadResult
				err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(machine)+"/jobs/"+url.PathEscape(st.JobID)+"/read",
					protocol.JobReadParams{
						Stream:       "both",
						Offset:       outOff,
						StderrOffset: errOff,
						Limit:        64 << 10,
						WaitMS:       int(wait.Milliseconds()),
					}, &rr)
				if err != nil {
					return err
				}
				writeOut(rr.Data)
				writeErr(rr.StderrData)
				outOff, errOff = rr.NextOffset, rr.StderrNextOffset
				status = rr.Status
				exitCode = rr.ExitCode
				if rr.EOF && outOff >= rr.Total && errOff >= rr.StderrTotal {
					break
				}
			}

			if asJSON {
				st.Status = status
				st.ExitCode = exitCode
				st.Stdout = outBuf.String()
				st.Stderr = errBuf.String()
				st.StdoutBytes = outOff
				st.StderrBytes = errOff
				st.OutputTruncated = false
				if err := printJSON(cmd.OutOrStdout(), st); err != nil {
					return err
				}
			}
			if status == protocol.JobExited && exitCode != nil && *exitCode != 0 {
				return exitError{code: *exitCode}
			}
			if status == protocol.JobKilled || status == protocol.JobLost {
				return exitError{code: 1}
			}
			return nil
		},
	}
	addClientFlags(cmd)
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory on the machine")
	cmd.Flags().StringVar(&skill, "skill", "", "run in this skill's directory")
	cmd.Flags().StringVar(&shell, "shell", "", "shell to use instead of the machine default")
	cmd.Flags().StringArrayVarP(&envPairs, "env", "e", nil, "environment variable KEY=VALUE (repeatable)")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "overall wait timeout before detaching (0 = wait until exit)")
	cmd.Flags().BoolVar(&bg, "bg", false, "start in background and print the job ID")
	cmd.Flags().BoolVar(&readStdin, "stdin", false, "pass local stdin to the remote command")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func newJobsCmd() *cobra.Command {
	var all, finished, asJSON bool
	cmd := &cobra.Command{
		Use:   "jobs <machine>",
		Short: "List or manage background jobs on a machine",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			path := "/machines/" + url.PathEscape(args[0]) + "/jobs"
			if all {
				path += "?all=true"
			}
			var res protocol.JobListResult
			if err := c.do(cmd.Context(), http.MethodGet, path, nil, &res); err != nil {
				return err
			}
			var jobs []protocol.JobStatus
			for _, j := range res.Jobs {
				if !finished && j.Status != protocol.JobRunning {
					continue
				}
				jobs = append(jobs, j)
			}
			if asJSON {
				if jobs == nil {
					jobs = []protocol.JobStatus{}
				}
				return printJSON(cmd.OutOrStdout(), jobs)
			}
			rows := make([][]string, 0, len(jobs))
			for _, j := range jobs {
				state := j.Status
				if j.Status == protocol.JobExited && j.ExitCode != nil {
					state = fmt.Sprintf("exited(%d)", *j.ExitCode)
				}
				cmdLine := strings.Join(strings.Fields(j.Command), " ")
				if len(cmdLine) > 80 {
					cmdLine = cmdLine[:79] + "…"
				}
				dur := (time.Duration(j.DurationMS) * time.Millisecond).Round(100 * time.Millisecond)
				rows = append(rows, []string{j.JobID, state, dur.String(), cmdLine})
			}
			table(cmd.OutOrStdout(), "JOB\tSTATE\tDURATION\tCOMMAND", rows)
			return nil
		},
	}
	addClientFlags(cmd)
	cmd.Flags().BoolVar(&all, "all", false, "include jobs started with other access keys")
	cmd.Flags().BoolVar(&finished, "finished", false, "include recently finished jobs")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	var stream, waitUntil, match string
	var offset, stderrOffset int64
	var limit int
	var waitDur, idleDur time.Duration
	var follow, readJSON bool
	readCmd := &cobra.Command{
		Use:   "read <machine> <job-id>",
		Short: "Read or follow output from a job",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			machine, jobID := args[0], args[1]
			if follow && stream == "" {
				stream = "both"
			}
			for {
				w := waitDur
				if follow && w == 0 {
					w = cliExecChunkWait
				}
				var res protocol.JobReadResult
				err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(machine)+"/jobs/"+url.PathEscape(jobID)+"/read",
					protocol.JobReadParams{
						Stream:       stream,
						Offset:       offset,
						StderrOffset: stderrOffset,
						Limit:        limit,
						WaitMS:       int(w.Milliseconds()),
						WaitUntil:    waitUntil,
						IdleMS:       int(idleDur.Milliseconds()),
						Match:        match,
					}, &res)
				if err != nil {
					return err
				}
				if readJSON {
					return printJSON(cmd.OutOrStdout(), res)
				}
				if res.Stream == "both" {
					fmt.Fprint(cmd.OutOrStdout(), res.Data)
					fmt.Fprint(cmd.ErrOrStderr(), res.StderrData)
					offset, stderrOffset = res.NextOffset, res.StderrNextOffset
					if !follow || (res.EOF && offset >= res.Total && stderrOffset >= res.StderrTotal) {
						return nil
					}
					continue
				}
				if res.Stream == "stderr" {
					fmt.Fprint(cmd.ErrOrStderr(), res.Data)
				} else {
					fmt.Fprint(cmd.OutOrStdout(), res.Data)
				}
				offset = res.NextOffset
				if !follow || (res.EOF && offset >= res.Total) {
					return nil
				}
			}
		},
	}
	readCmd.Flags().StringVar(&stream, "stream", "both", "stdout, stderr, or both")
	readCmd.Flags().Int64Var(&offset, "offset", 0, "byte offset (stdout when stream=both)")
	readCmd.Flags().Int64Var(&stderrOffset, "stderr-offset", 0, "stderr byte offset when stream=both")
	readCmd.Flags().IntVar(&limit, "limit", 64<<10, "max bytes per read")
	readCmd.Flags().DurationVar(&waitDur, "wait", 0, "wait up to this duration (max 60s)")
	readCmd.Flags().StringVar(&waitUntil, "until", "", "wait condition: output, idle, or exit")
	readCmd.Flags().DurationVar(&idleDur, "idle", 0, "quiet window for --until=idle")
	readCmd.Flags().StringVar(&match, "match", "", "Go regex; wake when new output matches")
	readCmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep reading until the job exits")
	readCmd.Flags().BoolVar(&readJSON, "json", false, "output JSON")

	var closeStdin bool
	writeCmd := &cobra.Command{
		Use:   "write <machine> <job-id> [data]",
		Short: "Write to a background job's stdin",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			var data string
			if len(args) == 3 {
				data = args[2]
			} else if !closeStdin {
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				data = string(raw)
			}
			return c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(args[0])+"/jobs/"+url.PathEscape(args[1])+"/write",
				protocol.JobWriteParams{Data: data, CloseStdin: closeStdin}, nil)
		},
	}
	writeCmd.Flags().BoolVar(&closeStdin, "close", false, "close stdin after writing")

	var signal string
	killCmd := &cobra.Command{
		Use:   "kill <machine> <job-id>",
		Short: "Stop a running job and its children",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			var st protocol.JobStatus
			if err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(args[0])+"/jobs/"+url.PathEscape(args[1])+"/kill",
				protocol.JobKillParams{Signal: signal}, &st); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "job %s: %s\n", st.JobID, st.Status)
			return nil
		},
	}
	killCmd.Flags().StringVar(&signal, "signal", "KILL", "TERM, INT, or KILL")

	cmd.AddCommand(readCmd, writeCmd, killCmd)
	return cmd
}

func newReadCmd() *cobra.Command {
	var linesSpec, outPath string
	var offset, limit int64
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "read <machine>:<path>",
		Short:   "Read a file from a machine",
		Example: "  lanesh read devbox:~/repo/main.go --lines 1-80\n  lanesh read devbox:/tmp/screen.png -o ./screen.png",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			machine, remotePath, err := parseTarget(args, false)
			if err != nil {
				return err
			}
			startLine, endLine, err := parseLineRange(linesSpec)
			if err != nil {
				return err
			}
			singleChunk := startLine > 0 || endLine > 0 || offset > 0 || limit > 0 || asJSON
			if singleChunk {
				var res protocol.FileReadResult
				err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(machine)+"/files/read",
					protocol.FileReadParams{
						Path: remotePath, StartLine: startLine, EndLine: endLine, Offset: offset, Limit: limit,
					}, &res)
				if err != nil {
					return err
				}
				if asJSON {
					return printJSON(cmd.OutOrStdout(), res)
				}
				data, err := decodeFileContent(res)
				if err != nil {
					return err
				}
				return writeCLIOutput(cmd.OutOrStdout(), outPath, data)
			}

			// Whole-file read: loop chunks until EOF (up to inline limit).
			var buf bytes.Buffer
			var curOff int64
			for {
				var res protocol.FileReadResult
				err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(machine)+"/files/read",
					protocol.FileReadParams{Path: remotePath, Offset: curOff, Limit: 256 << 10}, &res)
				if err != nil {
					return err
				}
				chunk, err := decodeFileContent(res)
				if err != nil {
					return err
				}
				buf.Write(chunk)
				if !res.Truncated || len(chunk) == 0 {
					break
				}
				curOff += int64(len(chunk))
				if curOff >= res.Size {
					break
				}
			}
			return writeCLIOutput(cmd.OutOrStdout(), outPath, buf.Bytes())
		},
	}
	addClientFlags(cmd)
	cmd.Flags().StringVar(&linesSpec, "lines", "", "1-based line range, e.g. 1-50 or 20-")
	cmd.Flags().Int64Var(&offset, "offset", 0, "byte offset")
	cmd.Flags().Int64Var(&limit, "limit", 0, "max bytes to read")
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "write output to a local file instead of stdout")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func decodeFileContent(res protocol.FileReadResult) ([]byte, error) {
	if res.Encoding == protocol.EncodingBase64 {
		return base64.StdEncoding.DecodeString(res.Content)
	}
	return []byte(res.Content), nil
}

func writeCLIOutput(stdout io.Writer, outPath string, data []byte) error {
	if outPath == "" {
		_, err := stdout.Write(data)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o750); err != nil {
		return err
	}
	return os.WriteFile(outPath, data, 0o600)
}

func newWriteCmd() *cobra.Command {
	var srcFile, modeStr string
	var overwrite, createDirs, asJSON bool
	cmd := &cobra.Command{
		Use:     "write <machine>:<path>",
		Short:   "Write a file (up to 1 MiB) to a machine from a local file or stdin",
		Example: "  echo 'hello' | lanesh write devbox:/tmp/hi.txt --overwrite\n  lanesh write devbox:~/notes/todo.md -f ./todo.md --create-dirs",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			machine, remotePath, err := parseTarget(args, false)
			if err != nil {
				return err
			}
			var raw []byte
			if srcFile != "" {
				raw, err = os.ReadFile(srcFile) //nolint:gosec // User-supplied source file.
				if err != nil {
					return err
				}
			} else {
				raw, err = io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
			}
			var mode uint32
			if modeStr != "" {
				parsed, err := strconv.ParseUint(modeStr, 8, 32)
				if err != nil {
					return fmt.Errorf("invalid --mode %q (expected octal, e.g. 0755): %w", modeStr, err)
				}
				mode = uint32(parsed)
			}
			enc := protocol.EncodingUTF8
			content := string(raw)
			if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
				enc = protocol.EncodingBase64
				content = base64.StdEncoding.EncodeToString(raw)
			}
			var res protocol.FileWriteResult
			err = c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(machine)+"/files/write",
				protocol.FileWriteParams{
					Path: remotePath, Content: content, Encoding: enc,
					Overwrite: overwrite, CreateDirs: createDirs, Mode: mode,
				}, &res)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s to %s:%s\n", fmtBytes(res.Size), machine, res.Path)
			return nil
		},
	}
	addClientFlags(cmd)
	cmd.Flags().StringVarP(&srcFile, "file", "f", "", "read content from this local file (default: stdin)")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace existing file")
	cmd.Flags().BoolVar(&createDirs, "create-dirs", false, "create parent directories")
	cmd.Flags().StringVar(&modeStr, "mode", "", "Unix permissions in octal, e.g. 0755")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func newEditCmd() *cobra.Command {
	var oldText, newText, editsFile string
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "edit <machine>:<path>",
		Short:   "Apply unique text replacements to a remote UTF-8 file",
		Example: "  lanesh edit devbox:~/app/config.yaml --old 'debug: false' --new 'debug: true'",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			machine, remotePath, err := parseTarget(args, false)
			if err != nil {
				return err
			}
			var ops []protocol.FileEditOp
			if editsFile != "" {
				raw, err := os.ReadFile(editsFile) //nolint:gosec // User-supplied edits file.
				if err != nil {
					return err
				}
				if err := json.Unmarshal(raw, &ops); err != nil {
					return fmt.Errorf("parse %s: %w", editsFile, err)
				}
			} else {
				if oldText == "" {
					return errors.New("provide --old/--new or --edits-file")
				}
				ops = []protocol.FileEditOp{{OldText: oldText, NewText: newText}}
			}
			var res protocol.FileEditResult
			if err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(machine)+"/files/edit",
				protocol.FileEditParams{Path: remotePath, Edits: ops}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), res)
			}
			if res.Diff == "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s:%s unchanged\n", machine, res.Path)
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), res.Diff)
			return nil
		},
	}
	addClientFlags(cmd)
	cmd.Flags().StringVar(&oldText, "old", "", "exact text to replace (must occur once)")
	cmd.Flags().StringVar(&newText, "new", "", "replacement text")
	cmd.Flags().StringVar(&editsFile, "edits-file", "", "JSON file with [{old_text, new_text}, ...] edits")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func newLsCmd() *cobra.Command {
	var depth int
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:     "ls <machine>[:<path>]",
		Short:   "List a directory on a machine",
		Example: "  lanesh ls devbox:~/workspace --depth 2",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			machine, remotePath, err := parseTarget(args, true)
			if err != nil {
				return err
			}
			var res protocol.FileListResult
			if err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(machine)+"/files/list",
				protocol.FileListParams{Path: remotePath, MaxDepth: depth, IncludeHidden: all}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), res)
			}
			for _, e := range res.Entries {
				if e.Dir {
					fmt.Fprintf(cmd.OutOrStdout(), "%s/\n", e.Path)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s\t(%s)\n", e.Path, fmtBytes(e.Size))
				}
			}
			if res.Truncated {
				fmt.Fprintf(cmd.ErrOrStderr(), "(truncated at %d entries)\n", len(res.Entries))
			}
			return nil
		},
	}
	addClientFlags(cmd)
	cmd.Flags().IntVar(&depth, "depth", 4, "max directory depth")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include hidden dotfiles and dotdirs")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func newGrepCmd() *cobra.Command {
	var glob string
	var regex, caseSensitive, asJSON bool
	var maxMatches int
	cmd := &cobra.Command{
		Use:     "grep <machine>[:<path>] <pattern>",
		Short:   "Search text files on a machine",
		Example: "  lanesh grep devbox:~/repo 'TODO' --glob '*.go'",
		Args:    cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			var machine, remotePath, pattern string
			if len(args) == 2 {
				var err error
				machine, remotePath, err = parseTarget(args[:1], true)
				if err != nil {
					return err
				}
				pattern = args[1]
			} else {
				machine, remotePath, pattern = args[0], args[1], args[2]
			}
			var res protocol.FileGrepResult
			if err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(machine)+"/files/grep",
				protocol.FileGrepParams{
					Path: remotePath, Pattern: pattern, Regex: regex,
					IgnoreCase: !caseSensitive, Glob: glob, MaxMatches: maxMatches,
				}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), res)
			}
			for _, m := range res.Matches {
				fmt.Fprintf(cmd.OutOrStdout(), "%s:%d: %s\n", m.Path, m.Line, m.Text)
			}
			if res.Truncated {
				fmt.Fprintf(cmd.ErrOrStderr(), "(stopped at %d matches)\n", len(res.Matches))
			}
			return nil
		},
	}
	addClientFlags(cmd)
	cmd.Flags().BoolVar(&regex, "regex", false, "treat pattern as a Go regular expression")
	cmd.Flags().BoolVarP(&caseSensitive, "case-sensitive", "s", false, "case-sensitive match")
	cmd.Flags().StringVar(&glob, "glob", "", "filename glob filter, e.g. *.go")
	cmd.Flags().IntVar(&maxMatches, "max", 100, "maximum matches (up to 500)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func newCpCmd() *cobra.Command {
	var overwrite, asJSON bool
	cmd := &cobra.Command{
		Use:   "cp <src> <dst>",
		Short: "Copy a file or directory between machines (or local and remote)",
		Example: "  lanesh cp alpha:~/dist/app.pkg beta:~/Downloads/app.pkg\n" +
			"  lanesh cp ./build.zip bytemac:~/Downloads/build.zip --overwrite",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			srcM, srcP, srcRemote := splitRemote(args[0])
			dstM, dstP, dstRemote := splitRemote(args[1])
			if !srcRemote && !dstRemote {
				return errors.New("at least one of src or dst must be remote (<machine>:<path>)")
			}
			if (srcRemote && srcP == "") || (dstRemote && dstP == "") {
				return errors.New("remote path cannot be empty")
			}

			// If either side is local, prefer streaming transfer when this machine is joined to the lane.
			if !srcRemote || !dstRemote {
				self := c.detectLocalMachine(cmd.Context(), nil)
				if self != "" {
					if !srcRemote {
						abs, err := filepath.Abs(srcP)
						if err != nil {
							return err
						}
						srcM, srcP, srcRemote = self, abs, true
					}
					if !dstRemote {
						abs, err := filepath.Abs(dstP)
						if err != nil {
							return err
						}
						dstM, dstP, dstRemote = self, abs, true
					}
				}
			}

			if srcRemote && dstRemote {
				var res protocol.TransferResult
				err := c.doLong(cmd.Context(), http.MethodPost, "/transfer", map[string]any{
					"from_machine": srcM, "from_path": srcP,
					"to_machine": dstM, "to_path": dstP,
					"overwrite": overwrite,
				}, &res)
				if err != nil {
					return err
				}
				if asJSON {
					return printJSON(cmd.OutOrStdout(), res)
				}
				sha := ""
				if res.SHA256 != "" {
					sha = " (sha256 " + res.SHA256 + ")"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "copied %d file(s) (%s) %s:%s -> %s:%s%s\n",
					res.Files, fmtBytes(res.Bytes), srcM, srcP, dstM, dstP, sha)
				return nil
			}

			// Fallback when this machine is not joined to the lane: inline read/write for single files <= 1 MiB.
			if !srcRemote {
				info, err := os.Stat(srcP)
				if err != nil {
					return err
				}
				if info.IsDir() || info.Size() > files.MaxInline {
					return fmt.Errorf("copying directories or files over %s from an unjoined machine requires joining this machine with `lanesh join`", fmtBytes(files.MaxInline))
				}
				raw, err := os.ReadFile(srcP) //nolint:gosec // User-supplied source file.
				if err != nil {
					return err
				}
				enc := protocol.EncodingUTF8
				content := string(raw)
				if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
					enc = protocol.EncodingBase64
					content = base64.StdEncoding.EncodeToString(raw)
				}
				var res protocol.FileWriteResult
				err = c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(dstM)+"/files/write",
					protocol.FileWriteParams{
						Path: dstP, Content: content, Encoding: enc, Overwrite: overwrite, CreateDirs: true,
					}, &res)
				if err != nil {
					return err
				}
				if asJSON {
					return printJSON(cmd.OutOrStdout(), res)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "copied 1 file (%s) %s -> %s:%s\n", fmtBytes(res.Size), srcP, dstM, res.Path)
				return nil
			}

			// Remote -> local fallback for single files <= 1 MiB.
			if !overwrite {
				if _, err := os.Stat(dstP); err == nil {
					return fmt.Errorf("%s already exists (pass --overwrite to replace)", dstP)
				}
			}
			var buf bytes.Buffer
			var curOff int64
			for {
				var res protocol.FileReadResult
				err := c.do(cmd.Context(), http.MethodPost, "/machines/"+url.PathEscape(srcM)+"/files/read",
					protocol.FileReadParams{Path: srcP, Offset: curOff, Limit: 256 << 10}, &res)
				if err != nil {
					return fmt.Errorf("%w (for directories or files over %s, join this machine with `lanesh join`)", err, fmtBytes(files.MaxInline))
				}
				chunk, err := decodeFileContent(res)
				if err != nil {
					return err
				}
				buf.Write(chunk)
				if !res.Truncated || len(chunk) == 0 {
					break
				}
				curOff += int64(len(chunk))
				if curOff >= res.Size {
					break
				}
			}
			if err := writeCLIOutput(cmd.OutOrStdout(), dstP, buf.Bytes()); err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), map[string]any{"path": dstP, "bytes": buf.Len(), "files": 1})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "copied 1 file (%s) %s:%s -> %s\n", fmtBytes(int64(buf.Len())), srcM, srcP, dstP)
			return nil
		},
	}
	addClientFlags(cmd)
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace existing destination")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "List, read or update shared projects"}
	addClientFlags(cmd)

	var listJSON bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List projects in the lane",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			var ps []struct {
				ID        string             `json:"id"`
				Name      string             `json:"name"`
				Summary   string             `json:"summary"`
				Revision  int                `json:"revision"`
				Locations []mcp.LocationView `json:"locations"`
			}
			if err := c.do(cmd.Context(), http.MethodGet, "/projects", nil, &ps); err != nil {
				return err
			}
			if listJSON {
				return printJSON(cmd.OutOrStdout(), ps)
			}
			rows := make([][]string, 0, len(ps))
			for _, p := range ps {
				sum := p.Summary
				if len(sum) > 70 {
					sum = sum[:69] + "…"
				}
				rows = append(rows, []string{p.Name, fmt.Sprintf("r%d", p.Revision), fmt.Sprint(len(p.Locations)), sum})
			}
			table(cmd.OutOrStdout(), "NAME\tREV\tLOCATIONS\tSUMMARY", rows)
			return nil
		},
	}
	listCmd.Flags().BoolVar(&listJSON, "json", false, "output JSON")

	var getJSON bool
	getCmd := &cobra.Command{
		Use:   "get <project>",
		Short: "Show a project's live locations and context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			var pv struct {
				ID            string             `json:"id"`
				Name          string             `json:"name"`
				Context       string             `json:"context"`
				Revision      int                `json:"revision"`
				UpdatedAt     time.Time          `json:"updated_at"`
				UpdatedByName string             `json:"updated_by_name"`
				Locations     []mcp.LocationView `json:"locations"`
			}
			if err := c.do(cmd.Context(), http.MethodGet, "/projects/"+url.PathEscape(args[0]), nil, &pv); err != nil {
				return err
			}
			if getJSON {
				return printJSON(cmd.OutOrStdout(), pv)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Project: %s (revision %d, updated %s by %s)\n", pv.Name, pv.Revision, pv.UpdatedAt.Local().Format(time.DateTime), pv.UpdatedByName)
			if len(pv.Locations) > 0 {
				fmt.Fprintln(w, "Locations:")
				for _, l := range pv.Locations {
					extra := ""
					if l.Info != nil {
						if l.Info.RealPath != "" && l.Info.RealPath != l.Info.Resolved {
							extra += " -> " + l.Info.RealPath
						}
						if l.Info.Git {
							g := l.Info.Branch
							if l.Info.Commit != "" && !strings.HasPrefix(g, "detached at ") {
								g += " (" + l.Info.Commit + ")"
							}
							extra += " [" + g + "]"
						} else if !l.Info.Exists {
							extra += " [missing]"
						}
					} else if l.Note != "" {
						extra += " [" + l.Note + "]"
					}
					fmt.Fprintf(w, "  - %s:%s%s\n", l.MachineName, l.Path, extra)
				}
			}
			if strings.TrimSpace(pv.Context) != "" {
				fmt.Fprintln(w)
				fmt.Fprintln(w, pv.Context)
			}
			return nil
		},
	}
	getCmd.Flags().BoolVar(&getJSON, "json", false, "output JSON")

	var create, updJSON bool
	var rename, oldText, newText, contextFile string
	var revision int
	var addLocs, removeLocs []string
	updateCmd := &cobra.Command{
		Use:   "update <project>",
		Short: "Create or update a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			var fullContext string
			if contextFile != "" {
				raw, err := os.ReadFile(contextFile) //nolint:gosec // User-supplied context file.
				if err != nil {
					return err
				}
				fullContext = string(raw)
			}
			parseLocs := func(specs []string) ([]mcp.LocationInput, error) {
				out := make([]mcp.LocationInput, 0, len(specs))
				for _, s := range specs {
					m, p, ok := splitRemote(s)
					if !ok || p == "" {
						return nil, fmt.Errorf("invalid location %q: expected <machine>:<path>", s)
					}
					out = append(out, mcp.LocationInput{Machine: m, Path: p})
				}
				return out, nil
			}
			add, err := parseLocs(addLocs)
			if err != nil {
				return err
			}
			rem, err := parseLocs(removeLocs)
			if err != nil {
				return err
			}
			in := mcp.UpdateProjectInput{
				Project: args[0], Create: create, Rename: rename,
				OldText: oldText, NewText: newText, Context: fullContext, Revision: revision,
				AddLocations: add, RemoveLocations: rem,
			}
			var res struct {
				Name     string `json:"name"`
				Revision int    `json:"revision"`
			}
			if err := c.do(cmd.Context(), http.MethodPost, "/projects/update", in, &res); err != nil {
				return err
			}
			if updJSON {
				return printJSON(cmd.OutOrStdout(), res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "project %q saved as revision %d\n", res.Name, res.Revision)
			return nil
		},
	}
	updateCmd.Flags().BoolVar(&create, "create", false, "create a new project")
	updateCmd.Flags().StringVar(&rename, "rename", "", "rename the project")
	updateCmd.Flags().StringVar(&oldText, "old", "", "context text to replace (must occur once)")
	updateCmd.Flags().StringVar(&newText, "new", "", "replacement text (or text to append when --old is omitted)")
	updateCmd.Flags().StringVar(&contextFile, "context-file", "", "replace whole context from a Markdown file (requires --revision when updating)")
	updateCmd.Flags().IntVar(&revision, "revision", 0, "expected revision number")
	updateCmd.Flags().StringArrayVar(&addLocs, "add-location", nil, "add <machine>:<path> (repeatable)")
	updateCmd.Flags().StringArrayVar(&removeLocs, "remove-location", nil, "remove <machine>:<path> (repeatable)")
	updateCmd.Flags().BoolVar(&updJSON, "json", false, "output JSON")

	cmd.AddCommand(listCmd, getCmd, updateCmd)
	return cmd
}
