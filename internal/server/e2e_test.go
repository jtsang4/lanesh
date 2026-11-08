package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jtsang4/lanesh/internal/daemon"
	"github.com/jtsang4/lanesh/internal/mcp"
	"github.com/jtsang4/lanesh/internal/protocol"
)

const testAdminToken = "lsa_test_admin_token"

type harness struct {
	t      *testing.T
	ctx    context.Context
	srv    *Server
	http   *httptest.Server
	laneID string
	join   string
	access string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s, _, err := New(ctx, Config{DataDir: t.TempDir(), AdminToken: testAdminToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() { ts.Close(); _ = s.Close() })
	h := &harness{t: t, ctx: ctx, srv: s, http: ts}

	var lane LaneSummary
	h.api(http.MethodPost, "/api/v1/lanes", map[string]any{"name": "test"}, &lane, http.StatusCreated)
	h.laneID = lane.ID
	var jk, ak struct {
		Token string `json:"token"`
	}
	h.api(http.MethodPost, "/api/v1/lanes/"+lane.ID+"/keys", map[string]any{"kind": "join", "name": "j"}, &jk, http.StatusCreated)
	h.api(http.MethodPost, "/api/v1/lanes/"+lane.ID+"/keys", map[string]any{"kind": "access", "name": "a"}, &ak, http.StatusCreated)
	h.join, h.access = jk.Token, ak.Token
	return h
}

func (h *harness) api(method, path string, body, out any, want int) {
	h.t.Helper()
	h.apiHeaders(method, path, body, out, want)
}

// apiHeaders is api, returning the response headers.
func (h *harness) apiHeaders(method, path string, body, out any, want int) http.Header {
	h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequestWithContext(h.ctx, method, h.http.URL+path, &buf)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		var e map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&e)
		h.t.Fatalf("%s %s = %d (%v), want %d", method, path, resp.StatusCode, e, want)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			h.t.Fatal(err)
		}
	}
	return resp.Header
}

type machine struct {
	name  string
	home  string
	paths daemon.Paths
}

// startMachine joins a daemon with its own home directory and skills.
func (h *harness) startMachine(name string, allowExec bool, skillNames ...string) machine {
	h.t.Helper()
	home := h.t.TempDir()
	m := machine{name: name, home: home, paths: daemon.Paths{Home: filepath.Join(home, ".lanesh")}}
	for _, s := range skillNames {
		writeSkill(h.t, home, s)
	}
	_, err := daemon.Join(h.ctx, m.paths, daemon.JoinOptions{
		LaneURL: h.http.URL + "/lanes/" + h.laneID, Key: h.join, Name: name, AllowExec: &allowExec,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	d, err := daemon.New(h.ctx, daemon.Options{Paths: m.paths, Home: home, Env: os.Environ(), ReconcileInterval: 200 * time.Millisecond})
	if err != nil {
		h.t.Fatal(err)
	}
	// Stop the daemon before its temporary directories are removed
	// (cleanups run in reverse order of registration).
	dctx, dcancel := context.WithCancel(h.ctx)
	done := make(chan struct{})
	go func() { _ = d.Run(dctx); close(done) }()
	h.t.Cleanup(func() { dcancel(); <-done })
	h.waitOnline(name, true)
	return m
}

func writeSkill(t *testing.T, home, name string) {
	t.Helper()
	dir := filepath.Join(home, ".agents", "skills", name)
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: The "+name+" skill\n---\n# "+name+"\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "scripts", "data.txt"), []byte("payload-"+name), 0o600)
}

func (h *harness) waitOnline(name string, online bool) {
	h.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var views []MachineView
		h.api(http.MethodGet, "/api/v1/lanes/"+h.laneID+"/machines", nil, &views, http.StatusOK)
		for _, v := range views {
			if v.Name == name && v.Online == online && (!online || v.SkillsHash != "") {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("machine %s did not become online=%v", name, online)
}

type headerTransport struct {
	headers map[string]string
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.headers {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (h *harness) mcpClient(token string) *sdkmcp.ClientSession {
	h.t.Helper()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(h.ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:   h.http.URL + "/lanes/" + h.laneID + "/mcp",
		HTTPClient: &http.Client{Transport: headerTransport{map[string]string{"Authorization": "Bearer " + token}}},
	}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = session.Close() })
	return session
}

// call invokes a tool and returns its text, or the error text. Every result,
// error or not, must end with the end marker.
func call(t *testing.T, s *sdkmcp.ClientSession, tool string, args map[string]any) (string, string) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	text := b.String()
	if !strings.HasSuffix(text, "\n"+mcp.EndMarker) {
		t.Fatalf("%s result does not end with the end marker:\n%s", tool, text)
	}
	if res.IsError {
		return "", text
	}
	return text, ""
}

func mustCall(t *testing.T, s *sdkmcp.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	out, errText := call(t, s, tool, args)
	if errText != "" {
		t.Fatalf("%s failed: %s", tool, errText)
	}
	return out
}

// mustContain fails unless text contains every one of want.
func mustContain(t *testing.T, what, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Fatalf("%s: missing %q in:\n%s", what, w, text)
		}
	}
}

var (
	describedToken = regexp.MustCompile(`Briefing token: ([a-z]+:[a-z2-7]{8})`)
	offeredToken   = regexp.MustCompile(`with briefing "([a-z]+:[a-z2-7]{8})"`)
)

func match(t *testing.T, re *regexp.Regexp, text string) string {
	t.Helper()
	m := re.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no match for %s in:\n%s", re, text)
	}
	return m[1]
}

