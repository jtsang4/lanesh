package mcp

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// Tree rendering limits: directories holding more files than collapseFiles
// are shown as one line, and if the tree is still longer than maxTreeLines,
// every subdirectory is collapsed.
const (
	collapseFiles = 25
	maxTreeLines  = 150
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
// "" for the skill root) as an indented tree, collapsing large directories.
// It returns the lines and how many directories were collapsed.
func renderTree(root string, entries []protocol.FileEntry) (lines []string, collapsed int) {
	top := buildTree(root, entries)
	render := func(threshold int, collapseAllSubdirs bool) ([]string, int) {
		var out []string
		folded := 0
		var walk func(n *treeNode, rel string, depth int)
		walk = func(n *treeNode, rel string, depth int) {
			for _, c := range sortedChildren(n) {
				p := path.Join(rel, c.name)
				indent := strings.Repeat("  ", depth)
				if !c.dir {
					out = append(out, fmt.Sprintf("%s%s (%s)", indent, c.name, size(c.size)))
					continue
				}
				if c.files > threshold || (collapseAllSubdirs && c.files > 0) {
					out = append(out, fmt.Sprintf("%s%s/ (%s, %s; list with path=%q)", indent, c.name, plural(c.files, "file", "files"), size(c.bytes), p+"/"))
					folded++
					continue
				}
				out = append(out, indent+c.name+"/")
				walk(c, p, depth+1)
			}
		}
		walk(top, root, 0)
		return out, folded
	}
	lines, collapsed = render(collapseFiles, false)
	if len(lines) > maxTreeLines {
		lines, collapsed = render(collapseFiles, true)
	}
	return lines, collapsed
}
