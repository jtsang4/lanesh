package mcp

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// BriefingRequired is returned instead of a result when a call lacks a valid
// briefing token. Its text is the machine's briefing, so the agent learns
// about the machine in the same round trip.
type BriefingRequired struct {
	Machine string
	Text    string
}

func (e *BriefingRequired) Error() string { return e.Text }

// joinBriefings combines the errors of a call that touches two machines.
func joinBriefings(errs ...error) error {
	var texts []string
	for _, err := range errs {
		var br *BriefingRequired
		switch {
		case err == nil:
		case errors.As(err, &br):
			texts = append(texts, strings.TrimRight(strings.TrimSuffix(br.Text, EndMarker), "\n")+"\n")
		default:
			return err
		}
	}
	if len(texts) == 0 {
		return nil
	}
	return &BriefingRequired{Text: strings.Join(texts, "") + EndMarker}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// writeMachine writes a machine's environment, permissions and skill list.
// The skill list degrades within skillsBudget: names with short descriptions,
// then names only, then just a count.
func writeMachine(t *text, m Machine, d protocol.DescribeResult) {
	env := d.Environment
	t.line("Machine: %s (%s/%s), user %s, home %s", m.Name, env.OS, env.Arch, env.User, env.Home)
	shell := env.DefaultShell
	if len(env.Shells) > 1 {
		shell += " (also " + strings.Join(without(env.Shells, env.DefaultShell), ", ") + ")"
	}
	t.line("Shell: %s", shell)
	t.line("Allows: shared skills %s; commands and files %s", yesNo(d.Policy.ExposeSkills), yesNo(d.Policy.AllowExec))

	skills := d.Skills.Skills
	if len(skills) == 0 {
		t.line("Skills: none")
		return
	}
	dirs := map[string]string{}
	for _, s := range skills {
		dirs[s.Source] = parentDir(s.Path)
	}
	where := make([]string, 0, len(dirs))
	for src, dir := range dirs {
		where = append(where, dir+" ("+src+")")
	}
	sort.Strings(where)
	t.line("Skills (%d) in %s:", len(skills), strings.Join(where, " and "))

	used := 0
	shown := 0
	for _, s := range skills {
		line := "- " + s.Name + ": " + shorten(s.Description, descMaxRunes)
		cost := estimateTokens(line)
		if used+cost > skillsBudget {
			break
		}
		t.raw(line)
		used += cost
		shown++
	}
	if rest := skills[shown:]; len(rest) > 0 {
		names := make([]string, len(rest))
		for i, s := range rest {
			names[i] = s.Name
		}
		list := strings.Join(names, ", ")
		if used+estimateTokens(list) <= skillsBudget+1500 {
			t.line("[lanesh] %d more skills, names only (to fit the budget): %s", len(rest), list)
		} else {
			t.line("[lanesh] %d more skills not listed (to fit the budget); see them with find_skills machine=%q", len(rest), m.Name)
		}
	}
	if len(d.Skills.Shadowed) > 0 {
		names := make([]string, len(d.Skills.Shadowed))
		for i, s := range d.Skills.Shadowed {
			names[i] = s.Name + " (" + s.Source + ")"
		}
		t.line("Hidden by a same-named skill with higher priority: %s", strings.Join(names, ", "))
	}
}

// parentDir returns the directory of a path from any machine: the server may
// run on another OS than the machine, so both separators count.
func parentDir(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i > 0 {
		return p[:i]
	}
	return p
}

func without(list []string, drop string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

// renderBriefing formats the reply to a call without a valid token. The first
// lines say what happened and exactly how to retry, because models act on them.
func renderBriefing(m Machine, d protocol.DescribeResult, token, reason, tool, param, changes string) string {
	var t text
	t.line("[lanesh] nothing was run on %q: %s.", m.Name, reason)
	t.line("Next: repeat the same %s call with %s %q. This is the machine's full briefing; no describe_machine needed.", tool, param, token)
	if changes != "" {
		t.raw(changes)
	}
	writeMachine(&t, m, d)
	return t.String()
}

// SkillChanges is the difference between two skill index versions.
type SkillChanges struct {
	Added   []protocol.Skill
	Updated []protocol.Skill
	Removed []protocol.Skill
}

// diffSkills compares skills by name. A skill counts as updated when its
// description or any of its files changed.
func diffSkills(old, cur protocol.SkillIndex) SkillChanges {
	before := make(map[string]protocol.Skill, len(old.Skills))
	for _, s := range old.Skills {
		before[s.Name] = s
	}
	var ch SkillChanges
	for _, s := range cur.Skills {
		prev, ok := before[s.Name]
		switch {
		case !ok:
			ch.Added = append(ch.Added, s)
		case prev.Hash != s.Hash || prev.Description != s.Description:
			ch.Updated = append(ch.Updated, s)
		}
		delete(before, s.Name)
	}
	for _, s := range old.Skills {
		if _, gone := before[s.Name]; gone {
			ch.Removed = append(ch.Removed, s)
		}
	}
	return ch
}

// renderChanges formats skill changes for a briefing; it is empty when only
// something else (such as which same-named skill takes priority) changed.
func renderChanges(ch SkillChanges) string {
	var b strings.Builder
	for _, s := range ch.Added {
		fmt.Fprintf(&b, "\n+ %s (new)", s.Name)
	}
	for _, s := range ch.Updated {
		fmt.Fprintf(&b, "\n~ %s (updated; if you read it before, read it again before relying on it)", s.Name)
	}
	for _, s := range ch.Removed {
		fmt.Fprintf(&b, "\n- %s (removed)", s.Name)
	}
	if b.Len() == 0 {
		return ""
	}
	return "Changes since your last briefing:" + b.String()
}

// maxInstructions keeps instructions within what clients show: Claude Code
// keeps 2,048 characters, and OpenAI advises essentials in the first 512.
const maxInstructions = 2000

// instructionsHead must stay within 512 characters.
const instructionsHead = `Lanesh connects you to the machines of one lane, each with its own skills, shell and files.
- describe_machine shows a machine's skills and gives a briefing token; pass it as briefing for that machine. If a call returns a briefing instead, nothing ran: retry with its token.
- Run a skill's scripts on its machine (exec with skill). Move files with transfer.
- Results end with "` + EndMarker + `"; if it is missing, your client cut the output: read less at once.`

// Instructions builds the server instructions for a lane: usage first, then a
// roster of its machines as of connection time.
func Instructions(machines []Machine) string {
	var b strings.Builder
	b.WriteString(instructionsHead)
	if len(machines) == 0 {
		b.WriteString("\n\nThis lane has no machines yet.")
		return b.String()
	}
	b.WriteString("\n\nMachines at connection time (list_machines for now):")
	for i, m := range machines {
		line := fmt.Sprintf("\n- %s: %s/%s", m.Name, m.OS, m.Arch)
		if m.Shell != "" {
			line += ", " + m.Shell
		}
		line += ", " + plural(m.SkillCount, "skill", "skills")
		if !m.Policy.AllowExec {
			line += ", no exec"
		}
		if !m.Online {
			line += ", offline"
		}
		more := fmt.Sprintf("\n- and %d more (see list_machines)", len(machines)-i)
		if b.Len()+len(line)+len(more) > maxInstructions {
			b.WriteString(more)
			break
		}
		b.WriteString(line)
	}
	return b.String()
}
