package statechart_test

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

// A renderer fixture with a forward chain, a skipped layer, a returning
// arrow (which widens the final poster), and a step inside a state. Bounds
// affect the header only. No TLC executable, browser, font or network is used.
func poster785Chart(bounds map[string]string) *statechart.Result {
	c := &statechart.Result{
		Version: 1,
		States: []statechart.State{
			{ID: "ready", Label: "Ready", Members: []string{"a"}, InitialMembers: []string{"a"}},
			{ID: "working", Label: "Working", Members: []string{"b"}},
			{ID: "done", Label: "Done", Members: []string{"c"}},
		},
		Initial: []string{"ready"},
		Report: statechart.Report{
			ArrowTarget: statechart.ArrowTarget,
			Source:      statechart.Source{Module: "Subtitle785", Config: tlc.Config{Specification: "Spec", Constants: bounds}},
			Counts: map[string]int64{
				"tlc_states": 3, "tlc_initial_states": 1, "tlc_steps": 5, "tlc_generated_states": 6,
				"chart_states": 3, "chart_arrows": 5, "chart_arrows_before_hiding": 5, "shown_steps": 5, "hidden_steps": 0,
			},
		},
	}
	for _, s := range []struct{ from, to, label, a, b string }{
		{"ready", "working", "Advance", "a", "b"},
		{"working", "done", "Finish", "b", "c"},
		{"done", "ready", "Return to start", "c", "a"},
		{"working", "working", "Poll", "b", "b"},
		{"ready", "done", "Skip", "a", "c"},
	} {
		c.Arrows = append(c.Arrows, statechart.Arrow{From: s.from, To: s.to, Label: s.label, Count: 1,
			Members: []tlc.GraphStep{{From: s.a, To: s.b, Action: s.label}}})
	}
	return c
}

func poster785Bounds(n int) map[string]string {
	b := map[string]string{}
	for i := n - 1; i >= 0; i-- {
		b[fmt.Sprintf("Bound%02d", i)] = "{one, two, three, four}"
	}
	return b
}

func poster785Render(t *testing.T, c *statechart.Result, animated bool) string {
	t.Helper()
	s, err := c.Poster(statechart.PosterOptions{Title: "subtitle bounds", Animated: animated})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type poster785Node struct {
	name   string
	a      map[string]string
	text   string
	parent *poster785Node
}

func poster785XML(t *testing.T, s string) []*poster785Node {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(s))
	var all, stack []*poster785Node
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid SVG XML: %v", err)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			n := &poster785Node{name: v.Name.Local, a: map[string]string{}}
			for _, a := range v.Attr {
				n.a[a.Name.Local] = a.Value
			}
			if len(stack) > 0 {
				n.parent = stack[len(stack)-1]
			}
			all = append(all, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(v)
			}
		}
	}
	if len(all) == 0 || all[0].name != "svg" {
		t.Fatal("missing SVG root")
	}
	return all
}

func poster785Num(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("invalid SVG number %q: %v", s, err)
	}
	return f
}

var poster785Numbers = regexp.MustCompile(`[-+]?(?:[0-9]*\.[0-9]+|[0-9]+)(?:[eE][-+]?[0-9]+)?`)

// Support a translation of a whole drawing as well as individually moved
// coordinates; neither strategy is part of the acceptance contract.
func poster785Offset(t *testing.T, n *poster785Node) (x, y float64) {
	t.Helper()
	for p := n; p != nil; p = p.parent {
		if tr := p.a["transform"]; tr != "" {
			if !strings.HasPrefix(tr, "translate(") {
				t.Fatalf("unexpected static transform %q", tr)
			}
			v := poster785Numbers.FindAllString(tr, -1)
			if len(v) < 1 || len(v) > 2 {
				t.Fatalf("invalid translation %q", tr)
			}
			x += poster785Num(t, v[0])
			if len(v) == 2 {
				y += poster785Num(t, v[1])
			}
		}
	}
	return
}

func poster785Inherited(n *poster785Node, key string) string {
	for ; n != nil; n = n.parent {
		if v := n.a[key]; v != "" {
			return v
		}
	}
	return ""
}

type poster785Line struct {
	text       string
	x, y, size float64
}

