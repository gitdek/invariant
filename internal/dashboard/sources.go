package dashboard

import (
	"regexp"
	"strings"
)

// Decision is one entry in decisions/log.md.
type Decision struct {
	ID     string `json:"id"`
	Date   string `json:"date"`
	Door   string `json:"door"`
	Status string `json:"status"`
	Text   string `json:"text"`
	Who    string `json:"who"`
}

var (
	mdLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	logID  = regexp.MustCompile(`^D-\d{4}$`)
)

// ParseLog reads the decision log's table, oldest first.
func ParseLog(md string) []Decision {
	var out []Decision
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 6 {
			continue
		}
		for i := range cells {
			cells[i] = plain(cells[i])
		}
		if !logID.MatchString(cells[0]) {
			continue
		}
		out = append(out, Decision{ID: cells[0], Date: cells[1], Door: cells[2], Status: cells[3], Text: cells[4], Who: cells[5]})
	}
	return out
}

// plain turns a line of Markdown into text: links become their text, and
// code and emphasis marks go.
func plain(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	s = strings.NewReplacer("`", "", "**", "").Replace(s)
	return strings.TrimSpace(s)
}

// Slice is a step on the roadmap.
type Slice struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status"` // done, now, proposed, planned or later
}

var (
	readmeSlice = regexp.MustCompile(`^- \[([ x])\] \*\*Slice (\w+) · (.+?)\.?\*\*\s*(.*)$`)
	refs        = regexp.MustCompile(`\s*\((?:\d+\.\d+(?:, )?)+\)|\s*` + "`" + `D-\d{4}` + "`")
)

// ParseSlices reads the roadmap: the slices so far from the README's status
// list, then the ones after it from the PRD's roadmap table.
func ParseSlices(readme, prd string) []Slice {
	var out []Slice
	now := false
	for _, line := range strings.Split(readme, "\n") {
		m := readmeSlice.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		s := Slice{ID: m[2], Title: plain(m[3]), Detail: firstSentence(plain(m[4])), Status: "done"}
		if m[1] == " " {
			s.Status = "later"
			if !now {
				s.Status, now = "now", true
			}
		}
		out = append(out, s)
	}
	last := 0
	for _, s := range out {
		if n := leadingNumber(s.ID); n > last {
			last = n
		}
	}
	inRoadmap := false
	for _, line := range strings.Split(prd, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			inRoadmap = t == "## Roadmap"
			continue
		}
		if !inRoadmap || !strings.HasPrefix(t, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(t, "|"), "|")
		if len(cells) != 3 {
			continue
		}
		id := strings.TrimSpace(cells[0])
		if !isNumber(id) && id != "Later" {
			continue
		}
		if isNumber(id) && atoi(id) <= last {
			continue
		}
		what := strings.TrimSpace(refs.ReplaceAllString(plain(cells[1]), ""))
		status := strings.ToLower(strings.Fields(plain(cells[2]) + " later")[0])
		status = strings.Trim(status, ".,")
		switch status {
		case "next":
			// The next slice on the road is the one in progress, once the
			// README's are all done.
			status = "planned"
			if !now {
				status, now = "now", true
			}
		case "proposed", "planned", "later", "done":
		default:
			status = "proposed"
		}
		out = append(out, Slice{ID: id, Title: shortTitle(what), Detail: what, Status: status})
	}
	return out
}

// shortTitle keeps a roadmap entry's name: what comes before a colon, or
// before its first comma when that's a name on its own.
func shortTitle(s string) string {
	if i := strings.Index(s, ": "); i > 0 {
		return s[:i]
	}
	if i := strings.Index(s, ", "); i >= 20 {
		return s[:i]
	}
	return s
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// leadingNumber reads the number a slice's ID starts with: 3 for "3b".
func leadingNumber(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
