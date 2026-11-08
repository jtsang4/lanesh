package mcp

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// Line budgets for rendered trees: a directory listing, and the file tree
// shown next to SKILL.md (smaller, since SKILL.md already takes much of the
// result budget).
const (
	maxTreeLines   = 150
	skillTreeLines = 60
)

type treeNode struct {
	name     string
	dir      bool
	size     int64
	files    int   // files below a directory
	bytes    int64 // bytes below a directory
	children map[string]*treeNode
}

func buildTree(root string, entries []protocol.FileEntry) *treeNode {
	top := &treeNode{dir: true, children: map[string]*treeNode{}}
	prefix := ""
	if root != "" {
		prefix = strings.TrimSuffix(root, "/") + "/"
	}
	for _, e := range entries {
		rel := strings.TrimPrefix(e.Path, prefix)
		if rel == "" || rel == e.Path && prefix != "" {
			continue
		}
		parts := strings.Split(rel, "/")
		n := top
		for i, p := range parts {
			last := i == len(parts)-1
			child := n.children[p]
			if child == nil {
				child = &treeNode{name: p, dir: !last || e.Dir, children: map[string]*treeNode{}}
				n.children[p] = child
			}
			if last && !e.Dir {
				child.size = e.Size
			}
			n = child
		}
	}
	var count func(*treeNode)
	count = func(n *treeNode) {
		for _, c := range n.children {
			if c.dir {
				count(c)
				n.files += c.files
				n.bytes += c.bytes
			} else {
				n.files++
				n.bytes += c.size
			}
		}
	}
	count(top)
	return top
}

func sortedChildren(n *treeNode) []*treeNode {
	out := make([]*treeNode, 0, len(n.children))
	for _, c := range n.children {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		// SKILL.md first, then directories, then files, each alphabetically.
		if out[i].name == "SKILL.md" || out[j].name == "SKILL.md" {
			return out[i].name == "SKILL.md"
		}
		if out[i].dir != out[j].dir {
			return out[i].dir
		}
		return out[i].name < out[j].name
	})
	return out
}

// renderTree lists entries below root (a directory relative to the skill, or
// "" for the skill root) as an indented tree. Directories are expanded level
// by level while the tree stays within maxLines; when a whole level does
// not fit, its smallest directories are expanded first and deeper levels stay
// collapsed. Collapsed directories show their file count and size; collapsed
// holds their paths. omitted counts entries of root itself beyond maxLines.
func renderTree(root string, entries []protocol.FileEntry, maxLines int) (lines []string, collapsed []string, omitted int) {
	top := buildTree(root, entries)
	open := map[*treeNode]bool{top: true}
	visible := len(top.children)
	level := childDirs(top)
	for len(level) > 0 && visible < maxLines {
		cost := 0
		for _, d := range level {
			cost += len(d.children)
		}
		if visible+cost <= maxLines {
			var next []*treeNode
			for _, d := range level {
				open[d] = true
				next = append(next, childDirs(d)...)
			}
			visible += cost
			level = next
			continue
		}
		sort.SliceStable(level, func(i, j int) bool { return len(level[i].children) < len(level[j].children) })
		for _, d := range level {
			if visible+len(d.children) <= maxLines {
				open[d] = true
				visible += len(d.children)
			}
		}
		break
	}

	var walk func(n *treeNode, rel string, depth int)
	walk = func(n *treeNode, rel string, depth int) {
		for _, c := range sortedChildren(n) {
			if len(lines) >= maxLines {
				omitted++
				continue
			}
			p := path.Join(rel, c.name)
			indent := strings.Repeat("  ", depth)
			switch {
			case !c.dir:
				lines = append(lines, fmt.Sprintf("%s%s (%s)", indent, c.name, size(c.size)))
			case open[c] || len(c.children) == 0:
				lines = append(lines, indent+c.name+"/")
				walk(c, p, depth+1)
			default:
				lines = append(lines, fmt.Sprintf("%s%s/ (collapsed: %s, %s)", indent, c.name, plural(c.files, "file", "files"), size(c.bytes)))
				collapsed = append(collapsed, p+"/")
			}
		}
	}
	walk(top, root, 0)
	return lines, collapsed, omitted
}

func childDirs(n *treeNode) []*treeNode {
	var out []*treeNode
	for _, c := range sortedChildren(n) {
		if c.dir {
			out = append(out, c)
		}
	}
	return out
}