func poster785Subtitle(t *testing.T, nodes []*poster785Node) []poster785Line {
	t.Helper()
	var lines []poster785Line
	var x, y float64
	for _, n := range nodes {
		if n.name != "text" && n.name != "tspan" {
			continue
		}
		// Keep the subtitle's existing readable size. Accept separate text
		// elements or positioned tspans, including cumulative relative dy.
		if poster785Inherited(n, "font-size") != "14.5" {
			continue
		}
		if n.name == "text" {
			x, y = 0, 0
		}
		if v := n.a["x"]; v != "" {
			x = poster785Num(t, v)
		}
		if v := n.a["y"]; v != "" {
			y = poster785Num(t, v)
		}
		if v := n.a["dx"]; v != "" {
			x += poster785Num(t, v)
		}
		if v := n.a["dy"]; v != "" {
			y += poster785Num(t, v)
		}
		if strings.TrimSpace(n.text) == "" {
			continue
		}
		dx, dy := poster785Offset(t, n)
		line := poster785Line{n.text, x + dx, y + dy, 14.5}
		if len(lines) > 0 && math.Abs(lines[len(lines)-1].y-line.y) < 0.1 {
			lines[len(lines)-1].text += line.text
		} else {
			lines = append(lines, line)
		}
		x += float64(utf8.RuneCountInString(n.text)) * 14.5 * 0.53
	}
	if len(lines) == 0 {
		t.Fatal("missing readable subtitle")
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].y < lines[j].y })
	return lines
}

func poster785CheckLines(t *testing.T, nodes []*poster785Node) []poster785Line {
	t.Helper()
	lines := poster785Subtitle(t, nodes)
	w := poster785Num(t, nodes[0].a["width"])
	for i, l := range lines {
		// Match the renderer's font-independent estimate for body text;
		// escaped XML is decoded before counting Unicode characters.
		if l.x < 56 || l.x+float64(utf8.RuneCountInString(l.text))*l.size*0.53 > w-56+0.1 {
			t.Errorf("subtitle line escapes poster padding (width %.0f): %+v", w, l)
		}
		if i == 0 && math.Abs(l.y-104) > 0.1 {
			t.Errorf("first subtitle line moved: %+v", l)
		}
		if i > 0 && l.y-lines[i-1].y < l.size {
			t.Errorf("subtitle lines overlap: %+v", lines)
		}
		if strings.HasPrefix(l.text, " · ") || strings.HasSuffix(l.text, " · ") {
			t.Errorf("stranded item separator: %q", l.text)
		}
	}
	return lines
}

func poster785Items(bounds map[string]string) []string {
	items := []string{"Reader statechart", "3 checked states"}
	var keys []string
	for k := range bounds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		items = append(items, k+" = "+bounds[k])
	}
	return items
}

func poster785LineItems(lines []poster785Line) []string {
	var items []string
	for _, l := range lines {
		items = append(items, strings.Split(l.text, " · ")...)
	}
	return items
}

func TestPosterWrapsWholeSubtitleItems785(t *testing.T) {
	// SHA-256 of complete original poster bytes, captured from repo/ for
	// this fixture. Fitting output is explicitly a compatibility promise.
	for _, legacy := range []struct {
		bounds          map[string]string
		still, animated string
	}{
		{nil, "57b77918e496f95e56cf0b698247c5aec0fa42d9a7aa700eb75047cae5d3a6c5", "3efdaa7af2fe725b3731418150d2fd0b184dd8fe5a71b81d99eab7fbae6d7ac0"},
		{map[string]string{"Limit": "2"}, "e6618c5a38d377fee5b2052d4afd5b183f48decd84e40d547085ac506b710d68", "764dca99fc61db8310e46cfb9d332a8810fb66213468de6d123b85957413c17e"},
		// Fits only after the returning arrow has widened the poster.
		{map[string]string{"Names": "{one, two, three, four}"}, "32ce452f7c6818b2fab0dae27ebfd8a3fe4c929250355edac6d54238566971e4", "5869d9a441f0cd47fa91edf8d952d7fd163a9efedffb6798e76455134731c970"},
	} {
		for _, animated := range []bool{false, true} {
			s := poster785Render(t, poster785Chart(legacy.bounds), animated)
			want := legacy.still
			if animated {
				want = legacy.animated
			}
			if got := fmt.Sprintf("%x", sha256.Sum256([]byte(s))); got != want {
				t.Errorf("fitting poster changed (animated=%t): SHA-256 %s, want %s", animated, got, want)
			}
		}
	}
	for _, count := range []int{7, 24} {
		bounds := poster785Bounds(count)
		c := poster785Chart(bounds)
		var stillLines []poster785Line
		for _, animated := range []bool{false, true} {
			s := poster785Render(t, c, animated)
			lines := poster785CheckLines(t, poster785XML(t, s))
			if len(lines) < 2 {
				t.Errorf("%d bounds remained on one overflowing line", count)
			}
			if got, want := poster785LineItems(lines), poster785Items(bounds); !reflect.DeepEqual(got, want) {
				t.Errorf("bounds were split, lost, shortened or reordered:\ngot %q\nwant %q", got, want)
			}
			if animated && !reflect.DeepEqual(stillLines, lines) {
				t.Error("animated and still subtitle layout differs")
			}
			if !animated {
				stillLines = lines
			}
			if again := poster785Render(t, c, animated); again != s {
				t.Error("wrapping is nondeterministic")
			}
		}
	}
}

