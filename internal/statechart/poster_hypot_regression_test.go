package statechart_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
)

// Nearly equal curve lengths choose different label positions if Hypot's
// inner multiply and add round separately. Preserve the arm64 placement in
// both motion modes; the ordinary suite also runs this with fusion disabled.
func TestPosterHypotLabelPlacement(t *testing.T) {
	_, edges := svg613Chain(3, false)
	for i := 1; i <= 7; i++ {
		edges = append(edges, svg613Edge{0, 2, fmt.Sprintf("Jump%d", i), false})
	}
	// Each 26-character word fills a poster line: three, six and one lines.
	c := svg613Chart(t, []string{
		strings.TrimSpace(strings.Repeat(strings.Repeat("A", 26)+" ", 3)),
		strings.TrimSpace(strings.Repeat(strings.Repeat("B", 26)+" ", 6)),
		"Done",
	}, []int{0}, edges)
	label := regexp.MustCompile(`<g><rect [^>]* y="([^"]+)"[^>]*/>\s*<text [^>]*>Jump3 <tspan`)
	for _, animated := range []bool{false, true} {
		t.Run(fmt.Sprintf("animated=%v", animated), func(t *testing.T) {
			s, err := c.Poster(statechart.PosterOptions{Title: "Hypotenuse rounding", Animated: animated})
			if err != nil {
				t.Fatal(err)
			}
			matches := label.FindAllStringSubmatch(s, -1)
			if len(matches) != 1 {
				t.Fatalf("found %d Jump3 label rectangles, want 1", len(matches))
			}
			if got := matches[0][1]; got != "707.0" {
				t.Errorf("Jump3 label y=%s, want arm64 707.0 (unfused Hypot gives 357.0)", got)
			}
		})
	}
}
