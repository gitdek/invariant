package statechart_test

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
)

func TestSVGRouteClearance(t *testing.T) {
	cases := []struct {
		name  string
		chart func(*testing.T) *statechart.Result
	}{
		{"ordinary", func(t *testing.T) *statechart.Result {
			return svg608Chart(t, "Ready", "Working", "Begin")
		}},
		{"unequal text and return spans", func(t *testing.T) *statechart.Result {
			return svg613Chart(t, []string{"Start", "Left\n" + strings.Repeat("W", 90), "Right", "Done"}, []int{0}, []svg613Edge{
				{0, 1, "Begin", false}, {0, 1, "Retry\nagain", false}, {0, 2, "Choose right", false},
				{1, 2, "Across\n" + strings.Repeat("long ", 30), false}, {2, 1, "Across back", false},
				{1, 1, "Inspect", false}, {1, 1, "Wait\nquietly", false},
				{1, 3, "Finish", false}, {2, 3, "Complete", false},
				{3, 0, "Restart\n" + strings.Repeat("return ", 40), false},
				{2, 0, "Reset", false}, {3, 1, "Rework", false},
			})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.chart(t)
			n := svg613Render(t, c)
			svg613Geometry(t, c, n)
			svg613Layers(t, c, n)
			var obstacles []svg608Box
			for _, g := range n.groups("data-state-id") {
				obstacles = append(obstacles, svg608Rect(t, g))
			}
			arrows := n.groups("data-from")
			for _, g := range arrows {
				obstacles = append(obstacles, svg608Rect(t, g))
			}
			for i, g := range arrows {
				points := svg613Path(t, svg608DirectedPath(t, g))
				for j, box := range obstacles {
					if j == len(c.States)+i {
						continue // The route passes behind its own label.
					}
					inside := svg608Box{box.x0 + 1, box.y0 + 1, box.x1 - 1, box.y1 - 1}
					for k := 1; k < len(points); k++ {
						if svg613SegmentBox(points[k-1], points[k], inside) {
							t.Fatalf("route %q passes through state or label %d", g.attrs["data-label"], j)
						}
					}
				}
			}
		})
	}
}
