package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jtsang4/lanesh/internal/daemon"
	"github.com/jtsang4/lanesh/internal/server"
)

func TestSplitRemoteAndLineRange(t *testing.T) {
	cases := []struct {
		spec    string
		machine string
		path    string
		ok      bool
	}{
		{"devbox:~/repo", "devbox", "~/repo", true},
		{"devbox:/tmp/a.txt", "devbox", "/tmp/a.txt", true},
		{"devbox:", "devbox", "", true},
		{"C:\\Users\\a\\f.txt", "", "C:\\Users\\a\\f.txt", false},
		{"d:/work/file.txt", "", "d:/work/file.txt", false},
		{"./local/file.txt", "", "./local/file.txt", false},
		{"/abs/with:colon", "", "/abs/with:colon", false},
		{"plain", "", "plain", false},
	}
	for _, tc := range cases {
		m, p, ok := splitRemote(tc.spec)
		if m != tc.machine || p != tc.path || ok != tc.ok {
			t.Errorf("splitRemote(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.spec, m, p, ok, tc.machine, tc.path, tc.ok)
		}
	}

	lineCases := []struct {
		spec       string
		start, end int
		wantErr    bool
	}{
		{"", 0, 0, false},
		{"5", 5, 5, false},
		{"10-20", 10, 20, false},
		{"10:20", 10, 20, false},
		{"10-", 10, 0, false},
		{"-20", 0, 20, false},
		{"20-10", 0, 0, true},
		{"0-5", 0, 0, true},
		{"-", 0, 0, true},
	}
	for _, tc := range lineCases {
		s, e, err := parseLineRange(tc.spec)
		if (err != nil) != tc.wantErr || (!tc.wantErr && (s != tc.start || e != tc.end)) {
			t.Errorf("parseLineRange(%q) = (%d, %d, %v), want (%d, %d, err=%v)", tc.spec, s, e, err, tc.start, tc.end, tc.wantErr)
		}
	}

	srv, lane, err := daemon.ParseClientLaneURL("http://127.0.0.1:8080/lanes/ln_123/mcp")
	if err != nil || srv != "http://127.0.0.1:8080" || lane != "ln_123" {
		t.Fatalf("ParseClientLaneURL(/mcp) = (%q, %q, %v)", srv, lane, err)
	}
	srv, lane, err = daemon.ParseClientLaneURL("https://lanesh.example.com/lanes/ln_123/v1/")
	if err != nil || srv != "https://lanesh.example.com" || lane != "ln_123" {
		t.Fatalf("ParseClientLaneURL(/v1/) = (%q, %q, %v)", srv, lane, err)
	}
}

func runCLI(t *testing.T, laneshHome string, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("LANESH_HOME", laneshHome)
	t.Setenv("LANESH_LANE_URL", "")
	t.Setenv("LANESH_MCP_URL", "")
	t.Setenv("LANESH_ACCESS_KEY", "")
	t.Setenv("LANESH_MACHINE", "")
	cmd := newRootCmd()
	var outB, errB bytes.Buffer
	cmd.SetOut(&outB)
	cmd.SetErr(&errB)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outB.String(), errB.String(), err
}

func cliShellCmd(unix, windows string) string {
	if runtime.GOOS == "windows" {
		return windows
	}
	return unix
}

func TestConsumerCLIEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const adminTok = "lsa_cli_test_token"
	srv, _, err := server.New(ctx, server.Config{DataDir: t.TempDir(), AdminToken: adminTok})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); _ = srv.Close() })

	adminDo := func(method, path string, body, out any) {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req, _ := http.NewRequestWithContext(ctx, method, ts.URL+path, &buf)
		req.Header.Set("Authorization", "Bearer "+adminTok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			t.Fatalf("%s %s = %d", method, path, resp.StatusCode)
		}
		if out != nil {
			_ = json.NewDecoder(resp.Body).Decode(out)
		}
	}

	var lane struct{ ID string }
	adminDo(http.MethodPost, "/api/v1/lanes", map[string]string{"name": "cli"}, &lane)
	var jk, ak struct{ Token string }
	adminDo(http.MethodPost, "/api/v1/lanes/"+lane.ID+"/keys", map[string]any{"kind": "join", "name": "j"}, &jk)
	adminDo(http.MethodPost, "/api/v1/lanes/"+lane.ID+"/keys", map[string]any{"kind": "access", "name": "a"}, &ak)

	startMachine := func(name string) (home string, lHome string) {
		home = t.TempDir()
		lHome = filepath.Join(home, ".lanesh")
		skDir := filepath.Join(home, ".agents", "skills", "ops")
		_ = os.MkdirAll(skDir, 0o755)
		_ = os.WriteFile(filepath.Join(skDir, "SKILL.md"), []byte("---\nname: ops\ndescription: Ops helper\n---\n# Ops\nneedle-line\n"), 0o600)
		allow := true
		if _, err := daemon.Join(ctx, daemon.Paths{Home: lHome}, daemon.JoinOptions{
			LaneURL: ts.URL + "/lanes/" + lane.ID, Key: jk.Token, Name: name, AllowExec: &allow,
		}); err != nil {
			t.Fatal(err)
		}
		d, err := daemon.New(ctx, daemon.Options{
			Paths: daemon.Paths{Home: lHome}, Home: home, Env: os.Environ(), ReconcileInterval: 200 * time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		dctx, dcancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { _ = d.Run(dctx); close(done) }()
		t.Cleanup(func() { dcancel(); <-done })
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			var views []server.MachineView
			adminDo(http.MethodGet, "/api/v1/lanes/"+lane.ID+"/machines", nil, &views)
			for _, v := range views {
				if v.Name == name && v.Online && v.SkillsHash != "" {
					return home, lHome
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("machine %s did not come online", name)
		return "", ""
	}

	alphaHome, alphaLanesh := startMachine("alpha")
	betaHome, _ := startMachine("beta")
	clientHome := t.TempDir() // Unjoined client home.

	// Connect with /mcp suffix (auto-stripped).
	out, _, err := runCLI(t, clientHome, "", "connect", ts.URL+"/lanes/"+lane.ID+"/mcp", "--key", ak.Token)
	if err != nil || !strings.Contains(out, "2/2 machines online") {
		t.Fatalf("connect = %q, %v", out, err)
	}

	// Machines, describe, skills.
	out, _, err = runCLI(t, clientHome, "", "machines")
	if err != nil || !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("machines = %q, %v", out, err)
	}
	out, _, err = runCLI(t, clientHome, "", "describe", "alpha")
	if err != nil || !strings.Contains(out, "ops: Ops helper") {
		t.Fatalf("describe = %q, %v", out, err)
	}
	out, _, err = runCLI(t, clientHome, "", "skills", "ops")
	if err != nil || !strings.Contains(out, "ops") {
		t.Fatalf("skills = %q, %v", out, err)
	}
	out, _, err = runCLI(t, clientHome, "", "skills", "read", "alpha", "ops")
	if err != nil || !strings.Contains(out, "# Ops") {
		t.Fatalf("skills read = %q, %v", out, err)
	}
	out, _, err = runCLI(t, clientHome, "", "skills", "grep", "alpha", "ops", "needle")
	if err != nil || !strings.Contains(out, "SKILL.md:") {
		t.Fatalf("skills grep = %q, %v", out, err)
	}

	// Exec: fast, large untruncated (>10 KB), non-zero exit code, --bg + jobs.
	out, _, err = runCLI(t, clientHome, "", "exec", "alpha", "--", cliShellCmd("echo cli-ok", "Write-Output cli-ok"))
	if err != nil || strings.TrimSpace(out) != "cli-ok" {
		t.Fatalf("exec = %q, %v", out, err)
	}
	// Large output exceeding 6.5 KB inline cap should be streamed untruncated from the ring buffer.
	bigCmd := cliShellCmd("for i in $(seq 1 400); do echo line-$i-0123456789abcdef; done", "1..400 | ForEach-Object { Write-Output \"line-$_-0123456789abcdef\" }")
	out, _, err = runCLI(t, clientHome, "", "exec", "alpha", "--", bigCmd)
	if err != nil || !strings.Contains(out, "line-1-0123456789abcdef") || !strings.Contains(out, "line-200-0123456789abcdef") || !strings.Contains(out, "line-400-0123456789abcdef") {
		t.Fatalf("large exec len=%d err=%v", len(out), err)
	}
	_, _, err = runCLI(t, clientHome, "", "exec", "alpha", "--", cliShellCmd("exit 7", "exit 7"))
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 7 {
		t.Fatalf("non-zero exec err = %v, want exit 7", err)
	}
	_, timeoutErr, err := runCLI(t, clientHome, "", "exec", "alpha", "--timeout", "250ms", "--", cliShellCmd("sleep 10", "Start-Sleep -Seconds 10"))
	if !errors.As(err, &ee) || ee.code != 124 || !strings.Contains(timeoutErr, "still running") {
		t.Fatalf("timeout exec err = %v, stderr = %q, want exit 124", err, timeoutErr)
	}
	bgOut, _, err := runCLI(t, clientHome, "", "exec", "alpha", "--bg", "--", cliShellCmd("echo bg-ready; sleep 30", "Write-Output bg-ready; Start-Sleep -Seconds 30"))
	if err != nil {
		t.Fatalf("exec --bg: %v", err)
	}
	jobID := strings.TrimSpace(bgOut)
	out, _, err = runCLI(t, clientHome, "", "jobs", "alpha")
	if err != nil || !strings.Contains(out, jobID) {
		t.Fatalf("jobs = %q, %v", out, err)
	}
	out, _, err = runCLI(t, clientHome, "", "jobs", "read", "alpha", jobID, "--wait", "10s", "--match", "bg-ready")
	if err != nil || !strings.Contains(out, "bg-ready") {
		t.Fatalf("jobs read = %q, %v", out, err)
	}
	if _, _, err = runCLI(t, clientHome, "", "jobs", "kill", "alpha", jobID); err != nil {
		t.Fatalf("jobs kill: %v", err)
	}

	// Write, edit, read (lines + PNG binary), ls, grep.
	out, _, err = runCLI(t, clientHome, "alpha\nbeta\ngamma\n", "write", "alpha:notes/doc.txt", "--create-dirs")
	if err != nil || !strings.Contains(out, "wrote 17 B") {
		t.Fatalf("write = %q, %v", out, err)
	}
	out, _, err = runCLI(t, clientHome, "", "edit", "alpha:notes/doc.txt", "--old", "beta", "--new", "BETA")
	if err != nil || !strings.Contains(out, "+BETA") {
		t.Fatalf("edit = %q, %v", out, err)
	}
	out, _, err = runCLI(t, clientHome, "", "read", "alpha:notes/doc.txt", "--lines", "2-3")
	if err != nil || out != "BETA\ngamma\n" {
		t.Fatalf("read --lines = %q, %v", out, err)
	}
	out, _, err = runCLI(t, clientHome, "", "ls", "alpha:notes")
	if err != nil || !strings.Contains(out, "doc.txt") {
		t.Fatalf("ls = %q, %v", out, err)
	}
	out, _, err = runCLI(t, clientHome, "", "grep", "alpha:notes", "BETA")
	if err != nil || !strings.Contains(out, "doc.txt:2: BETA") {
		t.Fatalf("grep = %q, %v", out, err)
	}

	// Binary PNG read with -o.
	var pngBuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{B: 255, A: 255})
	_ = png.Encode(&pngBuf, img)
	_ = os.WriteFile(filepath.Join(alphaHome, "notes", "img.png"), pngBuf.Bytes(), 0o600)
	localImg := filepath.Join(t.TempDir(), "downloaded.png")
	if _, _, err = runCLI(t, clientHome, "", "read", "alpha:notes/img.png", "-o", localImg); err != nil {
		t.Fatalf("read binary -o: %v", err)
	}
	gotImg, err := os.ReadFile(localImg)
	if err != nil || !bytes.Equal(gotImg, pngBuf.Bytes()) {
		t.Fatalf("downloaded png mismatch: %d vs %d, err=%v", len(gotImg), pngBuf.Len(), err)
	}

	// cp: machine-to-machine, unjoined local->remote, unjoined remote->local, and joined local->remote via transfer.
	out, _, err = runCLI(t, clientHome, "", "cp", "alpha:notes/doc.txt", "beta:inbox/doc.txt")
	if err != nil || !strings.Contains(out, "sha256") {
		t.Fatalf("cp remote->remote = %q, %v", out, err)
	}
	localSrc := filepath.Join(t.TempDir(), "up.txt")
	_ = os.WriteFile(localSrc, []byte("from-unjoined"), 0o600)
	if _, _, err = runCLI(t, clientHome, "", "cp", localSrc, "beta:inbox/up.txt"); err != nil {
		t.Fatalf("cp local->remote (unjoined): %v", err)
	}
	localDst := filepath.Join(t.TempDir(), "down.txt")
	if _, _, err = runCLI(t, clientHome, "", "cp", "beta:inbox/up.txt", localDst); err != nil {
		t.Fatalf("cp remote->local (unjoined): %v", err)
	}
	if raw, _ := os.ReadFile(localDst); string(raw) != "from-unjoined" {
		t.Fatalf("cp remote->local content = %q", raw)
	}

	// Connect on joined machine alpha: auto-detects self="alpha" and streams local->beta via /transfer.
	out, _, err = runCLI(t, alphaLanesh, "", "connect", ts.URL+"/lanes/"+lane.ID, "--key", ak.Token)
	if err != nil || !strings.Contains(out, `as "alpha"`) {
		t.Fatalf("joined connect = %q, %v", out, err)
	}
	alphaLocal := filepath.Join(alphaHome, "notes", "doc.txt")
	out, _, err = runCLI(t, alphaLanesh, "", "cp", alphaLocal, "beta:inbox/via-self.txt")
	if err != nil || !strings.Contains(out, "sha256") {
		t.Fatalf("joined cp local->remote = %q, %v", out, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(betaHome, "inbox", "via-self.txt")); string(raw) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("joined cp content = %q", raw)
	}

	// Projects via CLI.
	out, _, err = runCLI(t, clientHome, "", "project", "update", "atlas", "--create", "--new", "Atlas context", "--add-location", "alpha:notes")
	if err != nil || !strings.Contains(out, "revision 1") {
		t.Fatalf("project create = %q, %v", out, err)
	}
	out, _, err = runCLI(t, clientHome, "", "project", "list")
	if err != nil || !strings.Contains(out, "atlas") {
		t.Fatalf("project list = %q, %v", out, err)
	}
	out, _, err = runCLI(t, clientHome, "", "project", "get", "atlas")
	if err != nil || !strings.Contains(out, "Atlas context") || !strings.Contains(out, "alpha:notes") {
		t.Fatalf("project get = %q, %v", out, err)
	}

	// Disconnect removes client.toml.
	if _, _, err = runCLI(t, clientHome, "", "disconnect"); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if _, _, err = runCLI(t, clientHome, "", "machines"); err == nil {
		t.Fatal("machines after disconnect must fail")
	}
}
