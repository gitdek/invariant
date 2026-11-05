package statechart_test

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

// Public renderer contract: (*Result).SVG() (string, error). The caller owns
// evidence checking; rendering does not authenticate a hand-built Result.
// Errors return an empty string. The receiver is read-only, including maps.
//
// The inspectable SVG contract uses one g[data-state-id] per State, one
// g[data-from][data-to][data-label] per visible Arrow, and one g[data-initial]
// per initial chart ID. State and arrow groups have a title containing their
// exact reader label and visible SVG text for it. Nodes have rectangular
// outlines; arrows and initial indicators have paths with local arrowheads.
// metadata#statechart-data contains the full Result as inert JSON text.
// Root title/desc are linked by aria-labelledby/aria-describedby and role=img.
// Styling uses SVG presentation attributes, not executable or resource-bearing
// markup. These semantic hooks are for downstream inspection and accessibility;
// no coordinates, ordering, colors, font family or routing algorithm are pinned.
//
// The interface assertion keeps the baseline compilable: absence of this new
// renderer is a real test failure, rather than an unrelated compiler failure.
type svg608Renderer interface{ SVG() (string, error) }

func svg608API(t *testing.T, c *statechart.Result) svg608Renderer {
	t.Helper()
	r, ok := any(c).(svg608Renderer)
	if !ok {
		t.Fatal("checked statecharts do not expose SVG() (string, error)")
	}
	return r
}

func svg608JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func svg608Chart(t *testing.T, ready, work, advance string) *statechart.Result {
	t.Helper()
	in := statechart.Input{
		Graph: tlc.GraphResult{
			Graph: tlc.Graph{
				States: []tlc.GraphState{
					{ID: "a", Label: `phase = "ready"`, Values: map[string]string{"phase": `"ready"`}},
					{ID: "b", Label: `phase = "work"`, Values: map[string]string{"phase": `"work"`}},
					{ID: "c", Label: `phase = "work"`, Values: map[string]string{"phase": `"work"`}},
					{ID: "d", Label: `phase = "done"`, Values: map[string]string{"phase": `"done"`}},
				},
				Initial: []string{"a", "b", "d"},
				Steps: []tlc.GraphStep{
					{From: "a", Action: "Start", To: "b"}, {From: "a", Action: "Start", To: "b"},
					{From: "a", Action: "Retry", To: "b"}, {From: "b", Action: "Reset", To: "a"},
					{From: "b", Action: "Wait", To: "b"}, {From: "b", Action: "Inspect", To: "b"},
					{From: "b", Action: "Fail", To: "c"}, {From: "c", Action: "Stay", To: "c"},
					{From: "c", Action: "Finish", To: "d"}, {From: "a", Action: "Poll", To: "a"},
				},
			},
			Module: "Renderer608", Config: tlc.Config{Specification: "Spec", Constants: map[string]string{"Limit": "4"}},
			Tool: tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)},
			Run:  tlc.Result{Outcome: tlc.Passed, Version: "TLC2 Version 2.19", Depth: 4},
		},
		Lock: project.Lock{Bounds: map[string]string{"Limit": "4"}, Statements: []project.Statement{
			{Name: "Spec", Kind: project.Spec, Says: "A worker may start and finish.", SHA256: strings.Repeat("c", 64)},
			{Name: "Safe", Kind: project.Invariant, Says: "Keep  every <result> & its owner.\nExactly as ratified.", SHA256: strings.Repeat("d", 64)},
			{Name: "EventuallyDone", Kind: project.Property, Says: "Every started job eventually finishes.", SHA256: strings.Repeat("e", 64)},
		}},
		Invariants: map[string]map[string]bool{"a": {"Safe": true}, "b": {"Safe": true}, "c": {"Safe": false}, "d": {"Safe": true}},
	}
	in.Graph.StateCount, in.Graph.StepCount = len(in.Graph.Graph.States), len(in.Graph.Graph.Steps)
	in.Graph.Run.DistinctStates = int64(in.Graph.StateCount)
	in.Graph.Run.StatesGenerated = int64(in.Graph.StepCount + len(in.Graph.Graph.Initial) + 1)
	in.Temporal = []tlc.TraceFile{{Module: in.Graph.Module, TLC: in.Graph.Run.Version, Check: "EventuallyDone", Violated: "EventuallyDone", Loop: 2, States: []tlc.TraceState{
		{Index: 1, Action: "Initial predicate", TLA: map[string]string{"phase": `"ready"`}, Vars: map[string]any{"phase": "ready"}},
		{Index: 2, Action: "Start", TLA: map[string]string{"phase": `"work"`}, Vars: map[string]any{"phase": "work"}},
	}}}
	view := map[string]any{"version": 1,
		"state": []any{
			map[string]any{"id": "ready", "when": map[string]any{"eq": []any{map[string]any{"var": "phase"}, `"ready"`}}, "label": ready},
			map[string]any{"id": "work", "when": map[string]any{"eq": []any{map[string]any{"var": "phase"}, `"work"`}}, "label": work},
			map[string]any{"id": "done", "when": true, "label": "Done"},
		},
	}
	var rules []any
	for _, a := range []string{"Start", "Retry", "Reset", "Wait", "Inspect", "Fail", "Stay", "Finish", "Poll"} {
		label := a
		if a == "Start" {
			label = advance
		}
		if a == "Poll" {
			label = "Hidden polling"
		}
		rules = append(rules, map[string]any{"id": "rule-" + a, "when": map[string]any{"eq": []any{map[string]any{"action": true}, a}}, "label": label, "hide": a == "Poll"})
	}
	view["step"] = rules
	c, err := statechart.Compute(t.Context(), in, svg608JSON(t, view))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type svg608Node struct {
	name     string
	attrs    map[string]string
	children []*svg608Node
	text     string
	parent   *svg608Node
}

