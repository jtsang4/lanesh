package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
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

// The test server sits behind a proxy that, like Cloudflare, refuses request
// bodies above a limit; transfer parts must stay below it.
const (
	testPartBytes  = 64 << 10
	testProxyLimit = 256 << 10
)

// proxyBodyLimit buffers transfer uploads like a proxy and answers 413 with
// an HTML page when one exceeds limit. It also fails two transfer parts like
// Cloudflare: the 3rd is refused with 502 before it reaches the server, and
// the 5th reaches the server but its answer is replaced by a 524, so that the
// source sends a part the server already has.
func proxyBodyLimit(next http.Handler, limit int64) http.Handler {
	var uploads atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/transfers/") {
			body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			if int64(len(body)) > limit {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_, _ = io.WriteString(w, "<html><body><h1>413 Payload Too Large</h1></body></html>")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			if r.URL.Query().Get("wait") == "" {
				switch uploads.Add(1) {
				case 3:
					w.WriteHeader(http.StatusBadGateway)
					_, _ = io.WriteString(w, "<html>502 Bad Gateway</html>")
					return
				case 5:
					next.ServeHTTP(httptest.NewRecorder(), r)
					w.WriteHeader(524)
					_, _ = io.WriteString(w, "<html>524 A timeout occurred</html>")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func newHarness(t *testing.T, opts ...func(*Config)) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := Config{DataDir: t.TempDir(), AdminToken: testAdminToken, TransferPartBytes: testPartBytes}
	for _, opt := range opts {
		opt(&cfg)
	}
	s, _, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(proxyBodyLimit(s.Handler(), testProxyLimit))
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
	return h.mcpClientAt(h.http.URL, token)
}

// mcpClientAt connects to the lane's MCP endpoint through base, for example
// a proxy in front of the test server.
func (h *harness) mcpClientAt(base, token string) *sdkmcp.ClientSession {
	h.t.Helper()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(h.ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:   base + "/lanes/" + h.laneID + "/mcp",
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
	grep := mustCall(t, s, "grep", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy", "pattern": "PAYLOAD", "glob": "*.txt"})
	mustContain(t, "grep skill", grep, "1 match in 1 of 1 file", "scripts/data.txt:1: payload-deploy")

	// Commands run in the skill directory when skill is set.
	ex := mustCall(t, s, "exec", map[string]any{
		"machine": "alpha", "briefing": briefA, "skill": "deploy",
		"command": shellCmd("cat scripts/data.txt", "Get-Content scripts/data.txt"),
	})
	mustContain(t, "exec", ex, "exited 0", "[lanesh] begin stdout", "payload-deploy", "[lanesh] end stdout", "stderr: empty")

	// Long-running commands continue in the background.
	bg := mustCall(t, s, "exec", map[string]any{
		"machine": "alpha", "briefing": briefA, "timeout_ms": 200,
		"command": shellCmd("echo started\necho errline >&2\nsleep 30", "Write-Output started\n[Console]::Error.WriteLine('errline')\nStart-Sleep -Seconds 30"),
	})
	mustContain(t, "background exec", bg, "still running", "follow with job_read", `match="..."`)
	bgID := match(t, regexp.MustCompile(`\(job (\S+)\)\n`), bg)
	// job_read defaults to stream="both" (and auto-waits up to 30s when match is set), catching stderr readiness lines without specifying stream.
	bothRead := mustCall(t, s, "job_read", map[string]any{
		"machine": "alpha", "job_id": bgID, "match": "errline",
	})
	mustContain(t, "job_read default both+match", bothRead, "matched", "[lanesh] begin stdout", "started", "[lanesh] begin stderr", "errline", "no more output yet; the job is running")

	// job_list defaults to running jobs only and collapses multiline commands to one line.
	jobs := mustCall(t, s, "job_list", map[string]any{"machine": "alpha"})
	mustContain(t, "job_list running", jobs, "1 running job", "- "+bgID+": still running", "finished job omitted")
	if strings.Contains(jobs, "\necho errline") {
		t.Fatalf("job_list did not collapse multiline command:\n%s", jobs)
	}
	allJobs := mustCall(t, s, "job_list", map[string]any{"machine": "alpha", "include_finished": true})
	mustContain(t, "job_list finished", allJobs, "2 jobs", "- "+bgID+": still running")

	mustContain(t, "job_kill", mustCall(t, s, "job_kill", map[string]any{"machine": "alpha", "job_id": bgID}), "killed")
	read := mustCall(t, s, "job_read", map[string]any{"machine": "alpha", "job_id": bgID})
	mustContain(t, "job_read default both finished", read, "started", "errline", "end of output; the job has finished")
	readOut := mustCall(t, s, "job_read", map[string]any{"machine": "alpha", "job_id": bgID, "stream": "stdout"})
	mustContain(t, "job_read explicit stdout", readOut, "started", "end of stdout; the job has finished")

	// Files, editing (create + surgical edits + overwrite), directory listing, grep, image reading, and transfers.
	mustContain(t, "edit_file create", mustCall(t, s, "edit_file", map[string]any{"machine": "alpha", "briefing": briefA, "path": "out/report.txt", "content": "hello from alpha\nline two\nline three\n"}), "wrote 37 B")
	rf := mustCall(t, s, "read_file", map[string]any{"machine": "alpha", "briefing": briefA, "path": "~/out/report.txt"})
	mustContain(t, "read_file", rf, "(37 B)\nhello from alpha\nline two\nline three\n[lanesh] end ")
	rfLines := mustCall(t, s, "read_file", map[string]any{"machine": "alpha", "briefing": briefA, "path": "~/out/report.txt", "start_line": 2, "end_line": 3})
	mustContain(t, "read_file lines", rfLines, "lines 2-3 of 3", "2: line two", "3: line three")
	ed := mustCall(t, s, "edit_file", map[string]any{
		"machine": "alpha", "briefing": briefA, "path": "out/report.txt",
		"edits": []map[string]string{
			{"old_text": "line two", "new_text": "line TWO"},
			{"old_text": "line three", "new_text": "line THREE"},
		},
	})
	mustContain(t, "edit_file", ed, "2 edits applied", "[lanesh] begin diff", "-line two", "+line TWO", "+line THREE")
	ld := mustCall(t, s, "list_dir", map[string]any{"machine": "alpha", "briefing": briefA, "path": "out"})
	mustContain(t, "list_dir", ld, "report.txt")
	gf := mustCall(t, s, "grep", map[string]any{"machine": "alpha", "briefing": briefA, "path": "out", "pattern": "THREE", "glob": "*.txt"})
	mustContain(t, "grep files", gf, "1 match in 1 of 1 file", "report.txt:3: line THREE")
	if _, errText := call(t, s, "grep", map[string]any{"machine": "alpha", "briefing": briefA, "pattern": "THREE"}); !strings.Contains(errText, "give path or skill") {
		t.Fatalf("grep without path/skill must fail: %q", errText)
	}

	// Native image reading returns ImageContent alongside the framed text header.
	var pngBuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{G: 200, A: 255})
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	imgPath := filepath.Join(a.home, "out", "preview.png")
	if err := os.WriteFile(imgPath, pngBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	imgRes, err := s.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "read_file",
		Arguments: map[string]any{"machine": "alpha", "briefing": briefA, "path": "out/preview.png"},
	})
	if err != nil || imgRes.IsError {
		t.Fatalf("read_file image failed: %v %+v", err, imgRes)
	}
	var sawText, sawImage bool
	for _, c := range imgRes.Content {
		switch v := c.(type) {
		case *sdkmcp.TextContent:
			sawText = strings.Contains(v.Text, "image/png") && strings.HasSuffix(v.Text, "\n"+mcp.EndMarker)
		case *sdkmcp.ImageContent:
			sawImage = v.MIMEType == "image/png" && bytes.Equal(v.Data, pngBuf.Bytes())
		}
	}
	if !sawText || !sawImage {
		t.Fatalf("read_file image content missing text=%v image=%v: %+v", sawText, sawImage, imgRes.Content)
	}
	_ = os.Remove(imgPath)

	// Reset report.txt to 16 B before transfer assertions below.
	mustContain(t, "edit_file overwrite", mustCall(t, s, "edit_file", map[string]any{"machine": "alpha", "briefing": briefA, "path": "out/report.txt", "content": "hello from alpha", "overwrite": true}), "wrote 16 B")
	if _, errText := call(t, s, "edit_file", map[string]any{"machine": "alpha", "briefing": briefA, "path": "out/report.txt", "content": "x"}); !strings.Contains(errText, "exists") {
		t.Fatalf("overwrite protection: %q", errText)
	}
	tr := mustCall(t, s, "transfer", map[string]any{
		"from_machine": "alpha", "from_briefing": briefA, "from_path": "out",
		"to_machine": "beta", "to_briefing": briefB, "to_path": "inbox/from-alpha",
	})
	mustContain(t, "transfer", tr, "copied 1 file (16 B)", "(sha256 ")
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

	// A stream larger than the proxy's limit goes through in parts, both
	// incompressible and highly compressible content.
	big := make([]byte, 5*testProxyLimit)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	text := bytes.Repeat([]byte("GET /api/chat HTTP/1.1 200 {\"ok\":true}\n"), 40000)
	for name, data := range map[string][]byte{"big/random.bin": big, "big/log.txt": text} {
		p := filepath.Join(a.home, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tr = mustCall(t, s, "transfer", map[string]any{
		"from_machine": "alpha", "from_briefing": briefA, "from_path": "big",
		"to_machine": "beta", "to_briefing": briefB, "to_path": "inbox/big",
	})
	mustContain(t, "large transfer", tr, "copied 2 files")
	for name, want := range map[string][]byte{"random.bin": big, "log.txt": text} {
		got, err := os.ReadFile(filepath.Join(b.home, "inbox", "big", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("large transfer %s: %d bytes, want %d, %v", name, len(got), len(want), err)
		}
	}

	// A machine that does not allow exec refuses, and says so.
	briefG := match(t, describedToken, mustCall(t, s, "describe_machine", map[string]any{"machine": "gamma"}))
	if _, errText := call(t, s, "exec", map[string]any{"machine": "gamma", "briefing": briefG, "command": "echo hi"}); !strings.Contains(errText, "does not allow") {
		t.Fatalf("gamma exec: %q", errText)
	}

	// Once the machine's skills change:
	// - Unrelated operations (e.g. plain exec, or reading an unchanged skill)
	//   proceed with a non-blocking update notice carrying the new token and diff
	//   (no full skill list dump).
	// - Calls explicitly targeting a changed skill are blocked with a compact
	//   diff-only reply (also without dumping unchanged skills).
	writeSkill(t, a.home, "fresh")
	if err := os.WriteFile(filepath.Join(a.home, ".agents", "skills", "deploy", "scripts", "data.txt"), []byte("payload-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	updatedToken := regexp.MustCompile(`use briefing "([a-z]+:[a-z2-7]{8})"`)
	var fresh string
	for deadline := time.Now().Add(15 * time.Second); fresh == ""; {
		out, errText := call(t, s, "exec", map[string]any{"machine": "alpha", "briefing": briefA, "command": shellCmd("echo ok", "Write-Output ok")})
		if errText != "" {
			t.Fatalf("unrelated exec with historical token should not be blocked: %q", errText)
		}
		if strings.Contains(out, "+ fresh (new)") && strings.Contains(out, "~ deploy (updated") {
			if !strings.Contains(out, "ok") || strings.Contains(out, "- review:") || strings.Contains(out, "Skills (") {
				t.Fatalf("non-blocking update reply: %q", out)
			}
			fresh = match(t, updatedToken, out)
		} else if time.Now().After(deadline) {
			t.Fatalf("no precise change notice after the skills changed: %q", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Targeting the changed skill "deploy" with the old token IS blocked, with
	// a compact diff-only briefing (no unchanged skill list).
	_, errText = call(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "deploy"})
	mustContain(t, "changed skill blocked", errText, `nothing was run on "alpha"`, `skill "deploy" changed`, "~ deploy (updated", fresh)
	if strings.Contains(errText, "- review:") || strings.Contains(errText, "Skills (") {
		t.Fatalf("diff-only block must not dump full skill list:\n%s", errText)
	}
	// Reading an unchanged skill with the old token proceeds with the notice.
	unchangedRead := mustCall(t, s, "read_skill", map[string]any{"machine": "alpha", "briefing": briefA, "skill": "review"})
	mustContain(t, "unchanged skill proceeds", unchangedRead, "briefing updated", "# review")

	// Transfer skips briefing verification for the caller's own machine (you).
	selfClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-self", Version: "1"}, nil)
	selfSess, err := selfClient.Connect(h.ctx, &sdkmcp.StreamableClientTransport{
		Endpoint: h.http.URL + "/lanes/" + h.laneID + "/mcp",
		HTTPClient: &http.Client{Transport: headerTransport{map[string]string{
			"Authorization":   "Bearer " + h.access,
			mcp.HeaderMachine: "alpha",
		}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = selfSess.Close() })
	selfTr := mustCall(t, selfSess, "transfer", map[string]any{
		"from_machine": "alpha", "from_path": "out/report.txt",
		"to_machine": "beta", "to_briefing": briefB, "to_path": "inbox/from-self.txt",
	})
	mustContain(t, "self transfer", selfTr, "copied 1 file (16 B)")

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

// idleCutProxy ends a response, like Cloudflare does, when it flushes no data
// for idle. It counts the event streams that clients open with GET.
func idleCutProxy(next http.Handler, idle time.Duration, streams *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			streams.Add(1)
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		cw := &activityWriter{ResponseWriter: w, active: make(chan struct{}, 1)}
		go func() {
			t := time.NewTimer(idle)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-cw.active:
					t.Reset(idle)
				case <-t.C:
					cancel()
					return
				}
			}
		}()
		next.ServeHTTP(cw, r.WithContext(ctx))
	})
}

type activityWriter struct {
	http.ResponseWriter
	active chan struct{}
}

// Flush marks activity: only flushed data reaches the client.
func (w *activityWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
	select {
	case w.active <- struct{}{}:
	default:
	}
}

func (w *activityWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// MCP clients such as Antigravity give up on a session after the standalone
// event stream is closed a few times without new events, which an idle proxy
// does when nothing keeps the stream busy.
func TestMCPEventStreamSurvivesIdleProxy(t *testing.T) {
	const idle = 500 * time.Millisecond
	h := newHarness(t, func(c *Config) { c.SSEHeartbeat = idle / 10 })
	h.startMachine("alpha", false)
	var streams atomic.Int64
	proxy := httptest.NewServer(idleCutProxy(h.srv.Handler(), idle, &streams))
	t.Cleanup(proxy.Close)

	session := h.mcpClientAt(proxy.URL, h.access)
	deadline := time.Now().Add(5 * time.Second)
	for streams.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(4 * idle)
	if n := streams.Load(); n != 1 {
		t.Fatalf("the client opened %d event streams, want 1: the proxy closed the idle stream", n)
	}
	mustContain(t, "list_machines", mustCall(t, session, "list_machines", nil), "alpha")
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
