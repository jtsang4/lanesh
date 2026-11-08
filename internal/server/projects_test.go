package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jtsang4/lanesh/internal/mcp"
	"github.com/jtsang4/lanesh/internal/store"
)

func TestProjects(t *testing.T) {
	h := newHarness(t)
	a := h.startMachine("alpha", true)
	h.startMachine("gamma", false)
	repo := filepath.Join(a.home, "code", "app")
	for path, content := range map[string]string{".git/HEAD": "ref: refs/heads/main\n", "AGENTS.md": "# rules\n"} {
		full := filepath.Join(repo, filepath.FromSlash(path))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := h.mcpClient(h.access)

	out := mustCall(t, s, "get_project", nil)
	mustContain(t, "no projects", out, "no projects yet")

	out = mustCall(t, s, "update_project", map[string]any{
		"project": "App", "create": true,
		"context":       "# App\nThe web app for customers.\n\n## Rules\n- New work in a git worktree.\n\n## Status\nLogin page in progress.",
		"add_locations": []map[string]string{{"machine": "alpha", "path": "~/code/app"}, {"machine": "gamma", "path": "/srv/app"}},
	})
	mustContain(t, "create", out, `created project "App" (revision 1)`)
	if _, errText := call(t, s, "update_project", map[string]any{"project": "app", "create": true}); !strings.Contains(errText, "already exists") {
		t.Fatalf("duplicate create: %q", errText)
	}

	out = mustCall(t, s, "get_project", map[string]any{"project": "app"})
	mustContain(t, "get", out,
		`[lanesh] project "App" (revision 1, updated `, "by a)",
		"- alpha ~/code/app: git, branch main; has AGENTS.md",
		"read it before working there",
		"- gamma /srv/app: the machine does not allow file access; not checked",
		"[lanesh] begin context\n# App\nThe web app for customers.", "[lanesh] end context", "revision 1")

	// Edits: replace a unique snippet, append, whole replacement with a revision.
	mustContain(t, "edit", mustCall(t, s, "update_project", map[string]any{
		"project": "App", "old_text": "Login page in progress.", "new_text": "Login done; billing next.",
	}), "revision 2: context edited")
	mustContain(t, "append", mustCall(t, s, "update_project", map[string]any{
		"project": "App", "new_text": "Staging: https://staging.example.com",
	}), "revision 3: context appended")
	if _, errText := call(t, s, "update_project", map[string]any{"project": "App", "old_text": "nothing like this", "new_text": "x"}); !strings.Contains(errText, "occurs 0 times") {
		t.Fatalf("missing old_text: %q", errText)
	}
	if _, errText := call(t, s, "update_project", map[string]any{"project": "App", "context": "# App"}); !strings.Contains(errText, "needs the revision") {
		t.Fatalf("whole context without revision: %q", errText)
	}
	if _, errText := call(t, s, "update_project", map[string]any{"project": "App", "context": "# App", "revision": 1}); !strings.Contains(errText, "changed since that revision") {
		t.Fatalf("stale revision: %q", errText)
	}
	mustContain(t, "remove location", mustCall(t, s, "update_project", map[string]any{
		"project": "App", "remove_locations": []map[string]string{{"machine": "gamma", "path": "/srv/app"}},
	}), "revision 4: 1 location removed")
	if _, errText := call(t, s, "get_project", map[string]any{"project": "nope"}); !strings.Contains(errText, `no project "nope" (projects: App)`) {
		t.Fatalf("unknown project: %q", errText)
	}

	// Instructions name the projects; listing them shows their summaries.
	s2 := h.mcpClient(h.access)
	if instr := s2.InitializeResult().Instructions; !strings.HasSuffix(instr, "\n\nProjects: App") {
		t.Fatalf("instructions: %s", instr)
	}
	mustContain(t, "list", mustCall(t, s2, "get_project", nil), "- App: The web app for customers.")

	// Admin API: the same project, a conflict on a stale base, history.
	base := "/api/v1/lanes/" + h.laneID + "/projects"
	var view struct {
		store.Project
		Locations     []mcp.LocationView `json:"locations"`
		UpdatedByName string             `json:"updated_by_name"`
	}
	h.api(http.MethodGet, base+"/app", nil, &view, http.StatusOK)
	if view.Revision != 4 || len(view.Locations) != 1 || view.Locations[0].Info == nil || view.Locations[0].Info.Branch != "main" || view.UpdatedByName != "a" {
		t.Fatalf("admin view: %+v", view)
	}
	h.api(http.MethodPatch, base+"/"+view.ID, map[string]any{"context": "x", "base_revision": 3}, nil, http.StatusConflict)
	h.api(http.MethodPatch, base+"/"+view.ID, map[string]any{"name": "Shop", "base_revision": 4}, &view, http.StatusOK)
	h.api(http.MethodPatch, base+"/"+view.ID, map[string]any{"locations": []map[string]string{{"machine_id": "mch_nope", "path": "/x"}}}, nil, http.StatusBadRequest)
	var revs []map[string]any
	h.api(http.MethodGet, base+"/shop/revisions", nil, &revs, http.StatusOK)
	if len(revs) != 5 || revs[0]["name"] != "Shop" || revs[4]["updated_by_name"] != "a" {
		t.Fatalf("revisions: %v", revs)
	}
	var list []ProjectSummary
	h.api(http.MethodGet, base, nil, &list, http.StatusOK)
	if len(list) != 1 || list[0].Summary != "The web app for customers." || list[0].Locations[0].MachineName != "alpha" {
		t.Fatalf("list: %+v", list)
	}
	var audit []map[string]any
	h.api(http.MethodGet, "/api/v1/lanes/"+h.laneID+"/audit?action=project.update&limit=1", nil, &audit, http.StatusOK)
	if len(audit) != 1 || audit[0]["actor"] != "admin" || audit[0]["detail"].(map[string]any)["changes"] != "renamed" {
		t.Fatalf("audit: %v", audit)
	}
	h.api(http.MethodGet, "/api/v1/lanes/"+h.laneID+"/audit?action=project.create", nil, &audit, http.StatusOK)
	// The refused duplicate is recorded too, with the name it asked for.
	if len(audit) != 2 || audit[1]["actor_name"] != "a" || audit[0]["detail"].(map[string]any)["name"] != "app" ||
		!strings.HasPrefix(audit[0]["result"].(string), "error") {
		t.Fatalf("create audit: %v", audit)
	}
	h.api(http.MethodDelete, base+"/shop", nil, nil, http.StatusNoContent)
	h.api(http.MethodGet, base+"/shop", nil, nil, http.StatusNotFound)
}
