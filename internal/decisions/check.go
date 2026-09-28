package decisions

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Check reports what's wrong with the graph after a rebuild from the
// journals: an edge to a decision that doesn't exist, a SPEC line that rests
// on a superseded decision, a SPEC bullet that traces to no decision, and a
// decisions/log.md that isn't the store's view. The journals' chains were
// checked as they were read.
func (s *Store) Check(repos []Repo) ([]string, error) {
	var problems []string
	loaded := map[string]bool{}
	for _, r := range repos {
		loaded[r.Name] = true
	}
	rows, err := s.db.Query(`SELECT e.src, e.type, e.dst FROM edge e LEFT JOIN node n ON n.id = e.dst WHERE n.id IS NULL ORDER BY e.src, e.dst`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var src, typ, dst string
		if err := rows.Scan(&src, &typ, &dst); err != nil {
			rows.Close()
			return nil, err
		}
		if project := dst[:strings.LastIndex(dst, "/")]; loaded[project] {
			problems = append(problems, fmt.Sprintf("%s %s %s, which isn't a decision", src, typ, dst))
		}
	}
	rows.Close()
	stale, err := s.nodes(`SELECT ` + nodeColumns + ` FROM node n JOIN edge e ON e.src = n.id JOIN node d ON d.id = e.dst
		WHERE n.kind = 'spec' AND d.status = 'superseded' ORDER BY n.id`)
	if err != nil {
		return nil, err
	}
	for _, n := range stale {
		problems = append(problems, fmt.Sprintf("%s rests on a superseded decision: %s", n.ID, n.Text))
	}
	for _, r := range repos {
		spec, err := os.ReadFile(filepath.Join(r.Dir, "SPEC.md"))
		if err == nil {
			for _, line := range Untraced(string(spec)) {
				problems = append(problems, fmt.Sprintf("%s/SPEC.md: a bullet traces to no decision: %s", r.Name, line))
			}
		}
		log, err := os.ReadFile(filepath.Join(r.Dir, "decisions", "log.md"))
		if err != nil {
			continue
		}
		decisions, err := s.Decisions(r.Name)
		if err != nil {
			return nil, err
		}
		want, err := WithTable(string(log), Table(decisions, r.Name))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s/decisions/log.md: %v", r.Name, err))
		} else if want != string(log) {
			problems = append(problems, fmt.Sprintf("%s/decisions/log.md isn't the store's view: run `invariant decisions log`", r.Name))
		}
	}
	return problems, nil
}

var bullet = regexp.MustCompile(`^(\s*)(?:[-*] |\d+\. )`)

// Untraced is every bullet of a SPEC that names no decision, itself or
// through the bullets it sits under.
func Untraced(spec string) []string {
	type open struct {
		indent int
		traced bool
	}
	var stack []open
	var out []string
	for _, line := range strings.Split(spec, "\n") {
		m := bullet.FindStringSubmatch(line)
		if m == nil {
			if strings.HasPrefix(line, "#") {
				stack = nil
			}
			continue
		}
		indent := len(m[1])
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		traced := mentionedID.MatchString(line) || (len(stack) > 0 && stack[len(stack)-1].traced)
		if !traced {
			out = append(out, strings.TrimSpace(line))
		}
		stack = append(stack, open{indent, traced})
	}
	return out
}