func (n *svg608Node) all(name string) []*svg608Node {
	var out []*svg608Node
	if n.name == name {
		out = append(out, n)
	}
	for _, c := range n.children {
		out = append(out, c.all(name)...)
	}
	return out
}

func (n *svg608Node) groups(attr string) []*svg608Node {
	var out []*svg608Node
	for _, g := range n.all("g") {
		if _, ok := g.attrs[attr]; ok {
			out = append(out, g)
		}
	}
	return out
}

func svg608Parse(t *testing.T, s string) *svg608Node {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(s))
	var root *svg608Node
	var stack []*svg608Node
	ids := map[string]bool{}
	refs := []string{}
	allowed := strings.Fields("svg g defs marker path rect circle ellipse line polyline polygon text tspan title desc metadata")
	localURL := regexp.MustCompile(`^url\(#[A-Za-z_][A-Za-z0-9_.:-]*\)$`)
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid SVG XML: %v", err)
		}
		switch v := tok.(type) {
		case xml.Directive:
			t.Fatal("SVG contains a DTD or directive")
		case xml.ProcInst:
			if v.Target != "xml" || root != nil {
				t.Fatalf("active processing instruction %q", v.Target)
			}
		case xml.StartElement:
			if v.Name.Space != "http://www.w3.org/2000/svg" || !slices.Contains(allowed, v.Name.Local) {
				t.Fatalf("unexpected element {%s}%s", v.Name.Space, v.Name.Local)
			}
			n := &svg608Node{name: v.Name.Local, attrs: map[string]string{}}
			for _, a := range v.Attr {
				key := strings.ToLower(a.Name.Local)
				if strings.HasPrefix(key, "on") || key == "href" || key == "src" || key == "style" || key == "base" {
					t.Fatalf("active or resource-bearing attribute %s", a.Name.Local)
				}
				if a.Name.Space != "" && a.Name.Space != "http://www.w3.org/XML/1998/namespace" && a.Name.Space != "xmlns" {
					t.Fatalf("unexpected attribute namespace %q", a.Name.Space)
				}
				if !strings.HasPrefix(key, "data-") && strings.Contains(strings.ToLower(a.Value), "url(") {
					if !localURL.MatchString(a.Value) {
						t.Fatalf("external resource %q", a.Value)
					}
					refs = append(refs, strings.TrimSuffix(strings.TrimPrefix(a.Value, "url(#"), ")"))
				}
				if key == "id" {
					if a.Value == "" || ids[a.Value] {
						t.Fatalf("empty or duplicate SVG id %q", a.Value)
					}
					ids[a.Value] = true
				}
				n.attrs[a.Name.Local] = a.Value
			}
			if len(stack) == 0 {
				if root != nil || n.name != "svg" {
					t.Fatal("expected one SVG root")
				}
				root = n
			} else {
				n.parent = stack[len(stack)-1]
				n.parent.children = append(n.parent.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 && strings.TrimSpace(string(v)) != "" {
				t.Fatal("text outside SVG")
			}
			for _, n := range stack {
				n.text += string(v)
			}
		}
	}
	if root == nil || len(stack) != 0 {
		t.Fatal("missing or incomplete SVG")
	}
	for _, id := range refs {
		if !ids[id] {
			t.Fatalf("unresolved local resource #%s", id)
		}
	}
	return root
}