var poster785PathTokens = regexp.MustCompile(`[A-Za-z]|[-+]?(?:[0-9]*\.[0-9]+|[0-9]+)(?:[eE][-+]?[0-9]+)?`)

// Compare the rendered route, allowing only an absolute vertical translation.
// Relative curve/orbit dimensions stay fixed. This includes replay paths.
func poster785ShiftPath(t *testing.T, a, b string, ax, ay, bx, by, shift float64) {
	t.Helper()
	x, y := poster785PathTokens.FindAllString(a, -1), poster785PathTokens.FindAllString(b, -1)
	if len(x) != len(y) {
		t.Fatalf("route shape changed: %s -> %s", a, b)
	}
	cmd, arg := byte(0), 0
	for i := range x {
		if len(x[i]) == 1 && strings.ContainsAny(x[i], "MmLlCcSsQqTtHhVvAaZz") {
			if x[i] != y[i] {
				t.Fatalf("route command changed: %s -> %s", a, b)
			}
			cmd, arg = x[i][0], 0
			continue
		}
		av, bv := poster785Num(t, x[i]), poster785Num(t, y[i])
		want := 0.0
		if strings.ContainsRune("MLCSQT", rune(cmd)) {
			if arg%2 == 0 {
				av += ax
				bv += bx
			} else {
				av += ay
				bv += by
				want = shift
			}
		} else if cmd == 'H' {
			av += ax
			bv += bx
		} else if cmd == 'V' {
			av += ay
			bv += by
			want = shift
		}
		if math.Abs(bv-av-want) > 0.2 {
			t.Errorf("route did not translate by %.1f: %s -> %s (coordinate %d)", shift, a, b, i)
			return
		}
		arg++
	}
}

func poster785Body(nodes []*poster785Node) []*poster785Node {
	var body []*poster785Node
	started := false
	for _, n := range nodes {
		if n.a["data-from"] != "" || n.a["data-state-id"] != "" {
			started = true
		}
		if !started {
			continue
		}
		for _, attr := range []string{"x", "y", "cx", "cy", "x1", "y1", "x2", "y2", "d", "path"} {
			if n.a[attr] != "" {
				body = append(body, n)
				break
			}
		}
	}
	return body
}

func poster785Header(nodes []*poster785Node) []*poster785Node {
	var header []*poster785Node
	for _, n := range nodes {
		if n.name == "text" && (n.text == "Subtitle bounds" || n.text == "Complete TLC exploration") ||
			n.name == "rect" && n.a["width"] == "214" && n.a["height"] == "34" ||
			n.name == "circle" && n.a["r"] == "5" && n.a["fill"] == "#34d399" {
			header = append(header, n)
		}
	}
	return header
}

