package statechart_test

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A commit/abort chart whose start state sorts last by chart ID, as in #626's
// sample: the drawing, arrow descriptions and report all number states column
// by column, top to bottom, so the start state is State 1.
func TestSVGNumbersStatesInReadingOrder626(t *testing.T) {
	labels := []string{"Prepared", "Committed", "Aborted", "Started"}
	c := svg613Chart(t, labels, []int{3}, []svg613Edge{
		{3, 0, "Prepare", false}, {3, 2, "Abort", false}, {0, 1, "Commit", false},
		{0, 2, "Abort", false}, {2, 3, "Restart", false}, {1, 1, "Stay", false},
	})
	if c.States[len(c.States)-1].ID != c.Initial[0] {
		t.Fatal("the fixture's initial state must sort last by chart ID")
	}
	n := svg613Render(t, c)
	number := map[string]int{}
	label := map[string]string{}
	for _, s := range c.States {
		label[s.ID] = s.Label
	}
	for _, g := range n.groups("data-state-id") {
		var got int
		spans := g.all("tspan")
		if len(spans) == 0 {
			t.Fatal("state without drawn text")
		}
		if _, err := fmt.Sscanf(spans[0].text, "State %d ·", &got); err != nil {
			t.Fatalf("state text %q has no number: %v", spans[0].text, err)
		}
		number[g.attrs["data-state-id"]] = got
	}
	if number[c.Initial[0]] != 1 {
		t.Fatalf("the initial state is State %d, want State 1", number[c.Initial[0]])
	}
	// Columns, then rows: sorting the drawn boxes left to right, then top to
	// bottom, must give States 1, 2, 3 and 4.
	boxes := svg613StateBoxes(t, n)
	ids := slices.Collect(maps.Keys(boxes))
	slices.SortFunc(ids, func(a, b string) int {
		return cmp.Or(cmp.Compare(boxes[a].x0, boxes[b].x0), cmp.Compare(boxes[a].y0, boxes[b].y0))
	})
	for i, id := range ids {
		if number[id] != i+1 {
			t.Fatalf("reading order %v gives %s State %d, want %d", ids, id, number[id], i+1)
		}
	}
	for _, g := range n.groups("data-from") {
		var from, to int
		desc := g.all("desc")
		if len(desc) != 1 {
			t.Fatal("arrow without one description")
		}
		if _, err := fmt.Sscanf(desc[0].text, "State %d to state %d;", &from, &to); err != nil {
			t.Fatalf("arrow description %q: %v", desc[0].text, err)
		}
		if from != number[g.attrs["data-from"]] || to != number[g.attrs["data-to"]] {
			t.Fatalf("arrow %s -> %s described as State %d to state %d", g.attrs["data-from"], g.attrs["data-to"], from, to)
		}
	}
	for _, g := range n.groups("data-initial") {
		titles := g.all("title")
		if len(titles) != 1 || titles[0].text != fmt.Sprintf("Initial state %d", number[g.attrs["data-initial"]]) {
			t.Fatalf("initial indicator names another state: %+v", titles)
		}
	}
	// The report lists states in the same order and arrows by their numbered
	// endpoints, with every number naming the state the drawing gives it.
	var lines []string
	for _, g := range n.all("g") {
		if g.attrs["id"] == "chart-report" {
			for _, span := range g.all("tspan") {
				lines = append(lines, span.text)
			}
		}
	}
	byNumber := map[int]string{}
	for id, k := range number {
		byNumber[k] = label[id]
	}
	type arrow struct {
		from, to int
		label    string
	}
	want := map[arrow]bool{}
	for _, a := range c.Arrows {
		want[arrow{number[a.From], number[a.To], a.Label}] = true
	}
	states, last := 0, arrow{}
	for i, line := range lines {
		var k, from, to int
		if _, err := fmt.Sscanf(line, "State %d:", &k); err == nil {
			states++
			if k != states || line != fmt.Sprintf("State %d: %s", k, byNumber[k]) {
				t.Fatalf("report state line %q, want State %d: %s", line, states, byNumber[states])
			}
		}
		var name string
		if _, err := fmt.Sscanf(line, "Visible arrow %d: state %d -> state %d;", &k, &from, &to); err == nil {
			name = "visible"
		}
		if name == "" {
			continue
		}
		got := arrow{from, to, lines[i+1]}
		if !want[got] {
			t.Fatalf("report arrow %q %q names no drawn arrow", line, lines[i+1])
		}
		delete(want, got)
		if cmp.Or(cmp.Compare(got.from, last.from), cmp.Compare(got.to, last.to)) < 0 {
			t.Fatalf("report arrows out of reading order: %+v after %+v", got, last)
		}
		last = got
	}
	if states != len(c.States) || len(want) != 0 {
		t.Fatalf("report listed %d states and missed arrows %v", states, want)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "State 1: Started") {
		t.Fatal("the report doesn't list the initial state first")
	}
}

// Hidden arrows use the same numbers and order as visible ones.
func TestSVGNumbersHiddenArrowsInReadingOrder626(t *testing.T) {
	c := svg613Chart(t, []string{"Working", "Done", "Ready"}, []int{2}, []svg613Edge{
		{2, 0, "Begin", false}, {0, 1, "Finish", false}, {0, 0, "Poll", true}, {2, 2, "Poll", true},
	})
	if len(c.Report.Hidden) != 2 {
		t.Fatalf("fixture needs two hidden arrows, got %+v", c.Report.Hidden)
	}
	n := svg613Render(t, c)
	var lines []string
	for _, g := range n.all("g") {
		if g.attrs["id"] == "chart-report" {
			for _, span := range g.all("tspan") {
				lines = append(lines, span.text)
			}
		}
	}
	var hidden []string
	for _, line := range lines {
		if regexp.MustCompile(`^Hidden arrow \d+:`).MatchString(line) {
			hidden = append(hidden, line)
		}
	}
	want := []string{"Hidden arrow 1: state 1 -> state 1; 1 concrete occurrence", "Hidden arrow 2: state 2 -> state 2; 1 concrete occurrence"}
	if !slices.Equal(hidden, want) {
		t.Fatalf("hidden arrows %q, want %q", hidden, want)
	}
}