func svg608Render(t *testing.T, c *statechart.Result) (string, *svg608Node) {
	t.Helper()
	s, err := svg608API(t, c).SVG()
	if err != nil || s == "" {
		t.Fatalf("SVG rendering failed: %v", err)
	}
	return s, svg608Parse(t, s)
}

func svg608Visible(n *svg608Node) string {
	var out []string
	for _, text := range n.all("text") {
		out = append(out, text.text)
	}
	return strings.Join(out, "\n")
}

func svg608Number(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		t.Fatalf("non-finite SVG number %q", s)
	}
	return f
}

var svg608Numbers = regexp.MustCompile(`[-+]?(?:[0-9]*\.[0-9]+|[0-9]+\.?[0-9]*)(?:[eE][-+]?[0-9]+)?`)
var svg608Transforms = regexp.MustCompile(`([A-Za-z]+)\s*\(([^)]*)\)`)

// Resolve node bounds in root coordinates, allowing ordinary affine SVG
// transforms. Geometry assertions concern the image, not a particular layout.
type svg608Matrix [6]float64

func svg608Mul(a, b svg608Matrix) svg608Matrix {
	return svg608Matrix{a[0]*b[0] + a[2]*b[1], a[1]*b[0] + a[3]*b[1], a[0]*b[2] + a[2]*b[3], a[1]*b[2] + a[3]*b[3], a[0]*b[4] + a[2]*b[5] + a[4], a[1]*b[4] + a[3]*b[5] + a[5]}
}
func svg608Transform(t *testing.T, n *svg608Node) svg608Matrix {
	t.Helper()
	m := svg608Matrix{1, 0, 0, 1, 0, 0}
	if n.parent != nil {
		m = svg608Transform(t, n.parent)
	}
	for _, op := range svg608Transforms.FindAllStringSubmatch(n.attrs["transform"], -1) {
		var v []float64
		for _, s := range svg608Numbers.FindAllString(op[2], -1) {
			v = append(v, svg608Number(t, s))
		}
		x := svg608Matrix{1, 0, 0, 1, 0, 0}
		switch {
		case op[1] == "matrix" && len(v) == 6:
			copy(x[:], v)
		case op[1] == "translate" && (len(v) == 1 || len(v) == 2):
			x[4] = v[0]
			if len(v) == 2 {
				x[5] = v[1]
			}
		case op[1] == "scale" && (len(v) == 1 || len(v) == 2):
			x[0], x[3] = v[0], v[0]
			if len(v) == 2 {
				x[3] = v[1]
			}
		case op[1] == "rotate" && (len(v) == 1 || len(v) == 3):
			r := v[0] * math.Pi / 180
			x[0], x[1], x[2], x[3] = math.Cos(r), math.Sin(r), -math.Sin(r), math.Cos(r)
			if len(v) == 3 {
				x[4] = v[1] - x[0]*v[1] - x[2]*v[2]
				x[5] = v[2] - x[1]*v[1] - x[3]*v[2]
			}
		case op[1] == "skewX" && len(v) == 1:
			x[2] = math.Tan(v[0] * math.Pi / 180)
		case op[1] == "skewY" && len(v) == 1:
			x[1] = math.Tan(v[0] * math.Pi / 180)
		default:
			t.Fatalf("invalid SVG transform %q", op[0])
		}
		m = svg608Mul(m, x)
	}
	return m
}

