package files

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/jtsang4/lanesh/internal/protocol"
)

const (
	maxEditsPerCall = 50
	maxDiffBytes    = 8 << 10
	maxLCSProduct   = 400_000
)

// EditFile applies one or more unique substring replacements to a UTF-8 text
// file atomically and returns a compact unified diff of the change.
func EditFile(path string, edits []protocol.FileEditOp) (protocol.FileEditResult, error) {
	if len(edits) == 0 {
		return protocol.FileEditResult{}, errors.New("at least one edit is required")
	}
	if len(edits) > maxEditsPerCall {
		return protocol.FileEditResult{}, fmt.Errorf("at most %d edits per call", maxEditsPerCall)
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return protocol.FileEditResult{}, fmt.Errorf("%s: %w", path, ErrNotFound)
		}
		return protocol.FileEditResult{}, err
	}
	if info.IsDir() {
		return protocol.FileEditResult{}, fmt.Errorf("%s is a directory", path)
	}
	if info.Size() > MaxInline {
		return protocol.FileEditResult{}, fmt.Errorf("%s: %w", path, ErrTooLarge)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // Callers validate path against policy.
	if err != nil {
		return protocol.FileEditResult{}, err
	}
	if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
		return protocol.FileEditResult{}, fmt.Errorf("%s is not a UTF-8 text file", path)
	}

	before := string(raw)
	cur := before
	for i, ed := range edits {
		if ed.OldText == "" {
			return protocol.FileEditResult{}, fmt.Errorf("edit %d: old_text must not be empty", i+1)
		}
		n := strings.Count(cur, ed.OldText)
		if n != 1 {
			return protocol.FileEditResult{}, fmt.Errorf("edit %d: old_text occurs %d times in %s; it must occur exactly once", i+1, n, path)
		}
		cur = strings.Replace(cur, ed.OldText, ed.NewText, 1)
	}
	if len(cur) > MaxInline {
		return protocol.FileEditResult{}, ErrTooLarge
	}

	if cur != before {
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		if err := writeAtomic(path, strings.NewReader(cur), mode); err != nil {
			return protocol.FileEditResult{}, err
		}
	}

	return protocol.FileEditResult{
		Path:    path,
		Size:    int64(len(cur)),
		Applied: len(edits),
		Diff:    unifiedDiff(before, cur, 3),
	}, nil
}

type diffOp struct {
	kind byte // ' ', '-', '+'
	line string
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// unifiedDiff produces a compact unified diff with ctx lines of surrounding
// context, capped at maxDiffBytes so large replacements do not flood output.
func unifiedDiff(before, after string, ctx int) string {
	if before == after {
		return ""
	}
	a := splitLines(before)
	b := splitLines(after)

	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	aEnd, bEnd := len(a), len(b)
	for aEnd > prefix && bEnd > prefix && a[aEnd-1] == b[bEnd-1] {
		aEnd--
		bEnd--
	}

	var ops []diffOp
	for i := range prefix {
		ops = append(ops, diffOp{' ', a[i]})
	}
	midA, midB := a[prefix:aEnd], b[prefix:bEnd]
	if len(midA)*len(midB) <= maxLCSProduct {
		ops = append(ops, lcsDiff(midA, midB)...)
	} else {
		for _, l := range midA {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range midB {
			ops = append(ops, diffOp{'+', l})
		}
	}
	for i := aEnd; i < len(a); i++ {
		ops = append(ops, diffOp{' ', a[i]})
	}

	return formatHunks(ops, ctx)
}

func lcsDiff(a, b []string) []diffOp {
	m, n := len(a), len(b)
	// dp[(i)*(n+1)+j] is the LCS length of a[i:] and b[j:].
	dp := make([]int, (m+1)*(n+1))
	for i := m - 1; i >= 0; i-- {
		row := i * (n + 1)
		nextRow := (i + 1) * (n + 1)
		for j := n - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				dp[row+j] = dp[nextRow+j+1] + 1
			case dp[nextRow+j] >= dp[row+j+1]:
				dp[row+j] = dp[nextRow+j]
			default:
				dp[row+j] = dp[row+j+1]
			}
		}
	}
	ops := make([]diffOp, 0, m+n)
	i, j := 0, 0
	for i < m && j < n {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case dp[(i+1)*(n+1)+j] >= dp[i*(n+1)+j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < m; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < n; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

func formatHunks(ops []diffOp, ctx int) string {
	type span struct{ start, end int }
	var hunks []span
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		s := max(0, i-ctx)
		e := min(len(ops), i+ctx+1)
		if len(hunks) > 0 && s <= hunks[len(hunks)-1].end {
			hunks[len(hunks)-1].end = e
		} else {
			hunks = append(hunks, span{s, e})
		}
	}
	if len(hunks) == 0 {
		return ""
	}
	// Precompute 1-based old/new line numbers before each op.
	oldLine := make([]int, len(ops)+1)
	newLine := make([]int, len(ops)+1)
	ol, nl := 1, 1
	for i, op := range ops {
		oldLine[i], newLine[i] = ol, nl
		if op.kind != '+' {
			ol++
		}
		if op.kind != '-' {
			nl++
		}
	}
	oldLine[len(ops)], newLine[len(ops)] = ol, nl

	var b strings.Builder
	for _, h := range hunks {
		oStart, nStart := oldLine[h.start], newLine[h.start]
		oCount := oldLine[h.end] - oStart
		nCount := newLine[h.end] - nStart
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oStart, oCount, nStart, nCount)
		for _, op := range ops[h.start:h.end] {
			line := string(op.kind) + op.line + "\n"
			if b.Len()+len(line) > maxDiffBytes {
				b.WriteString("… (diff truncated)\n")
				return b.String()
			}
			b.WriteString(line)
		}
	}
	return b.String()
}
