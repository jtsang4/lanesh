package mcp

import (
	"sort"
	"strings"
	"unicode"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// queryTerms splits a query into lowercase terms: words for scripts with
// spaces, and character pairs (bigrams) for runs of Han characters, which are
// written without spaces. A single Han character is kept as a term on its own.
func queryTerms(q string) []string {
	var terms []string
	seen := map[string]bool{}
	addTerm := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			terms = append(terms, t)
		}
	}
	flushHan := func(run []rune) {
		if len(run) == 1 {
			addTerm(string(run))
		}
		for i := 0; i+1 < len(run); i++ {
			addTerm(string(run[i : i+2]))
		}
	}
	var word []rune
	var han []rune
	for _, r := range strings.ToLower(q) {
		switch {
		case unicode.Is(unicode.Han, r):
			if len(word) > 0 {
				addTerm(string(word))
				word = nil
			}
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if len(han) > 0 {
				flushHan(han)
				han = nil
			}
			word = append(word, r)
		default:
			if len(word) > 0 {
				addTerm(string(word))
				word = nil
			}
			if len(han) > 0 {
				flushHan(han)
				han = nil
			}
		}
	}
	if len(word) > 0 {
		addTerm(string(word))
	}
	if len(han) > 0 {
		flushHan(han)
	}
	return terms
}

// skillMatch is one skill name across the machines that have it.
type skillMatch struct {
	name        string
	description string
	machines    []string
	offline     map[string]bool
	hashes      map[string]bool
	coverage    float64
	score       int
}

// scoreSkill rates how well a skill matches the terms: coverage is the share
// of terms found in its name or description; score weighs where they were found.
func scoreSkill(name, description string, terms []string) (coverage float64, score int) {
	n := strings.ToLower(name)
	d := strings.ToLower(description)
	parts := strings.FieldsFunc(n, func(r rune) bool { return r == '-' || r == '_' || r == '.' || unicode.IsSpace(r) })
	hit := 0
	for _, t := range terms {
		best := 0
		switch {
		case n == t:
			best = 6
		case contains(parts, t):
			best = 4
		case strings.Contains(n, t):
			best = 3
		case strings.Contains(d, t):
			best = 1
		}
		if best > 0 {
			hit++
			score += best
		}
	}
	if len(terms) == 0 {
		return 0, 0
	}
	return float64(hit) / float64(len(terms)), score
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// findSkills groups skills by name across machines and ranks them against
// the query. With no query, every skill matches. partial reports that no skill
// covered at least half of the terms, so only the closest ones are returned.
func findSkills(machines []Machine, indexes map[string]protocol.SkillIndex, query string) (matches []*skillMatch, partial bool) {
	terms := queryTerms(query)
	byName := map[string]*skillMatch{}
	for _, m := range machines {
		for _, s := range indexes[m.ID].Skills {
			sm := byName[s.Name]
			if sm == nil {
				sm = &skillMatch{name: s.Name, description: s.Description, offline: map[string]bool{}, hashes: map[string]bool{}}
				sm.coverage, sm.score = scoreSkill(s.Name, s.Description, terms)
				if len(terms) == 0 {
					sm.coverage = 1
				}
				byName[s.Name] = sm
			}
			sm.machines = append(sm.machines, m.Name)
			sm.hashes[s.Hash] = true
			if !m.Online {
				sm.offline[m.Name] = true
			}
		}
	}
	for _, sm := range byName {
		if sm.coverage > 0 {
			matches = append(matches, sm)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.coverage != b.coverage {
			return a.coverage > b.coverage
		}
		if a.score != b.score {
			return a.score > b.score
		}
		return a.name < b.name
	})
	strong := 0
	for _, sm := range matches {
		if sm.coverage >= 0.5 {
			strong++
		}
	}
	if strong > 0 {
		return matches[:strong], false
	}
	if len(matches) > 5 {
		matches = matches[:5]
	}
	return matches, len(matches) > 0
}
