package mcp

import (
	"fmt"
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

func TestRenderTreeCollapses(t *testing.T) {
	entries := []protocol.FileEntry{
		{Path: "SKILL.md", Size: 100},
		{Path: "scripts", Dir: true},
		{Path: "scripts/run.sh", Size: 10},
		{Path: "references", Dir: true},
	}
	for i := range 40 {
		entries = append(entries, protocol.FileEntry{Path: fmt.Sprintf("references/doc-%02d.md", i), Size: 1024})
	}
	lines, collapsed := renderTree("", entries)
	want := []string{
		"SKILL.md (100 B)",
		`references/ (40 files, 40.0 KB; list with path="references/")`,
		"scripts/",
		"  run.sh (10 B)",
	}
	if collapsed != 1 || !reflect.DeepEqual(lines, want) {
		t.Fatalf("collapsed=%d\n%s", collapsed, strings.Join(lines, "\n"))
	}

	// Listing a directory shows its own files, relative to it.
	lines, collapsed = renderTree("references", entries)
	if collapsed != 0 || len(lines) != 40 || lines[0] != "doc-00.md (1.0 KB)" {
		t.Fatalf("subdirectory: %d %v", collapsed, lines[:3])
	}

	// Too many lines: every subdirectory collapses.
	var wide []protocol.FileEntry
	for d := range 20 {
		for f := range 10 {
			wide = append(wide, protocol.FileEntry{Path: fmt.Sprintf("d%02d/f%d", d, f), Size: 1})
		}
	}
	lines, collapsed = renderTree("", wide)
	if len(lines) != 20 || collapsed != 20 {
		t.Fatalf("wide tree: %d lines, %d collapsed", len(lines), collapsed)
	}
}

func TestWriteMachineBudget(t *testing.T) {
	var skills []protocol.Skill
	for i := range 400 {
		skills = append(skills, protocol.Skill{Name: fmt.Sprintf("skill-%03d", i), Description: strings.Repeat("word ", 80), Source: "agents", Path: "/h/.agents/skills/x"})
	}
	var out text
	writeMachine(&out, Machine{Name: "big"}, protocol.DescribeResult{Skills: protocol.SkillIndex{Skills: skills}})
	s := out.String()
	if out.tokens() > skillsBudget+2000 {
		t.Fatalf("briefing is %d tokens", out.tokens())
	}
	if !strings.Contains(s, "Skills (400) in /h/.agents/skills (agents):") || !strings.Contains(s, "more skills") {
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
