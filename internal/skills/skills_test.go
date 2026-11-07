package skills

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jtsang4/lanesh/internal/protocol"
)

func writeSkill(t *testing.T, dir, name, frontmatter string, extra map[string]string) string {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, SkillFile), []byte(frontmatter+"\n# Body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for p, c := range extra {
		full := filepath.Join(d, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestScanDedupesWithAgentsPriority(t *testing.T) {
	home := t.TempDir()
	srcs := DefaultSources(home)
	writeSkill(t, srcs[0].Dir, "deploy", "---\nname: deploy\ndescription: |\n  Deploy the\n  app\n---", nil)
	writeSkill(t, srcs[1].Dir, "deploy", "---\nname: deploy\ndescription: old\n---", nil)
	writeSkill(t, srcs[1].Dir, "review", "---\ndescription: Review code\n---", nil)
	writeSkill(t, srcs[1].Dir, "not-a-skill", "", nil)
	_ = os.Remove(filepath.Join(srcs[1].Dir, "not-a-skill", SkillFile))

	idx := Scan(srcs)
	if len(idx.Skills) != 2 {
		t.Fatalf("skills = %+v", idx.Skills)
	}
	d := idx.Skills[0]
	if d.Name != "deploy" || d.Source != "agents" || d.Description != "Deploy the app" {
		t.Fatalf("deploy = %+v", d)
	}
	if idx.Skills[1].Name != "review" || idx.Skills[1].Description != "Review code" {
		t.Fatalf("review = %+v", idx.Skills[1])
	}
	if len(idx.Shadowed) != 1 || idx.Shadowed[0].Source != "claude" {
		t.Fatalf("shadowed = %+v", idx.Shadowed)
	}
	if idx.Hash == "" || Scan(srcs).Hash != idx.Hash {
		t.Fatal("index hash must be stable")
	}
}

func TestScanFollowsSymlinksWithoutShadowing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	home := t.TempDir()
	srcs := DefaultSources(home)
	resolved := writeSkill(t, srcs[0].Dir, "pdf", "---\nname: pdf\ndescription: PDFs\n---", nil)
	_ = os.MkdirAll(srcs[1].Dir, 0o755)
	if err := os.Symlink(resolved, filepath.Join(srcs[1].Dir, "pdf")); err != nil {
		t.Fatal(err)
	}
	idx := Scan(srcs)
	if len(idx.Skills) != 1 || len(idx.Shadowed) != 0 {
		t.Fatalf("idx = %+v", idx)
	}
}

func TestHashChangesWithContent(t *testing.T) {
	home := t.TempDir()
	srcs := DefaultSources(home)
	d := writeSkill(t, srcs[0].Dir, "a", "---\nname: a\n---", map[string]string{"scripts/run.sh": "echo 1"})
	before := Scan(srcs)
	_ = os.WriteFile(filepath.Join(d, "scripts", "run.sh"), []byte("echo 2"), 0o600)
	after := Scan(srcs)
	if before.Skills[0].Hash == after.Skills[0].Hash || before.Hash == after.Hash {
		t.Fatal("hash must change when a file changes")
	}
}

func TestRead(t *testing.T) {
	home := t.TempDir()
	srcs := DefaultSources(home)
	writeSkill(t, srcs[0].Dir, "a", "---\nname: a\n---", map[string]string{
		"scripts/run.sh": "echo hi",
		"bin.dat":        "\x00\x01\x02",
	})
	idx := Scan(srcs)

	res, err := Read(idx, protocol.SkillReadParams{Skill: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "# Body") || res.Path != SkillFile || len(res.Files) != 4 {
		t.Fatalf("res = %+v", res)
	}

	res, err = Read(idx, protocol.SkillReadParams{Skill: "a", Path: "scripts/run.sh"})
	if err != nil || res.Content != "echo hi" || res.Encoding != protocol.EncodingUTF8 || res.Files != nil {
		t.Fatalf("res = %+v err = %v", res, err)
	}

	res, err = Read(idx, protocol.SkillReadParams{Skill: "a", Path: "bin.dat"})
	if err != nil || res.Encoding != protocol.EncodingBase64 {
		t.Fatalf("res = %+v err = %v", res, err)
	}

	res, err = Read(idx, protocol.SkillReadParams{Skill: "a", Path: "scripts/run.sh", Offset: 2, Limit: 3})
	if err != nil || res.Content != "ho " || !res.Truncated {
		t.Fatalf("partial res = %+v err = %v", res, err)
	}

	for _, bad := range []string{"../a/SKILL.md", "/etc/passwd", "scripts/../../x"} {
		if _, err := Read(idx, protocol.SkillReadParams{Skill: "a", Path: bad}); err == nil {
			t.Errorf("path %q must be rejected", bad)
		}
	}
	if _, err := Read(idx, protocol.SkillReadParams{Skill: "missing"}); err == nil {
		t.Error("missing skill must fail")
	}
}

func TestWatchReportsChanges(t *testing.T) {
	home := t.TempDir()
	srcs := DefaultSources(home)
	_ = os.MkdirAll(srcs[0].Dir, 0o755)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan protocol.SkillIndex, 4)
	go Watch(ctx, srcs, Scan(srcs), 200*time.Millisecond, func(idx protocol.SkillIndex) { got <- idx })

	writeSkill(t, srcs[0].Dir, "new", "---\nname: new\n---", nil)
	select {
	case idx := <-got:
		if len(idx.Skills) != 1 || idx.Skills[0].Name != "new" {
			t.Fatalf("idx = %+v", idx)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no change reported")
	}
}
