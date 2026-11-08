package skills

import (
	"strings"
	"testing"

	"github.com/jtsang4/lanesh/internal/protocol"
)

func TestGrep(t *testing.T) {
	home := t.TempDir()
	srcs := DefaultSources(home)
	writeSkill(t, srcs[0].Dir, "g", "---\nname: g\n---", map[string]string{
		"references/api.md":   "Intro\nCall the API with --token\nnothing here\n",
		"references/guide.md": "Use the api wisely\n",
		"scripts/run.sh":      "echo token\n",
		"bin.dat":             "token\x00binary",
		"node_modules/x.js":   "token",
		"long.txt":            strings.Repeat("x", 300) + " token",
	})
	idx := Scan(srcs)

	res, err := Grep(idx, protocol.SkillGrepParams{Skill: "g", Pattern: "token", IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]int{}
	for _, m := range res.Matches {
		paths[m.Path] = m.Line
	}
	if len(res.Matches) != 3 || paths["references/api.md"] != 2 || paths["scripts/run.sh"] != 1 || paths["long.txt"] != 1 {
		t.Fatalf("matches = %+v", res.Matches)
	}
	for _, m := range res.Matches {
		if m.Path == "long.txt" && (!strings.HasSuffix(m.Text, "…") || len([]rune(m.Text)) != maxGrepLineRunes+1) {
			t.Fatalf("long lines must be clipped: %q", m.Text)
		}
	}
	if res.FilesMatched != 3 {
		t.Fatalf("files matched = %d", res.FilesMatched)
	}

	// Case, path scope, regex and limits.
	if res, _ := Grep(idx, protocol.SkillGrepParams{Skill: "g", Pattern: "API", Path: "references/"}); len(res.Matches) != 1 {
		t.Fatalf("case-sensitive scoped: %+v", res.Matches)
	}
	if res, _ := Grep(idx, protocol.SkillGrepParams{Skill: "g", Pattern: `^(Intro|Use)\b`, Regex: true}); len(res.Matches) != 2 {
		t.Fatalf("regex: %+v", res.Matches)
	}
	if res, _ := Grep(idx, protocol.SkillGrepParams{Skill: "g", Pattern: "e", MaxMatches: 2}); len(res.Matches) != 2 || !res.Truncated {
		t.Fatalf("limit: %d truncated=%v", len(res.Matches), res.Truncated)
	}
	if _, err := Grep(idx, protocol.SkillGrepParams{Skill: "g", Pattern: "x", Path: "../.."}); err == nil {
		t.Fatal("paths outside the skill must be rejected")
	}
	if _, err := Grep(idx, protocol.SkillGrepParams{Skill: "g", Pattern: "(", Regex: true}); err == nil {
		t.Fatal("invalid regex must be an error")
	}
}

func TestReadDirectory(t *testing.T) {
	home := t.TempDir()
	srcs := DefaultSources(home)
	writeSkill(t, srcs[0].Dir, "d", "---\nname: d\n---", map[string]string{
		"references/a.md":     "a",
		"references/sub/b.md": "b",
		"scripts/run.sh":      "x",
	})
	idx := Scan(srcs)
	for _, p := range []string{"references", "references/"} {
		res, err := Read(idx, protocol.SkillReadParams{Skill: "d", Path: p})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsDir || res.Path != "references" || len(res.Files) != 3 {
			t.Fatalf("%s: %+v", p, res)
		}
		for _, f := range res.Files {
			if !strings.HasPrefix(f.Path, "references/") {
				t.Fatalf("paths must stay relative to the skill: %s", f.Path)
			}
		}
	}
	root, err := Read(idx, protocol.SkillReadParams{Skill: "d", Path: "/"})
	if err != nil || !root.IsDir || len(root.Files) < 6 {
		t.Fatalf("root listing: %+v %v", root, err)
	}
}
