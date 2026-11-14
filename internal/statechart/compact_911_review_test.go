package statechart_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
)

// A compact comparison draws both charts' initial states, which the complete
// one leaves to the candidate's own chart. Mermaid numbers every link in the
// order it's written, an initial state's among them, and each added, removed
// or changed arrow is coloured by that number: so the arrows a compact
// comparison colours are the ones it names, each of them, and no other
// (#911's review).
func TestCompact911ReviewComparisonColoursTheArrowsItNames(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Pulse", comparison648View)
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	md := c.CompactMarkdown()
	fence := regexp.MustCompile("(?s)```mermaid\nflowchart LR\n(.*?)```").FindStringSubmatch(md)
	if fence == nil {
		t.Fatalf("a compact comparison has no annotated chart:\n%s", md)
	}
	var links []string
	coloured := map[int]string{}
	for _, line := range strings.Split(fence[1], "\n") {
		line = strings.TrimSpace(line)
		if style := regexp.MustCompile(`^linkStyle (\d+) stroke:(#[0-9a-f]{6}),`).FindStringSubmatch(line); style != nil {
			n, _ := strconv.Atoi(style[1])
			coloured[n] = style[2]
		} else if strings.Contains(line, "-->") || strings.Contains(line, ".->") {
			links = append(links, line)
		}
	}
	colours := map[string]string{"added#58;": "#18743b", "removed#58;": "#b42318", "changed#58;": "#825600"}
	initials, named := 0, 0
	for n, link := range links {
		want := ""
		for change, colour := range colours {
			if strings.Contains(link, `"`+change) {
				want = colour
			}
		}
		if strings.Contains(link, `(("initial"))`) {
			initials++
		}
		if want != "" {
			named++
		}
		if coloured[n] != want {
			t.Errorf("link %d, %s, is coloured %q, want %q", n, link, coloured[n], want)
		}
	}
	if initials != 2 || named == 0 || len(coloured) != named {
		t.Errorf("%d initial states, %d arrows named as changes and %d coloured:\n%s", initials, named, len(coloured), fence[1])
	}
}

// An alternative's chart is checked under the choices made before it, and a
// compact chart says which, as the complete one does.
func TestCompact911ReviewCompactChartKeepsItsChoices(t *testing.T) {
	c := comparison648Snapshot(t, 1, "Poll", comparison648View).Chart
	c.Preview = statechart.Preview{Fork: "F2", Option: "A", Choices: map[string]string{"F1": "B", "F0": "A"}}
	const choices = "\nPreviously authorized choice: F0 = A.\n\nPreviously authorized choice: F1 = B.\n"
	for name, md := range map[string]string{"compact": c.CompactMarkdown(), "complete": c.Markdown()} {
		if !strings.HasPrefix(md, "Unratified alternative F2/A; model-only evidence with candidate rules.\n\n") || !strings.Contains(md, choices) {
			t.Errorf("a %s chart of an alternative doesn't say what it is or which choices it was checked under:\n%s", name, md)
		}
	}
	if plain := comparison648Snapshot(t, 1, "Poll", comparison648View).Chart.CompactMarkdown(); strings.Contains(plain, "Previously authorized") || strings.Contains(plain, "Unratified") {
		t.Errorf("a compact chart of no alternative names one:\n%s", plain)
	}
}
