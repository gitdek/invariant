package decisions

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// decisions/log.md was the record until D-0096. Its table carries over into
// the journals once, as import lines, and from then on it's a view of the
// store, written between two markers.
const (
	TableStart = "<!-- decisions: the table below is generated from the journal; change it with `invariant decisions` -->"
	TableEnd   = "<!-- decisions: end of the generated table -->"
	tableHead  = "| ID | Date | Door | Status | Decision | Who |\n| :-- | :-- | :-- | :-- | :-- | :-- |\n"
)

var (
	rowID     = regexp.MustCompile(`^(?:\[(D-\d{4,})\]\(([^)]+)\)|(D-\d{4,}))$`)
	refineRef = regexp.MustCompile(`\bfor (D-\d{4,})\b`)
	supersRef = regexp.MustCompile(`\b[Ss]upersedes? (D-\d{4,})\b`)
	reopenRef = regexp.MustCompile(`\b[Rr]eopens? (D-\d{4,})\b`)
)

// ParseLog reads the decisions in decisions/log.md's table as import lines,
// in the table's order. The edges a decision's words spell out are typed: a
// decision "for D-0082" refines it, and one that "supersedes" or "reopens"
// another says so. Every other mention is left to the rebuild, as a
// citation.
func ParseLog(text string) ([]Event, error) {
	var out []Event
	seen := map[string]bool{}
	for n, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "| D-") && !strings.HasPrefix(line, "| [D-") {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "| "), " |"), " | ")
		if len(cells) < 6 {
			return nil, fmt.Errorf("decisions/log.md:%d: a row has an ID, date, door, status, decision and who", n+1)
		}
		m := rowID.FindStringSubmatch(cells[0])
		if m == nil {
			return nil, fmt.Errorf("decisions/log.md:%d: %q isn't a decision ID", n+1, cells[0])
		}
		id, record := m[3], ""
		if id == "" {
			id, record = m[1], m[2]
		}
		if seen[id] {
			return nil, fmt.Errorf("decisions/log.md:%d: %s appears twice", n+1, id)
		}
		seen[id] = true
		status := cells[3]
		if !statuses[status] {
			return nil, fmt.Errorf("decisions/log.md:%d: %s's status %q isn't one the log defines", n+1, id, status)
		}
		said := strings.Join(cells[4:len(cells)-1], " | ")
		e := Event{At: cells[1] + "T00:00:00Z", By: "agent", Op: OpImport, ID: id, Date: cells[1], Door: cells[2],
			Status: status, Who: cells[len(cells)-1], Text: said, Record: record}
		typed := map[string]bool{}
		for _, t := range []struct {
			kind string
			re   *regexp.Regexp
		}{{Refines, refineRef}, {Supersedes, supersRef}, {Reopens, reopenRef}} {
			for _, mm := range t.re.FindAllStringSubmatch(said, -1) {
				if mm[1] != id && !typed[t.kind+mm[1]] {
					typed[t.kind+mm[1]] = true
					e.Edges = append(e.Edges, Edge{Type: t.kind, To: mm[1]})
				}
			}
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("decisions/log.md has no decisions")
	}
	return out, nil
}

// Import writes each decision of decisions/log.md to its own journal file,
// as the first line of each.
func Import(repo Repo, events []Event) error {
	for i := range events {
		if err := appendLine(repo, &events[i], true); err != nil {
			return err
		}
	}
	return nil
}

// Table is decisions/log.md's table, as the store holds the project's
// decisions.
func Table(decisions []Node, project string) string {
	var b strings.Builder
	b.WriteString(tableHead)
	for _, d := range decisions {
		id := strings.TrimPrefix(d.ID, project+"/")
		if d.Record != "" {
			id = "[" + id + "](" + d.Record + ")"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", id, d.Date, d.Door, d.Status, d.Text, d.Who)
	}
	return b.String()
}

// WithTable is decisions/log.md with its table replaced by table, between
// the markers. A log without markers gets them around its first table.
func WithTable(log, table string) (string, error) {
	if i, j := strings.Index(log, TableStart), strings.Index(log, TableEnd); i >= 0 && j > i {
		return log[:i] + TableStart + "\n\n" + table + "\n" + log[j:], nil
	}
	start := strings.Index(log, tableHead)
	if start < 0 {
		return "", fmt.Errorf("decisions/log.md has no decision table")
	}
	end := start + len(tableHead)
	for end < len(log) {
		next := strings.IndexByte(log[end:], '\n')
		if next < 0 || !strings.HasPrefix(log[end:], "| ") {
			break
		}
		end += next + 1
	}
	return log[:start] + TableStart + "\n\n" + table + "\n" + TableEnd + "\n" + log[end:], nil
}

// RenderLog is a repository's decisions/log.md with its table as the
// checkout's journal has the decisions.
func RenderLog(repo Repo) (string, error) {
	log, err := os.ReadFile(filepath.Join(repo.Dir, "decisions", "log.md"))
	if err != nil {
		return "", err
	}
	decisions, err := Load(repo)
	if err != nil {
		return "", err
	}
	nodes := make([]Node, len(decisions))
	for i, d := range decisions {
		nodes[i] = d.Node
	}
	return WithTable(string(log), Table(nodes, repo.Name))
}

// WriteLog brings decisions/log.md's table up to date with the checkout's
// journal, and says whether it changed.
func WriteLog(repo Repo) (bool, error) {
	want, err := RenderLog(repo)
	if err != nil {
		return false, err
	}
	path := filepath.Join(repo.Dir, "decisions", "log.md")
	have, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if string(have) == want {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(want), 0o644)
}
