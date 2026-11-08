package mcp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jtsang4/lanesh/internal/protocol"
)

func TestInstructionsFitClientLimits(t *testing.T) {
	if len(instructionsHead) > 512 {
		t.Fatalf("usage head is %d characters; clients may only show the first 512", len(instructionsHead))
	}
	few := Instructions([]Machine{
		{Name: "build-box", OS: "linux", Arch: "amd64", Shell: "bash", SkillCount: 3, Online: true, Policy: protocol.Policy{AllowExec: true}},
		{Name: "nas", OS: "linux", Arch: "arm64", SkillCount: 1, Policy: protocol.Policy{}},
	}, []string{"storefront", "博客"})
	for _, want := range []string{"- build-box: linux/amd64, bash, 3 skills", "- nas: linux/arm64, 1 skill, no exec, offline", "\n\nProjects: storefront, 博客"} {
		if !strings.Contains(few, want) {
			t.Errorf("missing %q in:\n%s", want, few)
		}
	}

	var many []Machine
	for i := range 200 {
		many = append(many, Machine{Name: fmt.Sprintf("machine-with-a-long-name-%03d", i), OS: "linux", Arch: "amd64", Shell: "zsh", Online: true, Policy: protocol.Policy{AllowExec: true}})
	}
	var projects []string
	for i := range 100 {
		projects = append(projects, fmt.Sprintf("project-%03d", i))
	}
	got := Instructions(many, projects)
	if len(got) > maxInstructions || !strings.Contains(got, "more (see list_machines)") ||
		!strings.Contains(got, "Projects: project-000, ") || !strings.HasSuffix(got, " more") {
		t.Fatalf("instructions for 200 machines: %d characters\n%s", len(got), got[len(got)-200:])
	}
	if empty := Instructions(nil, nil); !strings.Contains(empty, "no machines yet") || strings.Contains(empty, "Projects") {
		t.Fatal("empty lane instructions")
	}
}

func TestDiffSkills(t *testing.T) {
	old := protocol.SkillIndex{Skills: []protocol.Skill{
		{Name: "keep", Hash: "1", Description: "same"},
		{Name: "content", Hash: "2", Description: "same"},
		{Name: "described", Hash: "3", Description: "old words"},
		{Name: "gone", Hash: "4"},
	}}
	cur := protocol.SkillIndex{Skills: []protocol.Skill{
		{Name: "keep", Hash: "1", Description: "same"},
		{Name: "content", Hash: "2b", Description: "same"},
		{Name: "described", Hash: "3", Description: "new words"},
		{Name: "fresh", Hash: "5"},
	}}
	got := renderChanges(diffSkills(old, cur))
	want := "Changes since your last briefing:\n+ fresh (new)" +
		"\n~ content (updated; if you read it before, read it again before relying on it)" +
		"\n~ described (updated; if you read it before, read it again before relying on it)" +
		"\n- gone (removed)"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if renderChanges(diffSkills(old, old)) != "" {
		t.Fatal("identical versions must render no changes")
	}
}

func TestRenderBriefingAndJoin(t *testing.T) {
	d := protocol.DescribeResult{
		Environment: protocol.Environment{OS: "darwin", Arch: "arm64", User: "u", Home: "/Users/u", DefaultShell: "zsh", Shells: []string{"bash", "zsh"}},
		Policy:      protocol.Policy{ExposeSkills: true, AllowExec: true},
		Skills: protocol.SkillIndex{
			Skills:   []protocol.Skill{{Name: "deploy", Description: "Deploy things"}},
			Shadowed: []protocol.ShadowedSkill{{Name: "pdf", Source: "claude"}},
		},
	}
	text := renderBriefing(Machine{Name: "mac"}, d, "mac:abcdefgh", "no briefing token was given", "transfer", "to_briefing", "")
	if strings.Contains(text, "Changes since") {
		t.Fatalf("a briefing without changes must not mention them:\n%s", text)
	}
	for _, want := range []string{`[lanesh] nothing was run on "mac"`, `repeat the same transfer call with to_briefing "mac:abcdefgh"`, "no describe_machine needed", "Shell: zsh (also bash)", "- deploy: Deploy things", "pdf (claude)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}

	if !strings.HasSuffix(text, "\n"+EndMarker) {
		t.Fatalf("briefing must end with the end marker:\n%s", text)
	}

	a, b := &BriefingRequired{Text: "A\n" + EndMarker}, &BriefingRequired{Text: "B\n" + EndMarker}
	var br *BriefingRequired
	if err := joinBriefings(a, nil, b); !errors.As(err, &br) || br.Text != "A\nB\n"+EndMarker {
		t.Fatalf("joined = %v", err)
	}
	other := errors.New("offline")
	if err := joinBriefings(a, other); !errors.Is(err, other) {
		t.Fatalf("a non-briefing error must win: %v", err)
	}
	if joinBriefings(nil, nil) != nil {
		t.Fatal("no errors must join to nil")
	}
}