func shellCmd(unix, windows string) string {
	if runtime.GOOS == "windows" {
		return windows
	}
	return unix
}

func TestEndToEnd(t *testing.T) {
	h := newHarness(t)
	a := h.startMachine("alpha", true, "deploy", "review")
	b := h.startMachine("beta", true, "deploy")
	h.startMachine("gamma", false)
	s := h.mcpClient(h.access)

	// Unauthenticated access is rejected.
	resp, err := http.Post(h.http.URL+"/lanes/"+h.laneID+"/mcp", "application/json", strings.NewReader("{}")) //nolint:noctx
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated MCP request: %v %v", resp.StatusCode, err)
	}
	_ = resp.Body.Close()

	list := mustCall(t, s, "list_machines", nil)
	mustContain(t, "list_machines", list, "3 machines", "- alpha: ", "2 skills", "- gamma: ", "no commands or file access")

	// The instructions carry the usage essentials and a roster of the lane.
	instr := s.InitializeResult().Instructions
	if len(instr) > 2048 || !strings.Contains(instr[:min(512, len(instr))], "briefing") ||
		!strings.Contains(instr, "- alpha: ") || !strings.Contains(instr, "no exec") {
		t.Fatalf("instructions (%d chars):\n%s", len(instr), instr)
	}

	// Without a briefing token nothing runs; the reply is the briefing itself.
	_, errText := call(t, s, "exec", map[string]any{"machine": "alpha", "briefing": "", "command": "echo hi"})
	mustContain(t, "exec without briefing", errText, `[lanesh] nothing was run on "alpha"`, "- deploy: The deploy skill")
	offered := match(t, offeredToken, errText)

	da := mustCall(t, s, "describe_machine", map[string]any{"machine": "alpha"})
	briefA := match(t, describedToken, da)
	if briefA != offered {
		t.Fatalf("describe token %q differs from the offered one %q", briefA, offered)
	}
	if again := mustCall(t, s, "describe_machine", map[string]any{"machine": "alpha"}); match(t, describedToken, again) != briefA {
		t.Fatal("briefing tokens must be stable while the machine is unchanged")
	}
	mustContain(t, "describe alpha", da, "Skills (2) in ", "- deploy: The deploy skill", "- review: The review skill")
	briefB := match(t, describedToken, mustCall(t, s, "describe_machine", map[string]any{"machine": "beta"}))

	// A briefing for one machine does not unlock another.
	if _, errText := call(t, s, "exec", map[string]any{"machine": "beta", "briefing": briefA, "command": "echo hi"}); !strings.Contains(errText, "unknown") {
		t.Fatalf("briefing of alpha used for beta: %q", errText)
	}

	// A transfer with two missing tokens returns both briefings at once.
	_, errText = call(t, s, "transfer", map[string]any{
		"from_machine": "alpha", "from_briefing": "", "from_path": "x", "to_machine": "beta", "to_briefing": "", "to_path": "y",
	})
	mustContain(t, "transfer briefings", errText, `nothing was run on "alpha"`, `nothing was run on "beta"`, `to_briefing "beta:`)
	if strings.Count(errText, mcp.EndMarker) != 1 {
		t.Fatalf("joined briefings must end once:\n%s", errText)
	}

	// Finding skills by keyword, across machines and on one machine.
	found := mustCall(t, s, "find_skills", map[string]any{"query": "deploy"})
	mustContain(t, "find_skills", found, "- deploy (on alpha, beta; identical): The deploy skill", "describe_machine, then read_skill")
	if strings.Contains(found, "- review") {
		t.Fatalf("find_skills matched too much:\n%s", found)
	}
	mustContain(t, "find_skills machine", mustCall(t, s, "find_skills", map[string]any{"machine": "alpha"}), "2 skills on \"alpha\"", "- review: The review skill")

	sk := mustCall(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy"})
	mustContain(t, "read_skill", sk, "[lanesh] begin SKILL.md", "# deploy", "[lanesh] end SKILL.md", "Files:", "scripts/", "  data.txt (14 B)")
	file := mustCall(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy", "path": "scripts/data.txt"})
	mustContain(t, "read_skill file", file, "[lanesh] begin scripts/data.txt (14 B)\npayload-deploy\n[lanesh] end scripts/data.txt")
	part := mustCall(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy", "path": "scripts/data.txt", "limit": 7})
	mustContain(t, "partial read", part, "bytes 0-7", "payload\n", "continues: 7 more bytes; read on with offset=7")
	dir := mustCall(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy", "path": "scripts/"})
	mustContain(t, "read_skill directory", dir, "directory scripts/", "\ndata.txt (14 B)")
	both := mustCall(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy", "paths": []string{"SKILL.md", "scripts/data.txt", "missing.md"}})
	if _, errText := call(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy", "paths": []string{"SKILL.md"}, "offset": 5}); !strings.Contains(errText, "use path with offset") {
		t.Fatalf("paths with offset: %q", errText)
	}
	mustContain(t, "read_skill paths", both, "begin SKILL.md", "begin scripts/data.txt", "[lanesh] error: missing.md")
	if _, errText := call(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy", "path": "../../.lanesh/credentials.toml"}); errText == "" {
		t.Fatal("path traversal must be rejected")
	}
	grep := mustCall(t, s, "grep_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy", "pattern": "PAYLOAD"})
	mustContain(t, "grep_skill", grep, "1 match in 1 of 2 files", "scripts/data.txt:1: payload-deploy")

	// Commands run in the skill directory when skill is set.
	ex := mustCall(t, s, "exec", map[string]any{
		"machine": "alpha", "briefing": briefA, "skill": "deploy",
		"command": shellCmd("cat scripts/data.txt", "Get-Content scripts/data.txt"),
	})
	mustContain(t, "exec", ex, "exited 0", "[lanesh] begin stdout", "payload-deploy", "[lanesh] end stdout", "stderr: empty")

	// Long-running commands continue in the background.
	bg := mustCall(t, s, "exec", map[string]any{
		"machine": "alpha", "briefing": briefA, "timeout_ms": 200,
		"command": shellCmd("echo started; sleep 30", "Write-Output started; Start-Sleep -Seconds 30"),
	})
	mustContain(t, "background exec", bg, "still running", "follow with job_read")
	bgID := match(t, regexp.MustCompile(`\(job (\S+)\)\n`), bg)
	// Wait for the first line so that the shell has really started.
	for deadline := time.Now().Add(20 * time.Second); ; {
		r := mustCall(t, s, "job_read", map[string]any{"machine": "alpha", "job_id": bgID, "wait_ms": 1000})
		if strings.Contains(r, "started") {
			mustContain(t, "job_read running", r, "no more stdout yet; the job is running")
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background job produced no output: %v", r)
		}
	}
	jobs := mustCall(t, s, "job_list", map[string]any{"machine": "alpha"})
	mustContain(t, "job_list", jobs, "2 jobs", "- "+bgID+": still running")
	mustContain(t, "job_kill", mustCall(t, s, "job_kill", map[string]any{"machine": "alpha", "job_id": bgID}), "killed")
	read := mustCall(t, s, "job_read", map[string]any{"machine": "alpha", "job_id": bgID})
	mustContain(t, "job_read", read, "started", "end of stdout; the job has finished")

	// Files and transfers.
	mustContain(t, "write_file", mustCall(t, s, "write_file", map[string]any{"machine": "alpha", "briefing": briefA, "path": "out/report.txt", "content": "hello from alpha", "create_dirs": true}), "wrote 16 B")
	rf := mustCall(t, s, "read_file", map[string]any{"machine": "alpha", "briefing": briefA, "path": "~/out/report.txt"})
	mustContain(t, "read_file", rf, "(16 B)\nhello from alpha\n[lanesh] end ")
	if _, errText := call(t, s, "write_file", map[string]any{"machine": "alpha", "briefing": briefA, "path": "out/report.txt", "content": "x"}); !strings.Contains(errText, "exists") {
		t.Fatalf("overwrite protection: %q", errText)
	}
	tr := mustCall(t, s, "transfer", map[string]any{
		"from_machine": "alpha", "from_briefing": briefA, "from_path": "out",
		"to_machine": "beta", "to_briefing": briefB, "to_path": "inbox/from-alpha",
	})
	mustContain(t, "transfer", tr, "copied 1 file (16 B)")
	got, err := os.ReadFile(filepath.Join(b.home, "inbox", "from-alpha", "report.txt"))
	if err != nil || string(got) != "hello from alpha" {
		t.Fatalf("transferred file = %q, %v", got, err)
	}
	if _, errText := call(t, s, "transfer", map[string]any{
		"from_machine": "alpha", "from_briefing": briefA, "from_path": "out",
		"to_machine": "beta", "to_briefing": briefB, "to_path": "inbox/from-alpha",
	}); !strings.Contains(errText, "exists") {
		t.Fatalf("transfer overwrite protection: %q", errText)
	}

	// A machine that does not allow exec refuses, and says so.
	briefG := match(t, describedToken, mustCall(t, s, "describe_machine", map[string]any{"machine": "gamma"}))
	if _, errText := call(t, s, "exec", map[string]any{"machine": "gamma", "briefing": briefG, "command": "echo hi"}); !strings.Contains(errText, "does not allow") {
		t.Fatalf("gamma exec: %q", errText)
	}

	// Once the machine's skills change, the old token yields a fresh briefing
	// that says exactly what changed relative to that token, and the new token works.
	writeSkill(t, a.home, "fresh")
	if err := os.WriteFile(filepath.Join(a.home, ".agents", "skills", "deploy", "scripts", "data.txt"), []byte("payload-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	var fresh string
	for deadline := time.Now().Add(15 * time.Second); fresh == ""; {
		_, errText := call(t, s, "exec", map[string]any{"machine": "alpha", "briefing": briefA, "command": shellCmd("true", "$null")})
		if strings.Contains(errText, "+ fresh (new)") && strings.Contains(errText, "~ deploy (updated") {
			if !strings.Contains(errText, "changed since your last briefing") || strings.Contains(errText, "~ review") {
				t.Fatalf("change report: %q", errText)
			}
			fresh = match(t, offeredToken, errText)
		} else if time.Now().After(deadline) {
			t.Fatalf("no precise change report after the skills changed: %q", errText)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// A new session has no token: it gets the current briefing, with nothing
	// marked as changed. An unrecognized token gets no change report either.
	s2 := h.mcpClient(h.access)
	_, errText = call(t, s2, "exec", map[string]any{"machine": "alpha", "briefing": "", "command": "echo hi"})
	if !strings.Contains(errText, "- fresh: The fresh skill") || strings.Contains(errText, "Changes since") {
		t.Fatalf("new session briefing: %q", errText)
	}
	_, errText = call(t, s2, "exec", map[string]any{"machine": "alpha", "briefing": "alpha:aaaaaaaa", "command": "echo hi"})
	if !strings.Contains(errText, "unknown") || strings.Contains(errText, "Changes since") {
		t.Fatalf("unknown token briefing: %q", errText)
	}
	if fresh == briefA {
		t.Fatal("token must change with the skills")
	}
	mustCall(t, s, "exec", map[string]any{"machine": "alpha", "briefing": fresh, "command": shellCmd("true", "$null")})

	// Plain errors are framed too.
	if _, errText := call(t, s, "find_skills", map[string]any{}); !strings.HasPrefix(errText, "[lanesh] error: ") {
		t.Fatalf("framed error: %q", errText)
	}
	// So are argument errors found by the SDK before a tool runs (call checks the end marker).
	if _, errText := call(t, s, "job_read", map[string]any{"machine": "alpha", "job_id": "x", "offset": "5"}); !strings.HasPrefix(errText, "[lanesh] error: ") {
		t.Fatalf("argument type error: %q", errText)
	}
	if _, errText := call(t, s, "list_machines", map[string]any{"bogus": 1}); errText == "" {
		t.Fatal("unknown arguments must be rejected")
	}

	// Admin views.
	var laneSkills []LaneSkill
	h.api(http.MethodGet, "/api/v1/lanes/"+h.laneID+"/skills", nil, &laneSkills, http.StatusOK)
	var deploy *LaneSkill
	for i := range laneSkills {
		if laneSkills[i].Name == "deploy" {
			deploy = &laneSkills[i]
		}
	}
	// alpha's copy of deploy was modified above, so the two copies now differ.
	if deploy == nil || len(deploy.Machines) != 2 || deploy.Consistent {
		t.Fatalf("lane skills = %+v", laneSkills)
	}
	var audit []map[string]any
	h.api(http.MethodGet, "/api/v1/lanes/"+h.laneID+"/audit?action=exec", nil, &audit, http.StatusOK)
	if len(audit) < 3 {
		t.Fatalf("audit = %v", audit)
	}

	// Removing a machine disconnects it.
	var views []MachineView
	h.api(http.MethodGet, "/api/v1/lanes/"+h.laneID+"/machines", nil, &views, http.StatusOK)
	for _, v := range views {
		if v.Name == "beta" {
			h.api(http.MethodDelete, "/api/v1/lanes/"+h.laneID+"/machines/"+v.ID, nil, nil, http.StatusNoContent)
		}
	}
	if _, errText := call(t, s, "describe_machine", map[string]any{"machine": "beta"}); !strings.Contains(errText, "no machine") {
		t.Fatalf("removed machine: %q", errText)
	}
	_ = fmt.Sprint(b)
}

func TestJoinCannotTakeOverAMachine(t *testing.T) {
	h := newHarness(t)
	a := h.startMachine("alpha", true)
	id, err := daemon.MachineID(a.paths)
	if err != nil {
		t.Fatal(err)
	}

	// Someone with only the join key claims alpha's machine ID.
	body, _ := json.Marshal(map[string]any{"machine_id": id, "name": "evil", "policy": map[string]bool{"allow_exec": true}})
	req, _ := http.NewRequestWithContext(h.ctx, http.MethodPost, h.http.URL+"/lanes/"+h.laneID+"/join", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+h.join)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("takeover join = %d, want 409", resp.StatusCode)
	}
	h.waitOnline("alpha", true)

	// The machine itself can re-join: it proves its current credential.
	if _, err := daemon.Join(h.ctx, a.paths, daemon.JoinOptions{LaneURL: h.http.URL + "/lanes/" + h.laneID, Key: h.join}); err != nil {
		t.Fatalf("re-join from the machine: %v", err)
	}
	h.waitOnline("alpha", true)

	// Names can never collide with machine IDs.
	if got := sanitizeName("mch_"+strings.ToLower(id[4:]), ""); strings.HasPrefix(got, "mch_") {
		t.Fatalf("sanitizeName kept the ID prefix: %q", got)
	}
}

func TestCancelledCallsKeepTheConnection(t *testing.T) {
	h := newHarness(t)
	h.startMachine("alpha", true)
	var views []MachineView
	h.api(http.MethodGet, "/api/v1/lanes/"+h.laneID+"/machines", nil, &views, http.StatusOK)
	id := views[0].ID

	var st protocol.JobStatus
	cmd := shellCmd("sleep 30", "Start-Sleep -Seconds 30")
	if err := h.srv.call(h.ctx, h.laneID, id, protocol.MethodExecStart, protocol.ExecParams{Command: cmd, Background: true}, &st); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		ctx, cancel := context.WithTimeout(h.ctx, 30*time.Millisecond)
		_ = h.srv.call(ctx, h.laneID, id, protocol.MethodJobRead, protocol.JobReadParams{JobID: st.JobID, WaitMS: 2000}, nil)
		cancel()
	}
	time.Sleep(300 * time.Millisecond)
	var res protocol.JobReadResult
	if err := h.srv.call(h.ctx, h.laneID, id, protocol.MethodJobRead, protocol.JobReadParams{JobID: st.JobID}, &res); err != nil {
		t.Fatalf("connection lost after cancelled calls: %v", err)
	}
	_ = h.srv.call(h.ctx, h.laneID, id, protocol.MethodJobKill, protocol.JobKillParams{JobID: st.JobID}, nil)
}

func TestAdminAuthAndInstallScripts(t *testing.T) {
	h := newHarness(t)

	req, _ := http.NewRequestWithContext(h.ctx, http.MethodGet, h.http.URL+"/api/v1/lanes", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API: %v %v", resp.StatusCode, err)
	}
	_ = resp.Body.Close()

	login := func(token string) *http.Response { //nolint:bodyclose // The body is closed inside.
		body, _ := json.Marshal(map[string]string{"token": token})
		req, _ := http.NewRequestWithContext(h.ctx, http.MethodPost, h.http.URL+"/api/v1/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}
	if login("wrong").StatusCode != http.StatusUnauthorized { //nolint:bodyclose // Closed in login.
		t.Fatal("wrong token must fail")
	}
	ok := login(testAdminToken) //nolint:bodyclose // Closed in login.
	var cookie *http.Cookie
	for _, c := range ok.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %+v", cookie)
	}
	req, _ = http.NewRequestWithContext(h.ctx, http.MethodGet, h.http.URL+"/api/v1/info", nil)
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("cookie auth: %v %v", resp.StatusCode, err)
	}
	_ = resp.Body.Close()

	for _, p := range []string{"/install.sh", "/install.ps1"} {
		req, _ := http.NewRequestWithContext(h.ctx, http.MethodGet, h.http.URL+p, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %v", p, err)
		}
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		_ = resp.Body.Close()
		if !strings.Contains(buf.String(), h.http.URL) || !strings.Contains(buf.String(), "latest") {
			t.Fatalf("%s not rendered: %s", p, buf.String()[:200])
		}
	}

	// Join requires a valid join key.
	_, err = daemon.Join(h.ctx, daemon.Paths{Home: t.TempDir()}, daemon.JoinOptions{LaneURL: h.http.URL + "/lanes/" + h.laneID, Key: "lsj_wrong"})
	if err == nil || !strings.Contains(err.Error(), "invalid join key") {
		t.Fatalf("bad join key: %v", err)
	}
}
