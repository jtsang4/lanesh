package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jtsang4/lanesh/internal/mcp"
	"github.com/jtsang4/lanesh/internal/protocol"
)

func (h *harness) consumer(method, path string, token string, body, out any, want int) {
	h.t.Helper()
	var r bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&r).Encode(body); err != nil {
			h.t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(h.ctx, method, h.http.URL+"/lanes/"+h.laneID+"/v1"+path, &r)
	if err != nil {
		h.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			h.t.Fatal(err)
		}
	}
}

func TestConsumerAPI(t *testing.T) {
	h := newHarness(t)
	a := h.startMachine("alpha", true, "deploy")
	b := h.startMachine("beta", true)
	h.startMachine("gamma", false, "deploy")

	// Auth checks: missing, wrong, and join key are all rejected.
	h.consumer(http.MethodGet, "/machines", "", nil, nil, http.StatusUnauthorized)
	h.consumer(http.MethodGet, "/machines", "lsk_wrong", nil, nil, http.StatusUnauthorized)
	h.consumer(http.MethodGet, "/machines", h.join, nil, nil, http.StatusUnauthorized)

	// Machines & describe.
	var ms []MachineView
	h.consumer(http.MethodGet, "/machines", h.access, nil, &ms, http.StatusOK)
	if len(ms) != 3 {
		t.Fatalf("machines = %d, want 3", len(ms))
	}
	var desc protocol.DescribeResult
	h.consumer(http.MethodGet, "/machines/alpha/describe", h.access, nil, &desc, http.StatusOK)
	if len(desc.Skills.Skills) != 1 || desc.Skills.Skills[0].Name != "deploy" {
		t.Fatalf("describe skills = %+v", desc.Skills.Skills)
	}

	// Skills list / read / grep.
	var skList []map[string]any
	h.consumer(http.MethodGet, "/skills?q=deploy", h.access, nil, &skList, http.StatusOK)
	if len(skList) != 2 {
		t.Fatalf("skills list = %+v", skList)
	}
	var skRead protocol.SkillReadResult
	h.consumer(http.MethodPost, "/machines/alpha/skills/read", h.access, protocol.SkillReadParams{Skill: "deploy"}, &skRead, http.StatusOK)
	if !strings.Contains(skRead.Content, "# deploy") {
		t.Fatalf("skill read = %q", skRead.Content)
	}
	var skGrep protocol.SkillGrepResult
	h.consumer(http.MethodPost, "/machines/alpha/skills/grep", h.access, protocol.SkillGrepParams{Skill: "deploy", Pattern: "payload"}, &skGrep, http.StatusOK)
	if len(skGrep.Matches) != 1 {
		t.Fatalf("skill grep = %+v", skGrep)
	}

	// Policy enforcement: gamma disallows exec and files.
	h.consumer(http.MethodPost, "/machines/gamma/exec", h.access, protocol.ExecParams{Command: "echo hi"}, nil, http.StatusForbidden)
	h.consumer(http.MethodPost, "/machines/gamma/files/list", h.access, protocol.FileListParams{}, nil, http.StatusForbidden)

	// Exec & job lifecycle.
	var st protocol.JobStatus
	h.consumer(http.MethodPost, "/machines/alpha/exec", h.access, protocol.ExecParams{
		Command: shellCmd("echo hi-consumer", "Write-Output hi-consumer"),
	}, &st, http.StatusOK)
	if !strings.Contains(st.Stdout, "hi-consumer") {
		t.Fatalf("exec stdout = %q", st.Stdout)
	}

	var bg protocol.JobStatus
	h.consumer(http.MethodPost, "/machines/alpha/exec", h.access, protocol.ExecParams{
		Command:    shellCmd("cat", "$input | ForEach-Object { $_ }"),
		Background: true,
	}, &bg, http.StatusOK)
	var jlist protocol.JobListResult
	h.consumer(http.MethodGet, "/machines/alpha/jobs", h.access, nil, &jlist, http.StatusOK)
	if len(jlist.Jobs) < 2 {
		t.Fatalf("jobs = %+v", jlist.Jobs)
	}
	h.consumer(http.MethodPost, "/machines/alpha/jobs/"+bg.JobID+"/write", h.access, protocol.JobWriteParams{Data: "ping\n", CloseStdin: true}, nil, http.StatusNoContent)
	var jr protocol.JobReadResult
	h.consumer(http.MethodPost, "/machines/alpha/jobs/"+bg.JobID+"/read", h.access, protocol.JobReadParams{
		Stream: "both", WaitMS: 10000, WaitUntil: "exit",
	}, &jr, http.StatusOK)
	if !strings.Contains(jr.Data, "ping") || !jr.EOF {
		t.Fatalf("job read = %+v", jr)
	}

	// Files: write, read (lines), edit, list, grep, transfer.
	var fw protocol.FileWriteResult
	h.consumer(http.MethodPost, "/machines/alpha/files/write", h.access, protocol.FileWriteParams{
		Path: "work/note.txt", Content: "one\ntwo\nthree\n", CreateDirs: true,
	}, &fw, http.StatusOK)
	var fr protocol.FileReadResult
	h.consumer(http.MethodPost, "/machines/alpha/files/read", h.access, protocol.FileReadParams{
		Path: "work/note.txt", StartLine: 2, EndLine: 3,
	}, &fr, http.StatusOK)
	if fr.Content != "two\nthree\n" || fr.TotalLines != 3 {
		t.Fatalf("file read = %+v", fr)
	}
	var fe protocol.FileEditResult
	h.consumer(http.MethodPost, "/machines/alpha/files/edit", h.access, protocol.FileEditParams{
		Path:  "work/note.txt",
		Edits: []protocol.FileEditOp{{OldText: "two", NewText: "TWO"}},
	}, &fe, http.StatusOK)
	if !strings.Contains(fe.Diff, "+TWO") {
		t.Fatalf("file edit = %+v", fe)
	}
	var fl protocol.FileListResult
	h.consumer(http.MethodPost, "/machines/alpha/files/list", h.access, protocol.FileListParams{Path: "work"}, &fl, http.StatusOK)
	if len(fl.Entries) != 1 || fl.Entries[0].Path != "note.txt" {
		t.Fatalf("file list = %+v", fl)
	}
	var fg protocol.FileGrepResult
	h.consumer(http.MethodPost, "/machines/alpha/files/grep", h.access, protocol.FileGrepParams{Path: "work", Pattern: "TWO"}, &fg, http.StatusOK)
	if len(fg.Matches) != 1 {
		t.Fatalf("file grep = %+v", fg)
	}
	var tr protocol.TransferResult
	h.consumer(http.MethodPost, "/transfer", h.access, map[string]any{
		"from_machine": "alpha", "from_path": "work/note.txt",
		"to_machine": "beta", "to_path": "got/note.txt",
	}, &tr, http.StatusOK)
	if tr.Files != 1 || tr.SHA256 == "" {
		t.Fatalf("transfer = %+v", tr)
	}
	raw, err := os.ReadFile(filepath.Join(b.home, "got", "note.txt"))
	if err != nil || string(raw) != "one\nTWO\nthree\n" {
		t.Fatalf("transferred = %q, %v", raw, err)
	}

	// Projects: create, list, get, update.
	var pv ProjectView
	h.consumer(http.MethodPost, "/projects/update", h.access, mcp.UpdateProjectInput{
		Project:      "demo",
		Create:       true,
		Context:      "Line A\nLine B\n",
		AddLocations: []mcp.LocationInput{{Machine: "alpha", Path: "work"}},
	}, &pv, http.StatusCreated)
	if pv.Name != "demo" || pv.Revision != 1 || len(pv.Locations) != 1 || pv.Locations[0].Info == nil || !pv.Locations[0].Info.Dir {
		t.Fatalf("created project = %+v", pv)
	}
	var pList []ProjectSummary
	h.consumer(http.MethodGet, "/projects", h.access, nil, &pList, http.StatusOK)
	if len(pList) != 1 || pList[0].Name != "demo" {
		t.Fatalf("list projects = %+v", pList)
	}
	h.consumer(http.MethodGet, "/projects/demo", h.access, nil, &pv, http.StatusOK)
	if pv.Revision != 1 {
		t.Fatalf("get project = %+v", pv)
	}
	h.consumer(http.MethodPost, "/projects/update", h.access, mcp.UpdateProjectInput{
		Project: "demo", OldText: "Line B", NewText: "Line B updated",
	}, &pv, http.StatusOK)
	if pv.Revision != 2 || !strings.Contains(pv.Context, "Line B updated") {
		t.Fatalf("updated project = %+v", pv)
	}

	// Audit log recorded consumer actions.
	var audit []map[string]any
	h.api(http.MethodGet, "/api/v1/lanes/"+h.laneID+"/audit?action=file.edit", nil, &audit, http.StatusOK)
	if len(audit) < 1 {
		t.Fatalf("audit missing file.edit: %v", audit)
	}
	_ = a
}
