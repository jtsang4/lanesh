package mcp

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jtsang4/lanesh/internal/protocol"
)

func TestQueryTerms(t *testing.T) {
	for q, want := range map[string][]string{
		"PDF form-filling": {"pdf", "form", "filling"},
		"生成图片":             {"生成", "成图", "图片"},
		"用 dreamina 生成视频":  {"用", "dreamina", "生成", "成视", "视频"},
		"  ":               nil,
		"pdf pdf":          {"pdf"},
	} {
		if got := queryTerms(q); !reflect.DeepEqual(got, want) {
			t.Errorf("queryTerms(%q) = %q, want %q", q, got, want)
		}
	}
}

func TestFindSkills(t *testing.T) {
	ms := []Machine{{ID: "a", Name: "mac", Online: true}, {ID: "b", Name: "nas"}}
	idx := map[string]protocol.SkillIndex{
		"a": {Skills: []protocol.Skill{
			{Name: "pdf", Description: "Read and fill PDF forms", Hash: "1"},
			{Name: "pdf-tools", Description: "Merge files", Hash: "2"},
			{Name: "dreamina", Description: "即梦图片和视频生成", Hash: "3"},
			{Name: "notes", Description: "Write notes", Hash: "4"},
		}},
		"b": {Skills: []protocol.Skill{
			{Name: "pdf", Description: "Read and fill PDF forms", Hash: "1"},
			{Name: "notes", Description: "Write notes", Hash: "9"},
		}},
	}
	names := func(ms []*skillMatch) []string {
		var out []string
		for _, m := range ms {
			out = append(out, m.name)
		}
		return out
	}

	got, partial := findSkills(ms, idx, "pdf")
	if partial || !reflect.DeepEqual(names(got), []string{"pdf", "pdf-tools"}) {
		t.Fatalf("pdf: %v partial=%v", names(got), partial)
	}
	if got[0].machines[0] != "mac" || len(got[0].machines) != 2 || !got[0].offline["nas"] || len(got[0].hashes) != 1 {
		t.Fatalf("pdf grouping: %+v", got[0])
	}
	if s := machineList(got[0]); s != "on mac, nas (offline); identical" {
		t.Fatalf("machine list: %q", s)
	}

	got, _ = findSkills(ms, idx, "生成视频")
	if !reflect.DeepEqual(names(got), []string{"dreamina"}) {
		t.Fatalf("CJK: %v", names(got))
	}

	// Most terms missing everywhere: the closest skills, marked partial.
	got, partial = findSkills(ms, idx, "merge spreadsheets into one workbook")
	if !partial || len(got) == 0 || got[0].name != "pdf-tools" {
		t.Fatalf("partial: %v partial=%v", names(got), partial)
	}

	got, _ = findSkills(ms[1:], idx, "")
	if !reflect.DeepEqual(names(got), []string{"notes", "pdf"}) {
		t.Fatalf("machine listing: %v", names(got))
	}

	got, _ = findSkills(ms, idx, "notes")
	if s := machineList(got[0]); !strings.HasSuffix(s, "versions differ") {
		t.Fatalf("differing versions: %q", s)
	}
}

