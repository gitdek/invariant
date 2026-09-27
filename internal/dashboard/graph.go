package dashboard

import (
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/tlc"
)

// Graph is a model's state graph as TLC explored it, in the compact form
// the page draws: states by index, the initial ones, and every edge with its
// action. The page lays it out as orbits, one ring per step from Init.
type Graph struct {
	States  int       `json:"states"`
	Init    []int     `json:"init"`
	Actions []string  `json:"actions"`
	Edges   []int     `json:"edges"`            // flat triples: from, to, action
	Labels  []string  `json:"labels,omitempty"` // each state as TLA+, when the graph is small enough to send them
	Bugs    []BugPath `json:"bugs,omitempty"`

	vars []map[string]string // each state's variables, for finding a trace's states
}

// BugPath is a known bug's counterexample, drawn on the graph: the states
// the correct model shares with it, then the step that breaks an invariant.
type BugPath struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Says     string `json:"says"`
	Violated string `json:"violated"`
	Steps    int    `json:"steps"`
	Path     []int  `json:"path"`   // states of the counterexample the model reaches, from an initial state
	Action   string `json:"action"` // the step that leaves the model
	Escape   string `json:"escape"` // the state that breaks the invariant, as TLA+
}

// maxLabels is the most states whose TLA+ the page gets. Past it, the graph
// is drawn without hover text, to keep the download small.
const maxLabels = 2000

var (
	dotEdge = regexp.MustCompile(`^(-?\d+) -> (-?\d+)(?: \[label="((?:[^"\\]|\\.)*)")?`)
	dotNode = regexp.MustCompile(`^(-?\d+) \[label="((?:[^"\\]|\\.)*)"(,style = filled)?\]`)
)

// ParseDot reads the state graph TLC writes with -dump dot,actionlabels.
func ParseDot(dot string) (*Graph, error) {
	g := &Graph{}
	index := map[string]int{}
	actions := map[string]int{}
	state := func(key string) int {
		if i, ok := index[key]; ok {
			return i
		}
		i := len(index)
		index[key] = i
		g.vars = append(g.vars, nil)
		g.Labels = append(g.Labels, "")
		return i
	}
	for _, line := range strings.Split(dot, "\n") {
		line = strings.TrimSpace(line)
		if m := dotEdge.FindStringSubmatch(line); m != nil {
			from, to := state(m[1]), state(m[2])
			name := unescape(m[3])
			a, ok := actions[name]
			if !ok {
				a = len(g.Actions)
				actions[name] = a
				g.Actions = append(g.Actions, name)
			}
			g.Edges = append(g.Edges, from, to, a)
			continue
		}
		if m := dotNode.FindStringSubmatch(line); m != nil {
			i := state(m[1])
			g.Labels[i] = unescape(m[2])
			g.vars[i] = stateVars(g.Labels[i])
			if m[3] != "" {
				g.Init = append(g.Init, i)
			}
		}
	}
	g.States = len(index)
	if g.States == 0 || len(g.Init) == 0 {
		return nil, errors.New("TLC's dump has no initial state")
	}
	return g, nil
}

// Trace finds a known bug's counterexample on the graph. The trace comes
// from the model with the bug added, so its last state breaks an invariant
// and isn't in the graph; every state before the bug first acts should be.
func (g *Graph) Trace(t tlc.TraceFile) (path []int, action, escape string) {
	find := func(s tlc.TraceState) int {
		want := map[string]string{}
		for k, v := range s.TLA {
			want[k] = squash(v)
		}
	next:
		for i, have := range g.vars {
			if len(have) != len(want) {
				continue
			}
			for k, v := range want {
				if have[k] != v {
					continue next
				}
			}
			return i
		}
		return -1
	}
	for i, s := range t.States {
		n := find(s)
		if n < 0 || i == len(t.States)-1 {
			return path, s.Action, tlaState(s)
		}
		path = append(path, n)
	}
	return path, "", ""
}

// Compact drops a big graph's labels, which would make the page's download
// too big.
func (g *Graph) Compact() {
	if g.States > maxLabels {
		g.Labels = nil
	}
}

// stateVars splits a state as TLC prints it, "/\ x = 1" per variable, into
// each variable's value, with whitespace squashed.
func stateVars(text string) map[string]string {
	vars := map[string]string{}
	var name string
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, `/\ `) {
			if eq := strings.Index(t, " = "); eq > 0 {
				name = strings.TrimSpace(t[3:eq])
				vars[name] = t[eq+3:]
				continue
			}
		}
		if name != "" {
			vars[name] += " " + t
		}
	}
	if len(vars) == 0 {
		// A spec with one variable prints it without the conjunction.
		if eq := strings.Index(text, " = "); eq > 0 {
			vars[strings.TrimSpace(text[:eq])] = text[eq+3:]
		}
	}
	for k, v := range vars {
		vars[k] = squash(v)
	}
	return vars
}

func tlaState(s tlc.TraceState) string {
	names := make([]string, 0, len(s.TLA))
	for k := range s.TLA {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, k := range names {
		b.WriteString(`/\ ` + k + " = " + squash(s.TLA[k]) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			if s[i] == 'n' {
				b.WriteByte('\n')
			} else {
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }
