package statechart_test

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

// Reuse #608's unchanged fixture and XML/topology inspection helpers. These
// tests inspect the rendered image, not the renderer's private layout structs.
// Rectangular label backgrounds are also exercised by svg_regression_test.go.
type svg613Edge struct {
	from, to int
	label    string
	hidden   bool
}

func svg613Chart(t *testing.T, labels []string, initials []int, edges []svg613Edge) *statechart.Result {
	t.Helper()
	in := statechart.Input{
		Graph: tlc.GraphResult{
			Module: "Layout613",
			Config: tlc.Config{Specification: "Spec"},
			Tool: tlc.GraphTool{
				Image:     "java@sha256:" + strings.Repeat("a", 64),
				JarSHA256: strings.Repeat("b", 64),
			},
			Run: tlc.Result{Outcome: tlc.Passed, Version: "TLC2 Version 2.19", Depth: len(labels)},
		},
		Lock: project.Lock{Statements: []project.Statement{
			{Name: "Spec", Kind: project.Spec, Says: "Follow the displayed steps.", SHA256: strings.Repeat("c", 64)},
		}},
		Invariants: map[string]map[string]bool{},
	}
	var states, steps []any
	id := func(i int) string { return fmt.Sprintf("v%d", i) }
	for i, label := range labels {
		v := strconv.Itoa(i)
		in.Graph.Graph.States = append(in.Graph.Graph.States, tlc.GraphState{
			ID: id(i), Label: "phase = " + v, Values: map[string]string{"phase": v},
		})
		in.Invariants[id(i)] = map[string]bool{}
		states = append(states, map[string]any{
			"id": id(i), "when": map[string]any{"eq": []any{map[string]any{"var": "phase"}, v}}, "label": label,
		})
	}
	for _, i := range initials {
		in.Graph.Graph.Initial = append(in.Graph.Graph.Initial, id(i))
	}
	type actionKey struct {
		label  string
		hidden bool
	}
	actions := map[actionKey]string{}
	for _, e := range edges {
		key := actionKey{e.label, e.hidden}
		action, exists := actions[key]
		if !exists {
			action = fmt.Sprintf("Action%d", len(actions))
			actions[key] = action
			steps = append(steps, map[string]any{
				"id": action, "when": map[string]any{"eq": []any{map[string]any{"action": true}, action}},
				"label": e.label, "hide": e.hidden,
			})
		}
		in.Graph.Graph.Steps = append(in.Graph.Graph.Steps, tlc.GraphStep{From: id(e.from), To: id(e.to), Action: action})
	}
	if len(steps) == 0 {
		steps = append(steps, map[string]any{"id": "unused", "when": true, "label": "Unused"})
	}
	in.Graph.StateCount, in.Graph.StepCount = len(labels), len(edges)
	in.Graph.Run.DistinctStates = int64(len(labels))
	in.Graph.Run.StatesGenerated = int64(len(edges) + len(initials))
	c, err := statechart.Compute(t.Context(), in, svg608JSON(t, map[string]any{"version": 1, "state": states, "step": steps}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func svg613Chain(n int, loops bool) ([]string, []svg613Edge) {
	var labels []string
	var edges []svg613Edge
	for i := range n {
		labels = append(labels, fmt.Sprintf("Stage %03d", i))
		if i > 0 {
			edges = append(edges, svg613Edge{i - 1, i, "Continue", false})
		}
		if loops {
			edges = append(edges, svg613Edge{i, i, "Wait", false})
		}
	}
	return labels, edges
}

func TestSVG613ReachabilityLayouts(t *testing.T) {
	chain, chainEdges := svg613Chain(5, false)
	capacity, capacityEdges := svg613Chain(500, true)
	cases := []struct {
		name      string
		make      func(*testing.T) *statechart.Result
		uncrossed bool
	}{
		{"608 ordinary", func(t *testing.T) *statechart.Result {
			return svg608Chart(t, "Ready", "Working", "Begin")
		}, false},
		{"608 literal multiline", func(t *testing.T) *statechart.Result {
			const payload = "<script>alert('x')</script> & \"quoted\" #quot; <foreignObject onload='x'> https://invalid.example/a.svg javascript:alert(1)"
			c := svg608Chart(t, "準備 café & <ready>\n"+payload+"\n"+strings.Repeat("UnbrokenLabel", 24),
				"Working\n準備 café", "Advance <&>\n"+strings.Repeat("a long reader action ", 12))
			for i := range c.Arrows {
				c.Arrows[i].Label += fmt.Sprintf("\nRoute %d: %s", i, strings.Repeat("long action ", i+1))
			}
			return c
		}, false},
		{"chain", func(t *testing.T) *statechart.Result {
			return svg613Chart(t, chain, []int{0}, chainEdges)
		}, true},
		{"diamond", func(t *testing.T) *statechart.Result {
			return svg613Chart(t, []string{"Start", "Left", "Right", "Done"}, []int{0}, []svg613Edge{
				{0, 1, "Choose left", false}, {0, 2, "Choose right", false},
				{1, 3, "Join left", false}, {2, 3, "Join right", false},
			})
		}, false},
		{"cycle with back arrow", func(t *testing.T) *statechart.Result {
			return svg613Chart(t, []string{"Start", "Work", "Done"}, []int{0}, []svg613Edge{
				{0, 1, "Begin", false}, {1, 2, "Finish", false}, {2, 0, "Restart", false},
			})
		}, false},
		{"loops parallel reverse and same layer", func(t *testing.T) *statechart.Result {
			return svg613Chart(t, []string{"Start", "Left", "Right", "End"}, []int{0}, []svg613Edge{
				{0, 1, "Begin", false}, {0, 1, "Retry", false}, {1, 0, "Reset", false},
				{0, 2, "Other", false}, {1, 2, "Across", false}, {2, 1, "Return across", false},
				{1, 1, "Wait", false}, {1, 1, "Inspect\nagain", false},
				{1, 3, "Finish", false}, {2, 3, "Complete", false},
			})
		}, false},
		{"multiple initials and shortest distance", func(t *testing.T) *statechart.Result {
			return svg613Chart(t, []string{"Z start", "B", "C", "A end"}, []int{0, 3}, []svg613Edge{
				{0, 1, "First", false}, {1, 2, "Second", false}, {2, 3, "Third", false},
				{3, 2, "Shorter", false},
			})
		}, false},
		{"hidden shortcut does not determine rank", func(t *testing.T) *statechart.Result {
			return svg613Chart(t, []string{"Z start", "B", "C", "A end"}, []int{0}, []svg613Edge{
				{0, 1, "First", false}, {1, 2, "Second", false}, {2, 3, "Third", false},
				{0, 3, "Hidden shortcut", true},
			})
		}, false},
		{"608 capacity shape", func(t *testing.T) *statechart.Result {
			return svg613Chart(t, capacity, []int{0}, capacityEdges)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.make(t)
			n := svg613Render(t, c)
			svg613Geometry(t, c, n)
			if tc.uncrossed {
				svg613NoCrossings(t, n.groups("data-from"))
			}
			svg613Layers(t, c, n)
		})
	}
}

func svg613Render(t *testing.T, c *statechart.Result) *svg608Node {
	t.Helper()
	before := string(svg608JSON(t, c))
	want, n := svg608Render(t, c)
	for range 3 {
		got, err := c.SVG()
		if err != nil || got != want {
			t.Fatalf("layout is not deterministic: %v", err)
		}
	}
	if string(svg608JSON(t, c)) != before {
		t.Fatal("layout mutated the checked result")
	}
	metadata := n.all("metadata")
	if len(metadata) != 1 || metadata[0].attrs["id"] != "statechart-data" || metadata[0].text != before {
		t.Fatal("layout lost the exact inert result metadata")
	}
	svg608Topology(t, c, n)
	return n
}

func svg613Overlaps(a, b svg608Box) bool {
	return a.x0 < b.x1-0.01 && b.x0 < a.x1-0.01 && a.y0 < b.y1-0.01 && b.y0 < a.y1-0.01
}

func svg613Inside(a, b svg608Box) bool {
	return a.x0 >= b.x0-0.01 && a.y0 >= b.y0-0.01 && a.x1 <= b.x1+0.01 && a.y1 <= b.y1+0.01
}

func svg613ViewBox(t *testing.T, n *svg608Node) svg608Box {
	t.Helper()
	v := strings.Fields(n.attrs["viewBox"])
	if len(v) != 4 {
		t.Fatal("missing viewBox")
	}
	x, y := svg608Number(t, v[0]), svg608Number(t, v[1])
	w, h := svg608Number(t, v[2]), svg608Number(t, v[3])
	if w <= 0 || h <= 0 {
		t.Fatal("nonpositive viewBox")
	}
	return svg608Box{x, y, x + w, y + h}
}

func svg613StateBoxes(t *testing.T, n *svg608Node) map[string]svg608Box {
	t.Helper()
	out := map[string]svg608Box{}
	for _, g := range n.groups("data-state-id") {
		out[g.attrs["data-state-id"]] = svg608Rect(t, g)
	}
	return out
}

func svg613Layers(t *testing.T, c *statechart.Result, n *svg608Node) {
	t.Helper()
	distance := map[string]int{}
	queue := slices.Clone(c.Initial)
	for _, id := range c.Initial {
		distance[id] = 0
	}
	for i := 0; i < len(queue); i++ {
		for _, a := range c.Arrows {
			if a.From != queue[i] {
				continue
			}
			if _, ok := distance[a.To]; !ok {
				distance[a.To] = distance[a.From] + 1
				queue = append(queue, a.To)
			}
		}
	}
	boxes := svg613StateBoxes(t, n)
	byRank := map[int][]svg608Box{}
	for _, s := range c.States {
		rank, ok := distance[s.ID]
		if !ok {
			t.Fatalf("fixture state %s is unreachable", s.ID)
		}
		byRank[rank] = append(byRank[rank], boxes[s.ID])
	}
	previousRight := math.Inf(-1)
	for rank := 0; rank < len(byRank); rank++ {
		left, right := math.Inf(1), math.Inf(-1)
		commonLeft, commonRight := math.Inf(-1), math.Inf(1)
		for _, b := range byRank[rank] {
			left, right = min(left, b.x0), max(right, b.x1)
			commonLeft, commonRight = max(commonLeft, b.x0), min(commonRight, b.x1)
		}
		// Left- or centre-aligned boxes both form a column. Different ranks
		// must have disjoint horizontal bands in shortest-distance order.
		if commonLeft >= commonRight {
			t.Fatalf("states at distance %d do not occupy a common column", rank)
		}
		if left <= previousRight {
			t.Fatalf("distance %d column starts at %g, not right of previous column ending at %g", rank, left, previousRight)
		}
		previousRight = right
	}
}

func svg613Geometry(t *testing.T, c *statechart.Result, n *svg608Node) {
	t.Helper()
	view := svg613ViewBox(t, n)
	states := svg613StateBoxes(t, n)
	var reserved []svg608Box
	var names []string
	add := func(b svg608Box, name string) {
		t.Helper()
		if !svg613Inside(b, view) {
			t.Fatalf("%s escapes viewBox", name)
		}
		for i, old := range reserved {
			if svg613Overlaps(b, old) {
				t.Fatalf("%s overlaps %s", name, names[i])
			}
		}
		reserved, names = append(reserved, b), append(names, name)
	}
	for _, s := range c.States {
		add(states[s.ID], "state "+s.Label)
	}
	type route struct {
		from, to string
		points   [][2]float64
	}
	var routes []route
	for _, g := range n.groups("data-from") {
		label := svg608Rect(t, g)
		add(label, "arrow label "+g.attrs["data-label"])
		for _, span := range g.all("tspan") {
			if !svg613Inside(svg613TextBox(t, span), label) {
				t.Fatalf("arrow text does not fit its label background: %q", span.text)
			}
		}
		p := svg608DirectedPath(t, g)
		start, end := svg608Endpoints(t, p)
		from, to := g.attrs["data-from"], g.attrs["data-to"]
		if svg608DistanceToOutline(start, states[from]) > 12 || svg608DistanceToOutline(end, states[to]) > 12 {
			t.Fatalf("route %q is detached from its state outlines", g.attrs["data-label"])
		}
		points := svg613Path(t, p)
		nearLabel := false
		expanded := svg608Box{label.x0 - 24, label.y0 - 24, label.x1 + 24, label.y1 + 24}
		for i, point := range points {
			if !svg613Inside(svg608Box{point[0], point[1], point[0], point[1]}, view) {
				t.Fatal("arrow route escapes viewBox")
			}
			if i > 0 && svg613SegmentBox(points[i-1], point, expanded) {
				nearLabel = true
			}
			if states[from].x1 < states[to].x0 &&
				(point[0] < states[from].x1-12 || point[0] > states[to].x0+12) {
				t.Fatalf("forward route %q leaves the space between its columns", g.attrs["data-label"])
			}
		}
		if !nearLabel {
			t.Fatalf("label %q is not on or beside its own route", g.attrs["data-label"])
		}
		for _, old := range routes {
			if (old.from == from && old.to == to || old.from == to && old.to == from) &&
				svg613SameRoute(old.points, points) {
				t.Fatal("parallel, reverse or self-loop arrows coincide geometrically")
			}
		}
		routes = append(routes, route{from, to, points})
	}
	for _, g := range n.groups("data-initial") {
		_, end := svg608Endpoints(t, svg608DirectedPath(t, g))
		if svg608DistanceToOutline(end, states[g.attrs["data-initial"]]) > 12 {
			t.Fatal("initial arrow does not meet its state")
		}
	}
}

func TestSVG613OrdersLayersToReduceCrossings(t *testing.T) {
	// Lexical state order puts A/B and C/D in the opposite branch order.
	// Either column may be reordered; no particular tie breaker is required.
	c := svg613Chart(t, []string{"Start", "A", "B", "C", "D"}, []int{0}, []svg613Edge{
		{0, 1, "Choose A", false}, {0, 2, "Choose B", false},
		{1, 4, "A to D", false}, {2, 3, "B to C", false},
	})
	n := svg613Render(t, c)
	svg613Geometry(t, c, n)
	byLabel := map[string]svg608Box{}
	for _, g := range n.groups("data-state-id") {
		byLabel[g.all("title")[0].text] = svg608Rect(t, g)
	}
	cy := func(name string) float64 { b := byLabel[name]; return (b.y0 + b.y1) / 2 }
	if (cy("A")-cy("B"))*(cy("D")-cy("C")) <= 0 {
		t.Error("independent branches reverse vertical order between adjacent columns")
	}
	var branches []*svg608Node
	for _, g := range n.groups("data-from") {
		if g.attrs["data-label"] == "A to D" || g.attrs["data-label"] == "B to C" {
			branches = append(branches, g)
		}
	}
	svg613NoCrossings(t, branches)
	svg613Layers(t, c, n)
}

func TestSVG613StateBoxesFollowTheirOwnText(t *testing.T) {
	labels := []string{"Start", "Work", "Done"}
	edges := []svg613Edge{{0, 1, "Begin", false}, {1, 2, "Finish", false}}
	sparse := svg613Chart(t, labels, []int{0}, edges)
	denseEdges := slices.Clone(edges)
	for i := range 12 {
		denseEdges = append(denseEdges, svg613Edge{0, 1, fmt.Sprintf("Retry %d\nand continue", i), false},
			svg613Edge{1, 1, fmt.Sprintf("Inspect %d", i), false}, svg613Edge{2, 0, fmt.Sprintf("Reset %d", i), false})
	}
	dense := svg613Chart(t, labels, []int{0}, denseEdges)
	boxes := func(c *statechart.Result) map[string]svg608Box {
		n := svg613Render(t, c)
		svg613Geometry(t, c, n)
		out := map[string]svg608Box{}
		for _, g := range n.groups("data-state-id") {
			out[g.all("title")[0].text] = svg608Rect(t, g)
		}
		return out
	}
	small, busy := boxes(sparse), boxes(dense)
	sameSize := func(a, b svg608Box) bool {
		return math.Abs((a.x1-a.x0)-(b.x1-b.x0)) < 0.01 && math.Abs((a.y1-a.y0)-(b.y1-b.y0)) < 0.01
	}
	for _, label := range labels {
		if !sameSize(small[label], busy[label]) {
			t.Errorf("state %q grows when only its incident arrows change", label)
		}
	}
	// Modify a detached result so identifiers, ordering and displayed state
	// numbers stay the same. Only one state's reader label has changed.
	long := svg613Chart(t, labels, []int{0}, edges)
	longLabel := "Work\n" + strings.Repeat("W", 100)
	for i := range long.States {
		if long.States[i].Label == "Work" {
			long.States[i].Label = longLabel
		}
	}
	larger := boxes(long)
	if !sameSize(small["Start"], larger["Start"]) || !sameSize(small["Done"], larger["Done"]) {
		t.Error("a long label enlarges unrelated state boxes")
	}
	a, b := small["Work"], larger[longLabel]
	if b.x1-b.x0 <= a.x1-a.x0 && b.y1-b.y0 <= a.y1-a.y0 {
		t.Error("the longer multiline label receives no additional room")
	}
}

func TestSVG613CompactCompleteReport(t *testing.T) {
	c := svg608Chart(t, "Ready", "Working", "Begin")
	n := svg613Render(t, c)
	svg613Geometry(t, c, n)
	var report *svg608Node
	for _, g := range n.all("g") {
		if g.attrs["id"] == "chart-report" {
			report = g
		}
	}
	if report == nil {
		t.Fatal("missing chart-report group")
	}
	visible := svg608Visible(report)
	flat := strings.Join(strings.Fields(visible), " ")
	require := func(s string) {
		t.Helper()
		if s != "" && !strings.Contains(flat, strings.Join(strings.Fields(s), " ")) {
			t.Errorf("visible report omits %q", s)
		}
	}
	for k, v := range c.Report.Source.Config.Constants {
		require(k)
		require(v)
	}
	for _, r := range c.Report.Rules {
		require(r.Name)
		require(r.Says)
		require(r.SHA256)
		require(r.Expect)
	}
	for _, s := range c.States {
		require(s.Label)
		for _, values := range [][]string{s.Members, s.InitialMembers, s.ViolatedRules, s.UnavailableActions} {
			for _, v := range values {
				require(v)
			}
		}
	}
	for _, arrows := range [][]statechart.Arrow{c.Arrows, c.Report.Hidden} {
		for _, a := range arrows {
			require(a.Label)
			for _, r := range a.HidingRules {
				require(r)
			}
			for _, step := range a.Members {
				require(step.From)
				require(step.Action)
				require(step.To)
			}
		}
	}
	for k, meaning := range c.Report.CountMeanings {
		require(k)
		require(meaning)
		value := fmt.Sprint(c.Report.Counts[k])
		if !regexp.MustCompile(regexp.QuoteMeta(k) + "\\s*[:=]\\s*" + value + "\\b").MatchString(flat) {
			t.Errorf("report omits the value of %s", k)
		}
	}
	source := c.Report.Source
	for _, s := range []string{source.Module, source.TLC, source.Tool.Image, source.Tool.JarSHA256, source.Config.Specification} {
		require(s)
	}
	for _, values := range [][]string{source.Config.Invariants, source.Config.Properties} {
		for _, s := range values {
			require(s)
		}
	}
	for _, trace := range c.Report.Temporal {
		require(trace.Module)
		require(trace.TLC)
		require(trace.Check)
		require(trace.Violated)
		for _, s := range trace.States {
			require(s.Action)
			for k, v := range s.TLA {
				require(k)
				require(v)
			}
			for _, v := range s.Changed {
				require(v)
			}
			require(string(svg608JSON(t, s.Vars)))
		}
	}
	for _, word := range []string{"bounded", "proof", "unavailable", "hidden", "temporal", "trace", "advisory"} {
		if !strings.Contains(strings.ToLower(visible), word) {
			t.Errorf("missing report qualification %q", word)
		}
	}
	var occupied []svg608Box
	for _, g := range n.groups("data-state-id") {
		occupied = append(occupied, svg608Rect(t, g))
	}
	for _, g := range n.groups("data-from") {
		occupied = append(occupied, svg608Rect(t, g))
	}
	bounds := svg608Box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	view := svg613ViewBox(t, n)
	spans := report.all("tspan")
	if len(spans) == 0 {
		t.Fatal("report has no positioned visible text")
	}
	for _, span := range spans {
		if strings.TrimSpace(span.text) == "" {
			continue
		}
		b := svg613TextBox(t, span)
		if !svg613Inside(b, view) {
			t.Fatal("report text escapes viewBox")
		}
		for _, old := range occupied {
			if svg613Overlaps(b, old) {
				t.Fatalf("report text overlaps drawing or another report line: %q", span.text)
			}
		}
		occupied = append(occupied, b)
		bounds.x0, bounds.y0 = min(bounds.x0, b.x0), min(bounds.y0, b.y0)
		bounds.x1, bounds.y1 = max(bounds.x1, b.x1), max(bounds.y1, b.y1)
	}
	if bounds.x1-bounds.x0 > 4800 || bounds.y1-bounds.y0 > 2400 {
		t.Fatalf("four-state report is not compact: %g by %g; want at most 4800 by 2400", bounds.x1-bounds.x0, bounds.y1-bounds.y0)
	}
}

func TestSVG613CountGrammar(t *testing.T) {
	c := svg613Chart(t, []string{"Ready", "Working", "Working"}, []int{0}, []svg613Edge{
		{0, 1, "Begin", false}, {1, 2, "Wait", false}, {2, 2, "Wait", false},
	})
	n := svg613Render(t, c)
	byID := map[string]*svg608Node{}
	for _, g := range n.groups("data-state-id") {
		byID[g.attrs["data-state-id"]] = g
	}
	for _, s := range c.States {
		word := "states"
		if len(s.Members) == 1 {
			word = "state"
		}
		want := fmt.Sprintf("%d concrete %s", len(s.Members), word)
		if !regexp.MustCompile("\\b" + regexp.QuoteMeta(want) + "\\b").MatchString(svg608Visible(byID[s.ID])) {
			t.Errorf("state caption lacks %q", want)
		}
	}
	display := svg608Visible(n)
	for _, desc := range n.all("desc") {
		display += "\n" + desc.text
	}
	if regexp.MustCompile("\\b1 concrete (states|(?:step )?occurrences)\\b").MatchString(display) {
		t.Error("generated singular count has a plural noun")
	}
	if !regexp.MustCompile("\\b1 concrete (?:step )?occurrence\\b").MatchString(display) {
		t.Error("single concrete step has no singular occurrence caption")
	}
}

// The existing SVG tests require positioned tspans and reserve at least half
// an em per rune. Resolve presentation attributes and transforms so reducing
// the font or scaling down the report cannot masquerade as compact layout.
func svg613TextBox(t *testing.T, span *svg608Node) svg608Box {
	t.Helper()
	font := 14.0
	anchor := "start"
	for p := span; p != nil; p = p.parent {
		if s := p.attrs["font-size"]; s != "" {
			font = svg608Number(t, strings.TrimSuffix(s, "px"))
			break
		}
	}
	for p := span; p != nil; p = p.parent {
		if s := p.attrs["text-anchor"]; s != "" {
			anchor = s
			break
		}
	}
	x, y := svg608Number(t, span.attrs["x"]), svg608Number(t, span.attrs["y"])
	width := 0.5 * font * float64(utf8.RuneCountInString(span.text))
	if anchor == "middle" {
		x -= width / 2
	} else if anchor == "end" {
		x -= width
	}
	m := svg608Transform(t, span)
	if font*min(math.Hypot(m[0], m[1]), math.Hypot(m[2], m[3])) < 12 {
		t.Fatal("layout shrinks text below a readable 12 SVG units")
	}
	b := svg608Box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, p := range [][2]float64{{x, y - font}, {x + width, y - font}, {x, y}, {x + width, y}} {
		px, py := m[0]*p[0]+m[2]*p[1]+m[4], m[1]*p[0]+m[3]*p[1]+m[5]
		b.x0, b.y0, b.x1, b.y1 = min(b.x0, px), min(b.y0, py), max(b.x1, px), max(b.y1, py)
	}
	return b
}

func svg613Cross(a, b [2]float64) float64  { return a[0]*b[1] - a[1]*b[0] }
func svg613Sub(a, b [2]float64) [2]float64 { return [2]float64{a[0] - b[0], a[1] - b[1]} }
func svg613At(a, b [2]float64, u float64) [2]float64 {
	return [2]float64{a[0] + u*(b[0]-a[0]), a[1] + u*(b[1]-a[1])}
}

// Return actual intersections, including the extent and middle of collinear
// overlap. That prevents shared routing lanes from counting as no crossings.
func svg613Intersections(a, b, c, d [2]float64) [][2]float64 {
	r, s, ca := svg613Sub(b, a), svg613Sub(d, c), svg613Sub(c, a)
	den := svg613Cross(r, s)
	if math.Abs(den) > 1e-8 {
		u, v := svg613Cross(ca, s)/den, svg613Cross(ca, r)/den
		if u >= -1e-8 && u <= 1+1e-8 && v >= -1e-8 && v <= 1+1e-8 {
			return [][2]float64{svg613At(a, b, u)}
		}
		return nil
	}
	length2 := r[0]*r[0] + r[1]*r[1]
	if length2 < 1e-12 || math.Abs(svg613Cross(ca, r)) > 1e-6 {
		return nil
	}
	project := func(p [2]float64) float64 {
		v := svg613Sub(p, a)
		return (v[0]*r[0] + v[1]*r[1]) / length2
	}
	u, v := project(c), project(d)
	lo, hi := max(0, min(u, v)), min(1, max(u, v))
	if hi < lo-1e-8 {
		return nil
	}
	return [][2]float64{svg613At(a, b, lo), svg613At(a, b, (lo+hi)/2), svg613At(a, b, hi)}
}

func svg613SegmentBox(a, b [2]float64, box svg608Box) bool {
	inside := func(p [2]float64) bool {
		return p[0] >= box.x0 && p[0] <= box.x1 && p[1] >= box.y0 && p[1] <= box.y1
	}
	if inside(a) || inside(b) {
		return true
	}
	corners := [][2]float64{{box.x0, box.y0}, {box.x1, box.y0}, {box.x1, box.y1}, {box.x0, box.y1}}
	for i, c := range corners {
		if len(svg613Intersections(a, b, c, corners[(i+1)%4])) > 0 {
			return true
		}
	}
	return false
}

func svg613DistanceToRoute(p [2]float64, route [][2]float64) float64 {
	best := math.Inf(1)
	for i := 1; i < len(route); i++ {
		a, b := route[i-1], route[i]
		v, w := svg613Sub(b, a), svg613Sub(p, a)
		length2 := v[0]*v[0] + v[1]*v[1]
		u := 0.0
		if length2 > 0 {
			u = max(0, min(1, (v[0]*w[0]+v[1]*w[1])/length2))
		}
		q := svg613At(a, b, u)
		best = min(best, math.Hypot(p[0]-q[0], p[1]-q[1]))
	}
	return best
}

func svg613SameRoute(a, b [][2]float64) bool {
	for _, p := range a {
		if svg613DistanceToRoute(p, b) > 0.5 {
			return false
		}
	}
	for _, p := range b {
		if svg613DistanceToRoute(p, a) > 0.5 {
			return false
		}
	}
	return true
}

func svg613NoCrossings(t *testing.T, groups []*svg608Node) {
	t.Helper()
	if len(groups) < 2 {
		t.Fatal("crossing fixture has too few routes")
	}
	root := groups[0]
	for root.parent != nil {
		root = root.parent
	}
	states := svg613StateBoxes(t, root)
	for i, a := range groups {
		ap := svg613Path(t, svg608DirectedPath(t, a))
		for _, b := range groups[:i] {
			bp := svg613Path(t, svg608DirectedPath(t, b))
			var shared []svg608Box
			for _, x := range []string{a.attrs["data-from"], a.attrs["data-to"]} {
				if x == b.attrs["data-from"] || x == b.attrs["data-to"] {
					box := states[x]
					shared = append(shared, svg608Box{box.x0 - 12, box.y0 - 12, box.x1 + 12, box.y1 + 12})
				}
			}
			for j := 1; j < len(ap); j++ {
				for k := 1; k < len(bp); k++ {
					for _, p := range svg613Intersections(ap[j-1], ap[j], bp[k-1], bp[k]) {
						atSharedState := false
						for _, box := range shared {
							atSharedState = atSharedState || svg613Inside(svg608Box{p[0], p[1], p[0], p[1]}, box)
						}
						if !atSharedState {
							t.Fatalf("forward arrows %q and %q cross or share a lane at %v",
								a.attrs["data-label"], b.attrs["data-label"], p)
						}
					}
				}
			}
		}
	}
}

// Flatten standard SVG path commands in root coordinates. Straight segments
// remain exact; curves and elliptical arcs are sampled. This permits straight,
// orthogonal and curved routing without prescribing an implementation.
func svg613Path(t *testing.T, p *svg608Node) [][2]float64 {
	t.Helper()
	tokenRE := regexp.MustCompile("[MmLlHhVvCcSsQqTtAaZz]|[-+]?(?:[0-9]*\\.[0-9]+|[0-9]+\\.?[0-9]*)(?:[eE][-+]?[0-9]+)?")
	tokens := tokenRE.FindAllString(p.attrs["d"], -1)
	if strings.Trim(tokenRE.ReplaceAllString(p.attrs["d"], ""), " ,\t\r\n") != "" {
		t.Fatal("invalid SVG path syntax")
	}
	arity := map[byte]int{'M': 2, 'L': 2, 'H': 1, 'V': 1, 'C': 6, 'S': 4, 'Q': 4, 'T': 2, 'A': 7}
	var cmd, previous byte
	var pos, sub, control [2]float64
	var points [][2]float64
	appendPoint := func(q [2]float64) {
		if len(points) == 0 || q != points[len(points)-1] {
			points = append(points, q)
		}
	}
	for i := 0; i < len(tokens); {
		if len(tokens[i]) == 1 && strings.ContainsAny(tokens[i], "MmLlHhVvCcSsQqTtAaZz") {
			cmd = tokens[i][0]
			i++
			if cmd == 'Z' || cmd == 'z' {
				appendPoint(sub)
				pos = sub
				previous = 'Z'
				cmd = 0
				continue
			}
		}
		upper := cmd
		relative := cmd >= 'a' && cmd <= 'z'
		if relative {
			upper -= 32
		}
		count := arity[upper]
		if count == 0 || i+count > len(tokens) {
			t.Fatal("incomplete SVG path command")
		}
		v := make([]float64, count)
		for j := range count {
			v[j] = svg608Number(t, tokens[i+j])
		}
		i += count
		xy := func(j int) [2]float64 {
			q := [2]float64{v[j], v[j+1]}
			if relative {
				q[0] += pos[0]
				q[1] += pos[1]
			}
			return q
		}
		to := pos
		switch upper {
		case 'M', 'L':
			to = xy(0)
			if upper == 'M' {
				sub = to
			}
			appendPoint(to)
		case 'H':
			to[0] = v[0]
			if relative {
				to[0] += pos[0]
			}
			appendPoint(to)
		case 'V':
			to[1] = v[0]
			if relative {
				to[1] += pos[1]
			}
			appendPoint(to)
		case 'C', 'S':
			c1, c2 := pos, pos
			if upper == 'C' {
				c1, c2, to = xy(0), xy(2), xy(4)
			} else {
				if previous == 'C' || previous == 'S' {
					c1 = [2]float64{2*pos[0] - control[0], 2*pos[1] - control[1]}
				}
				c2, to = xy(0), xy(2)
			}
			for step := 1; step <= 64; step++ {
				u := float64(step) / 64
				a, b, c := svg613At(pos, c1, u), svg613At(c1, c2, u), svg613At(c2, to, u)
				appendPoint(svg613At(svg613At(a, b, u), svg613At(b, c, u), u))
			}
			control = c2
		case 'Q', 'T':
			c1 := pos
			if upper == 'Q' {
				c1, to = xy(0), xy(2)
			} else {
				if previous == 'Q' || previous == 'T' {
					c1 = [2]float64{2*pos[0] - control[0], 2*pos[1] - control[1]}
				}
				to = xy(0)
			}
			for step := 1; step <= 64; step++ {
				u := float64(step) / 64
				appendPoint(svg613At(svg613At(pos, c1, u), svg613At(c1, to, u), u))
			}
			control = c1
		case 'A':
			to = xy(5)
			for _, q := range svg613Arc(pos, to, v[0], v[1], v[2], v[3] != 0, v[4] != 0) {
				appendPoint(q)
			}
		}
		pos, previous = to, upper
		if upper == 'M' {
			if relative {
				cmd = 'l'
			} else {
				cmd = 'L'
			}
		}
	}
	if len(points) < 2 {
		t.Fatal("empty arrow route")
	}
	m := svg608Transform(t, p)
	for i, q := range points {
		points[i] = [2]float64{m[0]*q[0] + m[2]*q[1] + m[4], m[1]*q[0] + m[3]*q[1] + m[5]}
	}
	return points
}

func svg613Arc(from, to [2]float64, rx, ry, degrees float64, large, sweep bool) [][2]float64 {
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 || from == to {
		return [][2]float64{to}
	}
	phi := degrees * math.Pi / 180
	cs, sn := math.Cos(phi), math.Sin(phi)
	dx, dy := (from[0]-to[0])/2, (from[1]-to[1])/2
	x, y := cs*dx+sn*dy, -sn*dx+cs*dy
	lambda := x*x/(rx*rx) + y*y/(ry*ry)
	if lambda > 1 {
		rx *= math.Sqrt(lambda)
		ry *= math.Sqrt(lambda)
	}
	factor := math.Sqrt(max(0, (rx*rx*ry*ry-rx*rx*y*y-ry*ry*x*x)/(rx*rx*y*y+ry*ry*x*x)))
	if large == sweep {
		factor = -factor
	}
	cx, cy := factor*rx*y/ry, -factor*ry*x/rx
	start := math.Atan2((y-cy)/ry, (x-cx)/rx)
	end := math.Atan2((-y-cy)/ry, (-x-cx)/rx)
	delta := end - start
	if sweep && delta < 0 {
		delta += 2 * math.Pi
	}
	if !sweep && delta > 0 {
		delta -= 2 * math.Pi
	}
	center := [2]float64{cs*cx - sn*cy + (from[0]+to[0])/2, sn*cx + cs*cy + (from[1]+to[1])/2}
	var out [][2]float64
	for i := 1; i <= 96; i++ {
		angle := start + delta*float64(i)/96
		x, y := rx*math.Cos(angle), ry*math.Sin(angle)
		out = append(out, [2]float64{center[0] + cs*x - sn*y, center[1] + sn*x + cs*y})
	}
	out[len(out)-1] = to
	return out
}
