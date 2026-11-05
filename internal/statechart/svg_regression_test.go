package statechart_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gitdek/invariant/internal/statechart"
)

func TestSVGLabelAndReportClearance(t *testing.T) {
	c := svg608Chart(t, "Ready\n"+strings.Repeat("W", 160), "Working\n準備 café", "Begin")
	for i := range c.Arrows {
		c.Arrows[i].Label = fmt.Sprintf("Arrow %d\n%s\nlast line", i, strings.Repeat("long action ", i+1))
	}
	_, n := svg608Render(t, c)
	svg608Topology(t, c, n)
	v := strings.Fields(n.attrs["viewBox"])
	w, h := svg608Number(t, v[2]), svg608Number(t, v[3])
	var boxes []svg608Box
	for _, g := range n.groups("data-state-id") {
		boxes = append(boxes, svg608Rect(t, g))
	}
	for _, g := range n.groups("data-from") {
		box := svg608Rect(t, g)
		if box.x0 < 0 || box.y0 < 0 || box.x1 > w || box.y1 > h {
			t.Fatal("arrow label background escapes viewBox")
		}
		for _, other := range boxes {
			if box.x0 < other.x1 && other.x0 < box.x1 && box.y0 < other.y1 && other.y0 < box.y1 {
				t.Fatal("arrow label overlaps another label or a state")
			}
		}
		boxes = append(boxes, box)
		for _, span := range g.all("tspan") {
			x, y := svg608Number(t, span.attrs["x"]), svg608Number(t, span.attrs["y"])
			// A modest lower bound on monospace width catches text that gets
			// positioned on a line but no horizontal room for its contents.
			if x < box.x0 || y-14 < box.y0 || y > box.y1 || x+7*float64(utf8.RuneCountInString(span.text)) > box.x1 {
				t.Fatal("arrow text does not fit its reserved rectangle")
			}
		}
	}
	for _, span := range n.all("tspan") {
		x, y := svg608Number(t, span.attrs["x"]), svg608Number(t, span.attrs["y"])
		if x < 0 || y-14 < 0 || y > h || x+7*float64(utf8.RuneCountInString(span.text)) > w {
			t.Fatal("drawing or report text escapes viewBox")
		}
	}
}

func TestSVGRejectsInconsistentAndMetadataOnlyData(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*statechart.Result)
	}{
		{"repeated arrow bucket", func(c *statechart.Result) { c.Arrows = append(c.Arrows, c.Arrows[0]) }},
		{"hidden endpoint", func(c *statechart.Result) { c.Report.Hidden[0].From = "absent" }},
		{"occurrence count", func(c *statechart.Result) { c.Arrows[0].Count++ }},
		{"hidden occurrence", func(c *statechart.Result) { c.Report.Hidden[0].Members[0].To = "absent" }},
		{"shared concrete member", func(c *statechart.Result) { c.States[1].Members = append(c.States[1].Members, c.States[0].Members[0]) }},
		{"initial member", func(c *statechart.Result) { c.States[0].InitialMembers = []string{"absent"} }},
		{"count accounting", func(c *statechart.Result) { c.Report.Counts["hidden_steps"]++ }},
		{"advisory flag", func(c *statechart.Result) { c.Report.OverArrowTarget = !c.Report.OverArrowTarget }},
		{"bound name control", func(c *statechart.Result) { c.Report.Source.Config.Constants["bad\x00name"] = "1" }},
		{"provenance noncharacter", func(c *statechart.Result) { c.Report.Source.Tool.Image += "\ufffe" }},
		{"metadata map key UTF8", func(c *statechart.Result) { c.Report.Temporal[0].States[0].Vars[string([]byte{0xff})] = true }},
		{"nested metadata control", func(c *statechart.Result) {
			c.Report.Temporal[0].States[0].Vars["nested"] = []any{map[string]any{"value": "\x01"}}
		}},
		{"non JSON metadata", func(c *statechart.Result) { c.Report.Temporal[0].States[0].Vars["function"] = func() {} }},
		{"cyclic metadata", func(c *statechart.Result) {
			vars := c.Report.Temporal[0].States[0].Vars
			vars["cycle"] = vars
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := svg608Chart(t, "Ready", "Working", "Begin")
			tc.edit(c)
			s, err := c.SVG()
			if err == nil || s != "" {
				t.Fatalf("want explicit refusal without SVG, got %q, %v", s, err)
			}
		})
	}
}

func TestSVGLiteralIDsWhitespaceAndPreciseMetadata(t *testing.T) {
	c := svg608Chart(t, "Ready\r\n  two  spaces\t😀", "Working", "Go\t<&>\r\nnext")
	old := c.States[0].ID
	const id = "id\" onload=\"attack <&>\r\n\t"
	c.States[0].ID = id
	for i, initial := range c.Initial {
		if initial == old {
			c.Initial[i] = id
		}
	}
	for _, arrows := range [][]statechart.Arrow{c.Arrows, c.Report.Hidden} {
		for i := range arrows {
			if arrows[i].From == old {
				arrows[i].From = id
			}
			if arrows[i].To == old {
				arrows[i].To = id
			}
		}
	}
	c.Report.Temporal[0].States[0].Vars["large"] = json.Number("9007199254740993123456789")
	c.Report.Temporal[0].States[0].Vars["literal"] = "</metadata><script>literal & only</script>"
	before := string(svg608JSON(t, c))
	_, n := svg608Render(t, c)
	svg608Topology(t, c, n)
	metadata := n.all("metadata")
	if len(metadata) != 1 || metadata[0].text != before {
		t.Fatal("JSON metadata changed text, numeric precision or escaped markup")
	}
	for _, g := range n.groups("data-state-id") {
		for _, s := range c.States {
			if g.attrs["data-state-id"] == s.ID && !strings.Contains(svg608Visible(g), s.Label) {
				t.Fatal("visible state label lost literal whitespace")
			}
		}
	}
	if string(svg608JSON(t, c)) != before {
		t.Fatal("renderer mutated input")
	}
}
