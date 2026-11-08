package mcp

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool results are plain text, framed so that an agent can tell when
// something is missing:
//
//   - the first line starts with "[lanesh]" and states what follows;
//   - sections (files, command output) open with "[lanesh] begin …" and close
//     with "[lanesh] end …";
//   - wherever Lanesh itself leaves something out, a "[lanesh] …" line says
//     how much and how to get it;
//   - the last line is always EndMarker. If an agent does not see it, or sees
//     a begin without its end, its client cut the result.
//
// Results are kept within a budget so that common client limits (Claude Code
// and Codex cap tool output at roughly 10–25k tokens) are rarely reached.

// EndMarker is the last line of every tool result.
const EndMarker = "[lanesh] end of result"

// Budgets, in estimated tokens.
const (
	resultBudget   = 7000 // a whole result
	skillsBudget   = 5000 // the skill list inside describe_machine
	descMaxRunes   = 200  // skill descriptions in lists
	fileReadBudget = 20 << 10
)

// estimateTokens approximates the token count of s: about four ASCII
// characters per token and one token per CJK or other wide character.
func estimateTokens(s string) int {
	ascii, wide := 0, 0
	for _, r := range s {
		if r < 0x80 {
			ascii++
		} else {
			wide++
		}
	}
	return ascii/4 + wide
}

// text accumulates a framed tool result.
type text struct {
	b strings.Builder
}

func (t *text) line(format string, a ...any) {
	fmt.Fprintf(&t.b, format, a...)
	t.b.WriteByte('\n')
}

func (t *text) raw(s string) {
	t.b.WriteString(s)
	if !strings.HasSuffix(s, "\n") {
		t.b.WriteByte('\n')
	}
}

func (t *text) tokens() int { return estimateTokens(t.b.String()) }

// result finishes the text with EndMarker and wraps it in a tool result.
func (t *text) result() *mcp.CallToolResult {
	t.b.WriteString(EndMarker)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: t.b.String()}}}
}

// String returns the framed text with EndMarker, for errors.
func (t *text) String() string { return t.b.String() + EndMarker }

// shorten cuts s to at most n runes, marking the cut with an ellipsis.
func shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// size formats a byte count for humans.
func size(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