type svg608Box struct{ x0, y0, x1, y1 float64 }

func svg608Rect(t *testing.T, g *svg608Node) svg608Box {
	t.Helper()
	r := g.all("rect")
	if len(r) != 1 {
		t.Fatalf("state has %d rectangular outlines, want one", len(r))
	}
	n := r[0]
	get := func(key string) float64 {
		if n.attrs[key] == "" {
			return 0
		}
		return svg608Number(t, n.attrs[key])
	}
	x, y, w, h := get("x"), get("y"), get("width"), get("height")
	if w <= 0 || h <= 0 {
		t.Fatal("state has no visible area")
	}
	m := svg608Transform(t, n)
	b := svg608Box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, p := range [][2]float64{{x, y}, {x + w, y}, {x, y + h}, {x + w, y + h}} {
		px, py := m[0]*p[0]+m[2]*p[1]+m[4], m[1]*p[0]+m[3]*p[1]+m[5]
		b.x0, b.y0, b.x1, b.y1 = math.Min(b.x0, px), math.Min(b.y0, py), math.Max(b.x1, px), math.Max(b.y1, py)
	}
	return b
}

func svg608Topology(t *testing.T, c *statechart.Result, n *svg608Node) {
	t.Helper()
	states := n.groups("data-state-id")
	if len(states) != len(c.States) {
		t.Fatalf("drawn states=%d want %d", len(states), len(c.States))
	}
	seen := map[string]bool{}
	for _, g := range states {
		id := g.attrs["data-state-id"]
		if seen[id] {
			t.Fatalf("state drawn twice: %s", id)
		}
		seen[id] = true
		idx := slices.IndexFunc(c.States, func(s statechart.State) bool { return s.ID == id })
		if idx < 0 {
			t.Fatalf("invented state %s", id)
		}
		titles := g.all("title")
		if len(titles) != 1 || titles[0].text != c.States[idx].Label {
			t.Fatalf("lost state label %q", c.States[idx].Label)
		}
		if strings.TrimSpace(svg608Visible(g)) == "" {
			t.Fatal("state label only in metadata")
		}
	}
	wantInitial := map[string]bool{}
	for _, id := range c.Initial {
		wantInitial[id] = true
	}
	initial := n.groups("data-initial")
	if len(initial) != len(c.Initial) {
		t.Fatalf("drawn initials=%d want %d", len(initial), len(c.Initial))
	}
	for _, g := range initial {
		id := g.attrs["data-initial"]
		if !wantInitial[id] {
			t.Fatalf("invented or repeated initial %s", id)
		}
		delete(wantInitial, id)
		svg608DirectedPath(t, g)
	}
	type key struct{ from, to, label string }
	want := map[key]int{}
	for _, a := range c.Arrows {
		want[key{a.From, a.To, a.Label}]++
	}
	arrows := n.groups("data-from")
	if len(arrows) != len(c.Arrows) {
		t.Fatalf("drawn arrows=%d want %d", len(arrows), len(c.Arrows))
	}
	for _, g := range arrows {
		k := key{g.attrs["data-from"], g.attrs["data-to"], g.attrs["data-label"]}
		if want[k] != 1 {
			t.Fatalf("invented, hidden or repeated arrow %+v", k)
		}
		delete(want, k)
		titles := g.all("title")
		if len(titles) != 1 || titles[0].text != k.label || strings.TrimSpace(svg608Visible(g)) == "" {
			t.Fatalf("lost arrow label %q", k.label)
		}
		svg608DirectedPath(t, g)
	}
}

func svg608DirectedPath(t *testing.T, g *svg608Node) *svg608Node {
	t.Helper()
	var directed []*svg608Node
	for _, p := range g.all("path") {
		if p.attrs["marker-end"] != "" {
			directed = append(directed, p)
		}
	}
	if len(directed) != 1 || !strings.HasPrefix(directed[0].attrs["marker-end"], "url(#") {
		t.Fatal("missing unambiguous directed path")
	}
	p := directed[0]
	nums := svg608Numbers.FindAllString(p.attrs["d"], -1)
	if len(nums) < 4 {
		t.Fatal("empty arrow geometry")
	}
	values := map[float64]bool{}
	for _, s := range nums {
		values[svg608Number(t, s)] = true
	}
	if len(values) < 2 {
		t.Fatal("degenerate arrow geometry")
	}
	return p
}

