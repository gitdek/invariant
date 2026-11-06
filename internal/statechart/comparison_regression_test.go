package statechart_test

import (
	"bytes"
	"html"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
)

func TestComparisonKeepsDuplicateOccurrencesAndRejectsDamagedDeltas(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 1, "Poll", comparison648View)
	arrow := &b.Chart.Report.Hidden[0]
	arrow.Members = append(arrow.Members, arrow.Members[0])
	arrow.Count++
	for _, key := range []string{"hidden_steps", "tlc_steps", "tlc_generated_states"} {
		b.Chart.Report.Counts[key]++
	}
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Visible.ChangedArrows) != 0 || len(c.BeforeHiding.ChangedArrows) != 1 {
		t.Fatalf("hidden multiplicity was lost: %+v", c)
	}
	change := c.BeforeHiding.ChangedArrows[0]
	if len(change.Before.Members) != 1 || len(change.After.Members) != 2 {
		t.Fatal("concrete multiset was deduplicated")
	}
	before := comparison648JSON(t, c)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, comparison648JSON(t, c)) {
		t.Fatal("validation mutated the record")
	}
	c.BeforeHiding.ChangedArrows = nil
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "comparison") {
		t.Fatal("damaged delta remained valid")
	}
}

func TestComparisonSVGSourceNamesRemainLiteral(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", comparison648View)
	a.Name = `old <script>alert(1)</script> & " source`
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	svg, err := c.SVG()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(svg, "<script>") || !strings.Contains(svg, "&lt;script&gt;") {
		t.Fatal("baseline text became active markup")
	}
	a.Name = "invalid\x01name"
	c, err = statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SVG(); err == nil {
		t.Fatal("XML-forbidden baseline text accepted")
	}
}

func TestComparisonLimitationDoesNotReportAnEmptyDelta(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", comparison648View)
	a.Limitation = &statechart.Limitation{Code: "too_large", Actual: 501, Limit: 500, Source: a.Chart.Report.Source}
	a.Chart = nil
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	svg, err := c.SVG()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{c.Text(), c.Markdown(), svg} {
		text = html.UnescapeString(text)
		if !strings.Contains(text, "too_large") || strings.Contains(text, "States: 0 added") || strings.Contains(text, "Arrows: 0 added") {
			t.Fatal("an unavailable comparison reported an empty delta")
		}
	}
	if !strings.Contains(c.Markdown(), b.Chart.Markdown()) || !strings.Contains(svg, `data-comparison-side="candidate"`) {
		t.Fatal("limitation lost the complete candidate chart")
	}
}

func TestComparisonMarkdownKeepsNamedIssueBaseline(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", comparison648View)
	a.Kind, a.Name = "ratification", "Ratification #571 in examples/reader"
	b.Name = "Proposal <script>alert(1)</script>\n# injected heading"
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	md := c.Markdown()
	if !strings.Contains(md, a.Name) || strings.Contains(md, "<script>") || strings.Contains(md, "\n# injected heading") {
		t.Fatal("baseline name was escaped away or source text became markup")
	}
}
