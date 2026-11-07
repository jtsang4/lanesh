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

// call invokes a tool and returns its structured result, or the error text.
func call(t *testing.T, s *sdkmcp.ClientSession, tool string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError {
		var msg strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*sdkmcp.TextContent); ok {
				msg.WriteString(tc.Text)
			}
		}
		return nil, msg.String()
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out == nil {
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &out)
	}
	return out, ""
}

func mustCall(t *testing.T, s *sdkmcp.ClientSession, tool string, args map[string]any) map[string]any {
	t.Helper()
	out, errText := call(t, s, tool, args)
	if errText != "" {
		t.Fatalf("%s failed: %s", tool, errText)
	}
	return out
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
	if n := len(list["machines"].([]any)); n != 3 {
		t.Fatalf("machines = %v", list)
	}

	// The instructions carry the usage essentials and a roster of the lane.
	instr := s.InitializeResult().Instructions
	if len(instr) > 2048 || !strings.Contains(instr[:min(512, len(instr))], "briefing") ||
		!strings.Contains(instr, "- alpha: ") || !strings.Contains(instr, "no exec") {
		t.Fatalf("instructions (%d chars):\n%s", len(instr), instr)
	}

	// Without a briefing token nothing runs; the reply is the briefing itself.
	_, errText := call(t, s, "exec", map[string]any{"machine": "alpha", "briefing": "", "command": "echo hi"})
	if !strings.Contains(errText, `Nothing was run on "alpha"`) || !strings.Contains(errText, "- deploy: The deploy skill") {
		t.Fatalf("exec without briefing: %q", errText)
	}
	offered := regexp.MustCompile(`briefing "(alpha:[a-z2-7]{8})"`).FindStringSubmatch(errText)
	if offered == nil {
		t.Fatalf("no token offered: %q", errText)
	}

	da := mustCall(t, s, "describe_machine", map[string]any{"machine": "alpha"})
	briefA := da["briefing"].(string)
	if briefA != offered[1] {
		t.Fatalf("describe token %q differs from the offered one %q", briefA, offered[1])
	}
	if again := mustCall(t, s, "describe_machine", map[string]any{"machine": "alpha"}); again["briefing"] != briefA {
		t.Fatal("briefing tokens must be stable while the machine is unchanged")
	}
	if skills := da["skills"].([]any); len(skills) != 2 {
		t.Fatalf("alpha skills = %v", skills)
	}
	db := mustCall(t, s, "describe_machine", map[string]any{"machine": "beta"})
	briefB := db["briefing"].(string)

	// A briefing for one machine does not unlock another.
	if _, errText := call(t, s, "exec", map[string]any{"machine": "beta", "briefing": briefA, "command": "echo hi"}); !strings.Contains(errText, "unknown") {
		t.Fatalf("briefing of alpha used for beta: %q", errText)
	}

	// A transfer with two missing tokens returns both briefings at once.
	_, errText = call(t, s, "transfer", map[string]any{
		"from_machine": "alpha", "from_briefing": "", "from_path": "x", "to_machine": "beta", "to_briefing": "", "to_path": "y",
	})
	if !strings.Contains(errText, `Nothing was run on "alpha"`) || !strings.Contains(errText, `Nothing was run on "beta"`) {
		t.Fatalf("transfer briefings: %q", errText)
	}

	sk := mustCall(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy"})
	if !strings.Contains(sk["content"].(string), "# deploy") || len(sk["files"].([]any)) == 0 {
		t.Fatalf("read_skill = %v", sk)
	}
	file := mustCall(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy", "path": "scripts/data.txt"})
	if file["content"] != "payload-deploy" {
		t.Fatalf("read_skill file = %v", file)
	}
	if _, errText := call(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy", "path": "../../.lanesh/credentials.toml"}); errText == "" {
		t.Fatal("path traversal must be rejected")
	}

	// Commands run in the skill directory when skill is set.
	ex := mustCall(t, s, "exec", map[string]any{
		"machine": "alpha", "briefing": briefA, "skill": "deploy",
		"command": shellCmd("cat scripts/data.txt", "Get-Content scripts/data.txt"),
	})
	if ex["status"] != "exited" || strings.TrimSpace(ex["stdout"].(string)) != "payload-deploy" {
		t.Fatalf("exec = %v", ex)
	}

	// Long-running commands continue in the background.
	bg := mustCall(t, s, "exec", map[string]any{
		"machine": "alpha", "briefing": briefA, "timeout_ms": 200,
		"command": shellCmd("echo started; sleep 30", "Write-Output started; Start-Sleep -Seconds 30"),
	})
	if bg["status"] != "running" {
		t.Fatalf("background exec = %v", bg)
	}
	// Wait for the first line so that the shell has really started.
	for deadline := time.Now().Add(20 * time.Second); ; {
		r := mustCall(t, s, "job_read", map[string]any{"machine": "alpha", "job_id": bg["job_id"]})
		if strings.Contains(r["data"].(string), "started") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background job produced no output: %v", r)
		}
		time.Sleep(100 * time.Millisecond)
	}
	jobs := mustCall(t, s, "job_list", map[string]any{"machine": "alpha"})
	if len(jobs["jobs"].([]any)) < 2 {
		t.Fatalf("job_list = %v", jobs)
	}
	killed := mustCall(t, s, "job_kill", map[string]any{"machine": "alpha", "job_id": bg["job_id"]})
	if killed["status"] != "killed" {
		t.Fatalf("job_kill = %v", killed)
	}
	read := mustCall(t, s, "job_read", map[string]any{"machine": "alpha", "job_id": bg["job_id"]})
	if !strings.Contains(read["data"].(string), "started") || read["eof"] != true {
		t.Fatalf("job_read = %v", read)
	}

	// Files and transfers.
	mustCall(t, s, "write_file", map[string]any{"machine": "alpha", "briefing": briefA, "path": "out/report.txt", "content": "hello from alpha", "create_dirs": true})
	rf := mustCall(t, s, "read_file", map[string]any{"machine": "alpha", "briefing": briefA, "path": "~/out/report.txt"})
	if rf["content"] != "hello from alpha" {
		t.Fatalf("read_file = %v", rf)
	}
	if _, errText := call(t, s, "write_file", map[string]any{"machine": "alpha", "briefing": briefA, "path": "out/report.txt", "content": "x"}); !strings.Contains(errText, "exists") {
		t.Fatalf("overwrite protection: %q", errText)
	}
	tr := mustCall(t, s, "transfer", map[string]any{
		"from_machine": "alpha", "from_briefing": briefA, "from_path": "out",
		"to_machine": "beta", "to_briefing": briefB, "to_path": "inbox/from-alpha",
	})
	if tr["files"].(float64) != 1 {
		t.Fatalf("transfer = %v", tr)
	}
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
	dg := mustCall(t, s, "describe_machine", map[string]any{"machine": "gamma"})
	if _, errText := call(t, s, "exec", map[string]any{"machine": "gamma", "briefing": dg["briefing"], "command": "echo hi"}); !strings.Contains(errText, "does not allow") {
		t.Fatalf("gamma exec: %q", errText)
	}

	// Once the machine's skills change, the old token yields a fresh briefing
	// that lists the new skill, and the new token works.
	writeSkill(t, a.home, "fresh")
	var fresh string
	for deadline := time.Now().Add(15 * time.Second); fresh == ""; {
		_, errText := call(t, s, "exec", map[string]any{"machine": "alpha", "briefing": briefA, "command": shellCmd("true", "$null")})
		if strings.Contains(errText, "- fresh: The fresh skill") {
			fresh = regexp.MustCompile(`briefing "(alpha:[a-z2-7]{8})"`).FindStringSubmatch(errText)[1]
		} else if time.Now().After(deadline) {
			t.Fatalf("old token still accepted after the skills changed: %q", errText)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if fresh == briefA {
		t.Fatal("token must change with the skills")
	}
	mustCall(t, s, "exec", map[string]any{"machine": "alpha", "briefing": fresh, "command": shellCmd("true", "$null")})

	// Admin views.
	var laneSkills []LaneSkill
	h.api(http.MethodGet, "/api/v1/lanes/"+h.laneID+"/skills", nil, &laneSkills, http.StatusOK)
	var deploy *LaneSkill
	for i := range laneSkills {
		if laneSkills[i].Name == "deploy" {
			deploy = &laneSkills[i]
		}
	}
	if deploy == nil || len(deploy.Machines) != 2 || !deploy.Consistent {
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