// Read the endpoints of SVG's standard path commands. This lets the test
// verify that an arrow really connects the claimed nodes without specifying
// a straight, curved, orthogonal or elliptical routing strategy.
func svg608Endpoints(t *testing.T, p *svg608Node) (start, end [2]float64) {
	t.Helper()
	tokenRE := regexp.MustCompile(`[MmLlHhVvCcSsQqTtAaZz]|[-+]?(?:[0-9]*\.[0-9]+|[0-9]+\.?[0-9]*)(?:[eE][-+]?[0-9]+)?`)
	tokens := tokenRE.FindAllString(p.attrs["d"], -1)
	if strings.Trim(tokenRE.ReplaceAllString(p.attrs["d"], ""), " ,\t\r\n") != "" {
		t.Fatal("invalid SVG path syntax")
	}
	arities := map[byte]int{'M': 2, 'L': 2, 'H': 1, 'V': 1, 'C': 6, 'S': 4, 'Q': 4, 'T': 2, 'A': 7}
	var cmd byte
	var sub [2]float64
	first := true
	for i := 0; i < len(tokens); {
		tok := tokens[i]
		if len(tok) == 1 && strings.ContainsAny(tok, "MmLlHhVvCcSsQqTtAaZz") {
			cmd = tok[0]
			i++
			if cmd == 'Z' || cmd == 'z' {
				end = sub
				cmd = 0
				continue
			}
		}
		upper := cmd
		if upper >= 'a' && upper <= 'z' {
			upper -= 32
		}
		n := arities[upper]
		if n == 0 || i+n > len(tokens) {
			t.Fatal("incomplete SVG path command")
		}
		v := make([]float64, n)
		for k := range n {
			v[k] = svg608Number(t, tokens[i+k])
		}
		i += n
		to := end
		switch upper {
		case 'H':
			to[0] = v[0]
			if cmd == 'h' {
				to[0] += end[0]
			}
		case 'V':
			to[1] = v[0]
			if cmd == 'v' {
				to[1] += end[1]
			}
		default:
			to = [2]float64{v[n-2], v[n-1]}
			if cmd >= 'a' && cmd <= 'z' {
				to[0] += end[0]
				to[1] += end[1]
			}
		}
		if first {
			if upper != 'M' {
				t.Fatal("SVG path does not start with moveto")
			}
			start = to
			first = false
		}
		end = to
		if upper == 'M' {
			sub = to
			if cmd == 'M' {
				cmd = 'L'
			} else {
				cmd = 'l'
			}
		}
	}
	if first {
		t.Fatal("empty SVG path")
	}
	m := svg608Transform(t, p)
	transform := func(v [2]float64) [2]float64 {
		return [2]float64{m[0]*v[0] + m[2]*v[1] + m[4], m[1]*v[0] + m[3]*v[1] + m[5]}
	}
	return transform(start), transform(end)
}

func svg608DistanceToOutline(p [2]float64, b svg608Box) float64 {
	x, y := p[0], p[1]
	if x >= b.x0 && x <= b.x1 && y >= b.y0 && y <= b.y1 {
		return math.Min(math.Min(x-b.x0, b.x1-x), math.Min(y-b.y0, b.y1-y))
	}
	dx := math.Max(math.Max(b.x0-x, 0), x-b.x1)
	dy := math.Max(math.Max(b.y0-y, 0), y-b.y1)
	return math.Hypot(dx, dy)
}

