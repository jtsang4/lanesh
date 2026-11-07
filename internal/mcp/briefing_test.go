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
	})
	for _, want := range []string{"- build-box: linux/amd64, bash, 3 skills", "- nas: linux/arm64, 1 skills, no exec, offline"} {
		if !strings.Contains(few, want) {
			t.Errorf("missing %q in:\n%s", want, few)
		}
	}

	var many []Machine
	for i := range 200 {
		many = append(many, Machine{Name: fmt.Sprintf("machine-with-a-long-name-%03d", i), OS: "linux", Arch: "amd64", Shell: "zsh", Online: true, Policy: protocol.Policy{AllowExec: true}})
	}
	got := Instructions(many)
	if len(got) > 2048 || !strings.Contains(got, "more (see list_machines)") {
		t.Fatalf("instructions for 200 machines: %d characters\n%s", len(got), got[len(got)-200:])
	}
	if Instructions(nil) == "" || !strings.Contains(Instructions(nil), "no machines yet") {
		t.Fatal("empty lane instructions")
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
	text := renderBriefing(Machine{Name: "mac"}, d, "mac:abcdefgh", "no briefing token was given", "transfer", "to_briefing")
	for _, want := range []string{`Nothing was run on "mac"`, `repeat the same transfer call with to_briefing "mac:abcdefgh"`, "do not call describe_machine", "Shell: zsh (available: bash, zsh)", "- deploy: Deploy things", "pdf (claude)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}

	a, b := &BriefingRequired{Text: "A"}, &BriefingRequired{Text: "B"}
	var br *BriefingRequired
	if err := joinBriefings(a, nil, b); !errors.As(err, &br) || br.Text != "A\n\n---\n\nB" {
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
