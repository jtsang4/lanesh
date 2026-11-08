package files

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInspect(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "code", "app")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(repo, "AGENTS.md"), "# rules")
	write(t, filepath.Join(repo, "web", "package.json"), "{}")
	linked := os.Symlink("AGENTS.md", filepath.Join(repo, "CLAUDE.md")) == nil

	got := Inspect("~/code/app", home)
	want := []string{"AGENTS.md"}
	if linked {
		want = append(want, "CLAUDE.md -> AGENTS.md")
	}
	if !got.Exists || !got.Dir || !got.Git || got.Branch != "main" || got.Worktree || !reflect.DeepEqual(got.AgentFiles, want) {
		t.Fatalf("repo: %+v", got)
	}
	if got.Resolved != repo {
		t.Fatalf("resolved = %q", got.Resolved)
	}

	// A subdirectory of a repository is in it, without its agent files.
	sub := Inspect(filepath.Join(repo, "web"), home)
	if !sub.Git || sub.Branch != "main" || len(sub.AgentFiles) != 0 {
		t.Fatalf("subdirectory: %+v", sub)
	}

	// A linked worktree: .git is a file pointing into the main repository.
	wt := filepath.Join(home, "code", "app-feature")
	gitDir := filepath.Join(repo, ".git", "worktrees", "app-feature")
	write(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feature/login\n")
	write(t, filepath.Join(gitDir, "commondir"), "../..\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+gitDir+"\n")
	write(t, filepath.Join(wt, ".claude", "CLAUDE.md"), "x")
	got = Inspect(wt, home)
	if !got.Git || !got.Worktree || got.Branch != "feature/login" || !reflect.DeepEqual(got.AgentFiles, []string{".claude/CLAUDE.md"}) {
		t.Fatalf("worktree: %+v", got)
	}

	// A submodule: .git is a file too, but its Git directory has no commondir.
	sm := filepath.Join(repo, "vendor", "lib")
	smDir := filepath.Join(repo, ".git", "modules", "lib")
	write(t, filepath.Join(smDir, "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(sm, ".git"), "gitdir: ../../.git/modules/lib\n")
	if got := Inspect(sm, home); !got.Git || got.Worktree || got.Branch != "main" {
		t.Fatalf("submodule: %+v", got)
	}

	// Reftable repositories keep a stub HEAD; the branch is left unknown.
	write(t, filepath.Join(home, "rt", ".git", "HEAD"), "ref: refs/heads/.invalid\n")
	if got := Inspect("rt", home); !got.Git || got.Branch != "" {
		t.Fatalf("reftable: %+v", got)
	}

	// Detached HEAD, plain directories and missing paths.
	write(t, filepath.Join(home, "d", ".git", "HEAD"), "0123456789abcdef\n")
	if got := Inspect("d", home); got.Branch != "detached at 0123456" {
		t.Fatalf("detached: %+v", got)
	}
	plain := filepath.Join(home, "notes")
	_ = os.Mkdir(plain, 0o755)
	if got := Inspect(plain, home); !got.Exists || got.Git || got.AgentFiles != nil {
		t.Fatalf("plain: %+v", got)
	}
	if got := Inspect("~/missing", home); got.Exists || got.Error != "" {
		t.Fatalf("missing: %+v", got)
	}
	if got := Inspect(" ", home); got.Error == "" {
		t.Fatalf("empty path must be an error: %+v", got)
	}
}