func TestRenderTree(t *testing.T) {
	entries := []protocol.FileEntry{
		{Path: "SKILL.md", Size: 100},
		{Path: "scripts", Dir: true},
		{Path: "scripts/run.sh", Size: 10},
		{Path: "references", Dir: true},
		{Path: "empty", Dir: true},
	}
	for i := range 200 {
		entries = append(entries, protocol.FileEntry{Path: fmt.Sprintf("references/doc-%03d.md", i), Size: 1024})
	}
	// The level below the root does not fit: the small directory opens, the
	// large one is summarized.
	lines, collapsed, omitted := renderTree("", entries, maxTreeLines)
	want := []string{
		"SKILL.md (100 B)",
		"empty/",
		"references/ (collapsed: 200 files, 200.0 KB)",
		"scripts/",
		"  run.sh (10 B)",
	}
	if !reflect.DeepEqual(collapsed, []string{"references/"}) || omitted != 0 || !reflect.DeepEqual(lines, want) {
		t.Fatalf("collapsed=%v omitted=%d\n%s", collapsed, omitted, strings.Join(lines, "\n"))
	}

	// Listing a directory shows its own entries, relative to it, up to the budget.
	lines, _, omitted = renderTree("references", entries, maxTreeLines)
	if len(lines) != maxTreeLines || omitted != 50 || lines[0] != "doc-000.md (1.0 KB)" {
		t.Fatalf("subdirectory: %d lines, %d omitted, first %q", len(lines), omitted, lines[0])
	}

	// Nested directories open level by level while they fit.
	var deep []protocol.FileEntry
	for d := range 50 {
		for f := range 5 {
			deep = append(deep, protocol.FileEntry{Path: fmt.Sprintf("references/d%02d/f%d.md", d, f), Size: 1})
		}
	}
	lines, collapsed, _ = renderTree("", deep, skillTreeLines)
	// 51 lines leave room for one more 5-file directory, and no more.
	if len(lines) != 56 || len(collapsed) != 49 || collapsed[0] != "references/d01/" || lines[1] != "  d00/" || lines[7] != "  d01/ (collapsed: 5 files, 5 B)" {
		t.Fatalf("deep tree: %d lines, %d collapsed\n%s", len(lines), len(collapsed), strings.Join(lines[:8], "\n"))
	}
	lines, collapsed, _ = renderTree("", deep, 400)
	if len(collapsed) != 0 || len(lines) != 301 {
		t.Fatalf("deep tree with room: %d lines, %d collapsed", len(lines), len(collapsed))
	}
}

func TestWriteMachineBudget(t *testing.T) {
	dir := filepath.Join("h", ".agents", "skills")
	var skills []protocol.Skill
	for i := range 400 {
		skills = append(skills, protocol.Skill{Name: fmt.Sprintf("skill-%03d", i), Description: strings.Repeat("word ", 80), Source: "agents", Path: filepath.Join(dir, "x")})
	}
	var out text
	writeMachine(&out, Machine{Name: "big"}, protocol.DescribeResult{Skills: protocol.SkillIndex{Skills: skills}})
	s := out.String()
	if out.tokens() > skillsBudget+2000 {
		t.Fatalf("briefing is %d tokens", out.tokens())
	}
	if !strings.Contains(s, "Skills (400) in "+dir+" (agents):") || !strings.Contains(s, "more skills") {
		t.Fatalf("budget notice missing:\n%s", s[len(s)-400:])
	}
	if !strings.Contains(s, "…") {
		t.Fatal("long descriptions must be shortened")
	}
}

func TestWriteFile(t *testing.T) {
	var out text
	writeFile(&out, "a.md", "hello\n", protocol.EncodingUTF8, 0, 6, false)
	writeFile(&out, "big.md", "abc", protocol.EncodingUTF8, 10, 100, false)
	writeFile(&out, "img.png", "AAAA", protocol.EncodingBase64, 0, 3, false)
	got := out.String()
	want := `[lanesh] begin a.md (6 B)
hello
[lanesh] end a.md
[lanesh] begin big.md (100 B, bytes 10-13)
abc
[lanesh] end big.md
[lanesh] big.md continues: 87 more bytes; read on with offset=13
[lanesh] img.png is binary (3 B); not shown. Copy it with transfer if you need it.
` + EndMarker
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEstimateTokensAndShorten(t *testing.T) {
	if n := estimateTokens("abcdefgh中文"); n != 4 {
		t.Fatalf("estimate = %d", n)
	}
	if s := shorten("abcdef", 4); s != "abc…" {
		t.Fatalf("shorten = %q", s)
	}
	if s := shorten("short", 10); s != "short" {
		t.Fatalf("shorten = %q", s)
	}
}
