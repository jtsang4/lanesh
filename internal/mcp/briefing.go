package mcp

import (
	"errors"
	"fmt"
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
			texts = append(texts, br.Text)
		default:
			return err
		}
	}
	if len(texts) == 0 {
		return nil
	}
	return &BriefingRequired{Text: strings.Join(texts, "\n\n---\n\n")}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// renderBriefing formats a machine briefing for an agent. The first lines say
// what happened and exactly how to retry, because models act on them; the
// briefing below is the same information describe_machine returns.
func renderBriefing(m Machine, d protocol.DescribeResult, token, reason, tool, param string) string {
	var b strings.Builder
	env := d.Environment
	fmt.Fprintf(&b, "Nothing was run on %q: %s.\n", m.Name, reason)
	fmt.Fprintf(&b, "Next step: read the briefing below, then repeat the same %s call with %s %q. "+
		"This reply already is the machine's full briefing, so do not call describe_machine for it.\n\n", tool, param, token)
	fmt.Fprintf(&b, "Machine: %s (%s/%s), user %s, home %s\n", m.Name, env.OS, env.Arch, env.User, env.Home)
	fmt.Fprintf(&b, "Shell: %s", env.DefaultShell)
	if len(env.Shells) > 0 {
		fmt.Fprintf(&b, " (available: %s)", strings.Join(env.Shells, ", "))
	}
	fmt.Fprintf(&b, "\nAllows: shared skills %s; commands and file access %s\n", yesNo(d.Policy.ExposeSkills), yesNo(d.Policy.AllowExec))
	skills := d.Skills.Skills
	if len(skills) == 0 {
		b.WriteString("Skills: none\n")
	} else {
		fmt.Fprintf(&b, "Skills (%d); read one with read_skill, run its scripts here with exec and skill set:\n", len(skills))
		for _, s := range skills {
			fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
		}
	}
	if len(d.Skills.Shadowed) > 0 {
		names := make([]string, 0, len(d.Skills.Shadowed))
		for _, s := range d.Skills.Shadowed {
			names = append(names, s.Name+" ("+s.Source+")")
		}
		fmt.Fprintf(&b, "Hidden by a same-named skill with higher priority: %s\n", strings.Join(names, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// Limits for the instructions text: Claude Code keeps the first 2,048
// characters, and OpenAI recommends putting essentials in the first 512.
const (
	maxInstructions = 2000
)

// instructionsHead must stay within 512 characters.
const instructionsHead = `Lanesh connects you to the machines of one lane; each has its own skills, shell and files.
1. describe_machine(machine) returns a machine's environment, skills and briefing token.
2. Pass that token as briefing in every other call for that machine. If a call returns a briefing instead of a result, nothing ran: retry the same call with the token it gives, without describing again.
3. Run a skill's scripts on the machine that has it: exec with skill set.
4. Files are not shared: use transfer between machines.`

const instructionsBody = `

Long commands keep running after exec returns: follow them with job_read (set wait_ms) and stop them with job_kill. Use read_file and write_file only for small files.`

// Instructions builds the server instructions for a lane: usage first, then a
// roster of its machines as of connection time.
func Instructions(machines []Machine) string {
	var b strings.Builder
	b.WriteString(instructionsHead)
	b.WriteString(instructionsBody)
	if len(machines) == 0 {
		b.WriteString("\n\nThis lane has no machines yet.")
		return b.String()
	}
	b.WriteString("\n\nMachines when you connected (list_machines shows the current state):")
	for i, m := range machines {
		line := fmt.Sprintf("\n- %s: %s/%s", m.Name, m.OS, m.Arch)
		if m.Shell != "" {
			line += ", " + m.Shell
		}
		line += fmt.Sprintf(", %d skills", m.SkillCount)
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