func TestSVG608TopologyAndGeometry(t *testing.T) {
	c := svg608Chart(t, "Ready", "Working", "Begin")
	_, n := svg608Render(t, c)
	svg608Topology(t, c, n)
	v := strings.Fields(n.attrs["viewBox"])
	if len(v) != 4 {
		t.Fatal("missing usable viewBox")
	}
	x, y, w, h := svg608Number(t, v[0]), svg608Number(t, v[1]), svg608Number(t, v[2]), svg608Number(t, v[3])
	if w <= 0 || h <= 0 {
		t.Fatal("nonpositive viewBox")
	}
	boxes := map[string]svg608Box{}
	for _, g := range n.groups("data-state-id") {
		b := svg608Rect(t, g)
		if b.x0 < x || b.y0 < y || b.x1 > x+w || b.y1 > y+h {
			t.Fatal("state clipped by viewBox")
		}
		for _, old := range boxes {
			if b.x0 < old.x1 && old.x0 < b.x1 && b.y0 < old.y1 && old.y0 < b.y1 {
				t.Fatal("state outlines overlap")
			}
		}
		boxes[g.attrs["data-state-id"]] = b
	}
	paths := map[string]map[string]bool{}
	for _, g := range n.groups("data-from") {
		p := svg608DirectedPath(t, g)
		start, end := svg608Endpoints(t, p)
		// Permit the small gap normally reserved for the arrowhead/stroke.
		if svg608DistanceToOutline(start, boxes[g.attrs["data-from"]]) > 12 || svg608DistanceToOutline(end, boxes[g.attrs["data-to"]]) > 12 {
			t.Fatal("drawn arrow does not connect its checked source and target")
		}
		ends := []string{g.attrs["data-from"], g.attrs["data-to"]}
		slices.Sort(ends)
		pair := strings.Join(ends, "\x00")
		if paths[pair] == nil {
			paths[pair] = map[string]bool{}
		}
		// Exact duplicate routes are not distinct parallel/return arrows.
		route := strings.Join(strings.Fields(p.attrs["d"]), " ") + "/" + fmt.Sprint(svg608Transform(t, p))
		if paths[pair][route] {
			t.Fatal("parallel, reverse or loop paths coincide")
		}
		paths[pair][route] = true
		if ends[0] == ends[1] && len(svg608Numbers.FindAllString(p.attrs["d"], -1)) < 6 {
			t.Fatal("self-loop has no route around its state")
		}
	}
	for _, g := range n.groups("data-initial") {
		_, end := svg608Endpoints(t, svg608DirectedPath(t, g))
		if svg608DistanceToOutline(end, boxes[g.attrs["data-initial"]]) > 12 {
			t.Fatal("initial arrow points at the wrong state")
		}
	}
}

func TestSVG608LiteralLabelsAndAccessibility(t *testing.T) {
	short := svg608Chart(t, "Ready", "Working", "Begin")
	_, small := svg608Render(t, short)
	const payload = "<script>alert('x')</script> & \"quoted\" #quot; <foreignObject onload='x'> https://invalid.example/a.svg javascript:alert(1)"
	label := "準備 café & <ready>\n" + payload + "\n" + strings.Repeat("UnbrokenLabel", 24)
	arrow := "Advance <&>\n" + strings.Repeat("a long reader action ", 12)
	c := svg608Chart(t, label, "Working", arrow)
	_, n := svg608Render(t, c)
	svg608Topology(t, c, n)
	if n.attrs["role"] != "img" {
		t.Fatal("SVG lacks image role")
	}
	for _, pair := range [][2]string{{"aria-labelledby", "title"}, {"aria-describedby", "desc"}} {
		ids := strings.Fields(n.attrs[pair[0]])
		if len(ids) == 0 {
			t.Fatalf("missing %s", pair[0])
		}
		for _, id := range ids {
			found := false
			for _, node := range n.all(pair[1]) {
				found = found || node.attrs["id"] == id && strings.TrimSpace(node.text) != ""
			}
			if !found {
				t.Fatalf("broken accessible %s reference %q", pair[1], id)
			}
		}
	}
	var longNode *svg608Node
	for _, g := range n.groups("data-state-id") {
		if g.all("title")[0].text == label {
			longNode = g
		}
	}
	if longNode == nil {
		t.Fatal("literal title lost")
	}
	for _, line := range strings.Split(label, "\n") {
		if !strings.Contains(strings.Join(strings.Fields(svg608Visible(longNode)), " "), strings.Join(strings.Fields(line), " ")) {
			t.Fatalf("visible label lost %q", line)
		}
	}
	spans := longNode.all("tspan")
	if len(spans) < 3 {
		t.Fatal("multiline text has no separate SVG lines")
	}
	positioned := 0
	for _, s := range spans {
		if s.attrs["y"] != "" || s.attrs["dy"] != "" {
			positioned++
		}
	}
	if positioned < 3 {
		t.Fatal("multiline label lacks line positioning")
	}
	var shortBox svg608Box
	for _, g := range small.groups("data-state-id") {
		if g.all("title")[0].text == "Ready" {
			shortBox = svg608Rect(t, g)
		}
	}
	longBox := svg608Rect(t, longNode)
	if longBox.x1-longBox.x0 <= shortBox.x1-shortBox.x0 && longBox.y1-longBox.y0 <= shortBox.y1-shortBox.y0 {
		t.Fatal("long label gets no additional room")
	}
	for _, g := range n.groups("data-from") {
		if g.attrs["data-label"] == arrow {
			if len(g.all("tspan")) < 2 {
				t.Fatal("arrow label ignores line breaks")
			}
			if !strings.Contains(strings.Join(strings.Fields(svg608Visible(g)), " "), strings.Join(strings.Fields(arrow), " ")) {
				t.Fatal("long arrow text truncated")
			}
		}
	}
}