func TestPosterWrappedHeaderMovesTheWholeDrawing785(t *testing.T) {
	for _, animated := range []bool{false, true} {
		base := poster785XML(t, poster785Render(t, poster785Chart(map[string]string{"Limit": "2"}), animated))
		wrapped := poster785XML(t, poster785Render(t, poster785Chart(poster785Bounds(24)), animated))
		lines := poster785CheckLines(t, wrapped)
		shift := lines[len(lines)-1].y - 104
		if shift <= 0 {
			t.Fatal("overflowing subtitle added no header height")
		}
		if base[0].a["width"] != wrapped[0].a["width"] {
			t.Error("wrapping widened the poster")
		}
		if got := poster785Num(t, wrapped[0].a["height"]) - poster785Num(t, base[0].a["height"]); math.Abs(got-shift) > 1 {
			t.Errorf("canvas grew %.1f; added subtitle height is %.1f", got, shift)
		}
		vb := strings.Fields(wrapped[0].a["viewBox"])
		if len(vb) != 4 || poster785Num(t, vb[2]) != poster785Num(t, wrapped[0].a["width"]) || poster785Num(t, vb[3]) != poster785Num(t, wrapped[0].a["height"]) {
			t.Error("viewBox does not contain the expanded canvas")
		}
		ha, hb := poster785Header(base), poster785Header(wrapped)
		if len(ha) != 4 || len(hb) != 4 {
			t.Fatal("missing title or exploration badge")
		}
		for i, a := range ha {
			b := hb[i]
			ax, ay := poster785Offset(t, a)
			bx, by := poster785Offset(t, b)
			if !reflect.DeepEqual(a.a, b.a) || ax != bx || ay != by {
				t.Errorf("title or exploration badge moved: %s %q", a.name, a.text)
			}
		}
		a, b := poster785Body(base), poster785Body(wrapped)
		if len(a) != len(b) || len(a) < 30 {
			t.Fatalf("drawing content changed: %d -> %d elements", len(a), len(b))
		}
		for i, before := range a {
			after := b[i]
			if before.name != after.name || before.text != after.text {
				t.Fatalf("drawing content changed at %d", i)
			}
			ax, ay := poster785Offset(t, before)
			bx, by := poster785Offset(t, after)
			for _, attr := range []string{"x", "y", "cx", "cy", "x1", "y1", "x2", "y2", "width", "height", "r", "rx", "ry"} {
				av, bv := before.a[attr], after.a[attr]
				if av == "" && bv == "" {
					continue
				}
				x, y, want := poster785Num(t, av), poster785Num(t, bv), 0.0
				if attr == "y" || attr == "cy" || attr == "y1" || attr == "y2" {
					x += ay
					y += by
					want = shift
				}
				if attr == "x" || attr == "cx" || attr == "x1" || attr == "x2" {
					x += ax
					y += bx
				}
				if math.Abs(y-x-want) > 0.2 {
					t.Errorf("%s[%d].%s moved %.1f, want %.1f", before.name, i, attr, y-x, want)
				}
			}
			for _, attr := range []string{"d", "path"} {
				if before.a[attr] != "" {
					poster785ShiftPath(t, before.a[attr], after.a[attr], ax, ay, bx, by, shift)
				}
			}
		}
		// The START pill sits eleven pixels above its card. It too must be
		// below the last subtitle baseline, with the original breathing room.
		for _, n := range wrapped {
			if n.name == "rect" && n.parent != nil && n.parent.a["data-state-id"] != "" {
				_, dy := poster785Offset(t, n)
				if poster785Num(t, n.a["y"])+dy <= lines[len(lines)-1].y+14.5 {
					t.Error("a state card or its START pill overlaps the subtitle")
				}
			}
		}
	}
}

func TestPosterOversizedItemKeepsCompleteEvidence785(t *testing.T) {
	value := "{" + strings.Repeat("café<&>, λvalue, ", 80) + "last}"
	bounds := map[string]string{"Before": "{one, two}", "Payloads": value, "ZLimit": "7"}
	c := poster785Chart(bounds)
	before, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := c.SVG()
	if err != nil {
		t.Fatal(err)
	}
	var still []poster785Line
	for _, animated := range []bool{false, true} {
		s := poster785Render(t, c, animated)
		if !utf8.ValidString(s) || strings.ContainsRune(s, '\uFFFD') {
			t.Error("shortening damaged Unicode text")
		}
		lines := poster785CheckLines(t, poster785XML(t, s))
		items := poster785LineItems(lines)
		want := poster785Items(bounds)
		if len(items) != len(want) {
			t.Fatalf("an oversized item split or swallowed neighbors: %q", items)
		}
		for i := range want {
			if strings.HasPrefix(want[i], "Payloads = ") {
				if !strings.HasSuffix(items[i], "…") {
					t.Errorf("oversized item has no ellipsis: %q", items[i])
					continue
				}
				prefix := strings.TrimSuffix(items[i], "…")
				if !strings.HasPrefix(prefix, "Payloads = {") || !strings.HasPrefix(want[i], prefix) || len(prefix) >= len(want[i]) {
					t.Errorf("shortened bound is not a literal prefix: %q", items[i])
				}
			} else if items[i] != want[i] {
				t.Errorf("neighboring item changed: %q, want %q", items[i], want[i])
			}
		}
		if animated && !reflect.DeepEqual(still, lines) {
			t.Error("oversized item differs between poster twins")
		}
		if !animated {
			still = lines
		}
	}
	after, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("poster mutated reader evidence")
	}
	again, err := c.SVG()
	if err != nil || again != evidence {
		t.Errorf("complete evidence image changed: %v", err)
	}
	nodes := poster785XML(t, evidence)
	var visible, metadata strings.Builder
	for _, n := range nodes {
		if n.name == "text" || n.name == "tspan" {
			visible.WriteString(n.text)
		}
		if n.name == "metadata" {
			metadata.WriteString(n.text)
		}
	}
	if !strings.Contains(visible.String(), "Payloads = "+value) {
		t.Error("complete evidence image lost the full visible bound")
	}
	var retained statechart.Result
	if err := json.Unmarshal([]byte(metadata.String()), &retained); err != nil || retained.Report.Source.Config.Constants["Payloads"] != value {
		t.Errorf("complete evidence metadata lost the full bound: %v", err)
	}
}