func TestSVG608ReportAndMetadata(t *testing.T) {
	c := svg608Chart(t, "Ready", "Working", "Begin")
	_, n := svg608Render(t, c)
	var metadata []*svg608Node
	for _, m := range n.all("metadata") {
		if m.attrs["id"] == "statechart-data" {
			metadata = append(metadata, m)
		}
	}
	if len(metadata) != 1 {
		t.Fatal("missing full checked chart metadata")
	}
	var saved statechart.Result
	if err := json.Unmarshal([]byte(metadata[0].text), &saved); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&saved, c) {
		t.Fatal("SVG dropped or altered checked chart data")
	}
	visible := svg608Visible(n)
	flat := strings.Join(strings.Fields(visible), " ")
	for _, rule := range c.Report.Rules {
		if !strings.Contains(flat, strings.Join(strings.Fields(rule.Says), " ")) || !strings.Contains(visible, rule.Name) {
			t.Fatalf("rule wording absent: %s", rule.Name)
		}
	}
	for _, want := range []string{"Limit", "4", "Safe", "Hidden polling", "Poll", "rule-Poll", "Renderer608", "TLC2 Version 2.19"} {
		if !strings.Contains(visible, want) {
			t.Fatalf("report omits %q", want)
		}
	}
	for _, s := range c.States {
		for _, action := range s.UnavailableActions {
			if !strings.Contains(visible, action) {
				t.Fatalf("unavailable action absent: %s", action)
			}
		}
	}
	for key, meaning := range c.Report.CountMeanings {
		if !strings.Contains(flat, strings.Join(strings.Fields(meaning), " ")) {
			t.Fatalf("count qualification absent: %s", key)
		}
	}
	lower := strings.ToLower(visible)
	for _, want := range []string{"bounded", "proof", "unavailable", "hidden", "temporal", "trace", "eventuallydone"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("missing visible qualification %q", want)
		}
	}
	if strings.Contains(lower, "unbounded proof") {
		t.Fatal("renderer added an unbounded proof claim")
	}
}

func TestSVG608SupportedCapacity(t *testing.T) {
	// An ordinary checked chart at capacity: a reachable chain and one loop
	// per state. More than 20 arrows is advice, not permission to omit any.
	in := statechart.Input{Graph: tlc.GraphResult{
		Module: "Capacity608", Config: tlc.Config{Specification: "Spec"},
		Tool: tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)},
		Run:  tlc.Result{Outcome: tlc.Passed, Version: "TLC2 Version 2.19", Depth: 500},
	}, Lock: project.Lock{Statements: []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "Advance or wait.", SHA256: strings.Repeat("c", 64)}}}, Invariants: map[string]map[string]bool{}}
	var states []any
	for i := range 500 {
		id := strconv.Itoa(i)
		in.Graph.Graph.States = append(in.Graph.Graph.States, tlc.GraphState{ID: id, Label: "phase = " + id, Values: map[string]string{"phase": id}})
		in.Invariants[id] = map[string]bool{}
		in.Graph.Graph.Steps = append(in.Graph.Graph.Steps, tlc.GraphStep{From: id, Action: "Wait", To: id})
		if i > 0 {
			in.Graph.Graph.Steps = append(in.Graph.Graph.Steps, tlc.GraphStep{From: strconv.Itoa(i - 1), Action: "Next", To: id})
		}
		states = append(states, map[string]any{"id": "s" + id, "when": map[string]any{"eq": []any{map[string]any{"var": "phase"}, id}}, "label": "Stage " + id})
	}
	in.Graph.Graph.Initial = []string{"0"}
	in.Graph.StateCount, in.Graph.StepCount = 500, 999
	in.Graph.Run.DistinctStates, in.Graph.Run.StatesGenerated = 500, 1000
	view := map[string]any{"version": 1, "state": states, "step": []any{map[string]any{"id": "step", "when": true, "label": "Continue"}}}
	c, err := statechart.Compute(t.Context(), in, svg608JSON(t, view))
	if err != nil {
		t.Fatal(err)
	}
	_, n := svg608Render(t, c)
	svg608Topology(t, c, n)
	visible := strings.ToLower(svg608Visible(n))
	if !strings.Contains(visible, "20") || !strings.Contains(visible, "advis") || !strings.Contains(visible, "999") {
		t.Fatal("over-target chart lost its advisory or actual arrow count")
	}
}

func TestSVG608DeterministicAndReadOnly(t *testing.T) {
	c := svg608Chart(t, "Ready", "Working", "Begin")
	r := svg608API(t, c)
	t.Setenv("PATH", t.TempDir())
	before := string(svg608JSON(t, c))
	want, n := svg608Render(t, c)
	svg608Topology(t, c, n)
	for range 5 {
		got, err := r.SVG()
		if err != nil || got != want {
			t.Fatalf("unstable render: %v", err)
		}
	}
	var wg sync.WaitGroup
	failures := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := r.SVG()
			if err != nil || got != want {
				failures <- fmt.Sprintf("concurrent render changed: %v", err)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if string(svg608JSON(t, c)) != before {
		t.Fatal("render mutated chart or report")
	}
}

func TestSVG608RejectsUnrenderableInput(t *testing.T) {
	svg608API(t, svg608Chart(t, "Ready", "Working", "Begin"))
	for _, tc := range []struct {
		name string
		edit func(*statechart.Result)
	}{
		{"unsupported version", func(c *statechart.Result) { c.Version = 2 }},
		{"empty states", func(c *statechart.Result) { c.States = nil }},
		{"duplicate state", func(c *statechart.Result) { c.States = append(c.States, c.States[0]) }},
		{"unknown endpoint", func(c *statechart.Result) { c.Arrows[0].To = "missing" }},
		{"unknown initial", func(c *statechart.Result) { c.Initial = []string{"missing"} }},
		{"missing initials", func(c *statechart.Result) { c.Initial = nil }},
		{"duplicate initial", func(c *statechart.Result) { c.Initial = append(c.Initial, c.Initial[0]) }},
		{"XML control in label", func(c *statechart.Result) { c.States[0].Label = "x\x00y" }},
		{"invalid UTF8 in arrow", func(c *statechart.Result) { c.Arrows[0].Label = string([]byte{0xff}) }},
		{"XML control in report", func(c *statechart.Result) { c.Report.Rules[0].Says = "x\x01y" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := svg608Chart(t, "Ready", "Working", "Begin")
			tc.edit(c)
			s, err := svg608API(t, c).SVG()
			if err == nil || strings.TrimSpace(err.Error()) == "" || s != "" {
				t.Fatalf("want error without partial SVG, got %q, %v", s, err)
			}
		})
	}
	s, err := svg608API(t, nil).SVG()
	if err == nil || s != "" {
		t.Fatalf("nil chart: %q, %v", s, err)
	}
}
