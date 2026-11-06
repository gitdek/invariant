package statechart_test

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

func TestComparisonSerializationPreservesTraceNumbers(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", comparison648View)
	a.Chart.Report.Temporal = []tlc.TraceFile{{States: []tlc.TraceState{{Index: 1,
		Vars: map[string]any{"count": json.Number("9007199254740993")}}}}}
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	data := comparison648JSON(t, c)
	var restored statechart.Comparison
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(); err != nil {
		t.Fatalf("saved comparison lost exact trace evidence: %v", err)
	}
	if string(comparison648JSON(t, &restored)) != string(data) {
		t.Fatal("comparison serialization changed trace numbers")
	}
}

func TestComparisonRetainsMembershipAndOccurrenceChanges(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 1, "Poll", comparison648View)
	for i := range b.Chart.States {
		for j, id := range b.Chart.States[i].Members {
			if id == "1" {
				b.Chart.States[i].Members[j] = "9"
			}
		}
	}
	for _, arrows := range [][]statechart.Arrow{b.Chart.Arrows, b.Chart.Report.Hidden} {
		for i := range arrows {
			for j := range arrows[i].Members {
				step := &arrows[i].Members[j]
				if step.From == "1" {
					step.From = "9"
				}
				if step.To == "1" {
					step.To = "9"
				}
			}
		}
	}
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	// TLC picks its fingerprint polynomial afresh on every run, so another
	// exploration of the same model names the same concrete states anew.
	if n := len(c.Visible.AddedStates) + len(c.Visible.RemovedStates) + len(c.Visible.ChangedStates) + len(c.Visible.ChangedArrows) + len(c.BeforeHiding.ChangedArrows); n != 0 {
		t.Fatalf("renamed fingerprints reported as %d changes: %+v", n, c)
	}

	b = comparison648Snapshot(t, 1, "Poll", comparison648View)
	s0 := &b.Chart.States[0]
	s0.Members = append(s0.Members, "9")
	b.Chart.Report.Counts["tlc_states"]++
	c, err = statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Visible.ChangedStates) != 1 || len(c.Visible.ChangedStates[0].Before.Members)+1 != len(c.Visible.ChangedStates[0].After.Members) {
		t.Fatalf("lost membership change: %+v", c)
	}

	b = comparison648Snapshot(t, 1, "Poll", comparison648View)
	a0 := &b.Chart.Arrows[0]
	a0.Members = append(a0.Members, a0.Members[0])
	a0.Count++
	b.Chart.Report.Counts["tlc_steps"]++
	b.Chart.Report.Counts["shown_steps"]++
	b.Chart.Report.Counts["tlc_generated_states"]++
	c, err = statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Visible.ChangedArrows) != 1 || len(c.Visible.ChangedArrows[0].Before.Members) != 1 || len(c.Visible.ChangedArrows[0].After.Members) != 2 {
		t.Fatal("duplicate occurrence was deduplicated")
	}
	c.Visible.ChangedArrows[0].After.Members = nil
	if err := c.Validate(); err == nil {
		t.Fatal("changed delta accepted with original fingerprint")
	}
}

func TestComparisonMermaidMarksRemovedDirections(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", comparison648View)
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	md := c.Markdown()
	for _, want := range []string{b.Chart.Markdown(), "flowchart LR", "stroke-dasharray:8 5", "removed#58;", "added#58;", " -. ", "#18743b", "#b42318"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestComparisonLimitationDoesNotReportZeroChanges(t *testing.T) {
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
	for _, text := range []string{c.Markdown(), svg} {
		if !strings.Contains(text, "Comparison unavailable") || strings.Contains(text, "states added 0") || strings.Contains(text, "arrows added 0") {
			t.Fatal("unavailable comparison reported zero differences")
		}
	}
	if !strings.Contains(c.Markdown(), b.Chart.Markdown()) {
		t.Fatal("limited baseline suppressed the complete candidate")
	}
}

func TestComparisonNestedSVGHasUniqueIDsAndAttributes(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", comparison648View)
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	svg, err := c.SVG()
	if err != nil {
		t.Fatal(err)
	}
	svg, err = statechart.StackSVG(svg, svg)
	if err != nil {
		t.Fatal(err)
	}
	d := xml.NewDecoder(strings.NewReader(svg))
	ids := map[string]bool{}
	var references []string
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		el, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		attrs := map[xml.Name]bool{}
		for _, a := range el.Attr {
			if attrs[a.Name] {
				t.Fatalf("duplicate attribute %s", a.Name)
			}
			attrs[a.Name] = true
			if a.Name.Local == "id" {
				if ids[a.Value] {
					t.Fatalf("duplicate id %s", a.Value)
				}
				ids[a.Value] = true
			}
			if a.Name.Local == "marker-end" {
				references = append(references, strings.TrimSuffix(strings.TrimPrefix(a.Value, "url(#"), ")"))
			}
		}
	}
	for _, ref := range references {
		if !ids[ref] {
			t.Fatalf("missing local marker %s", ref)
		}
	}
}

// A saved proposal omits empty includes, so a comparison recomputed from it
// must match the one its check made (#648).
func TestComparisonTreatsAbsentIncludesAsEmpty(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", comparison648View)
	checked, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	a.Includes, b.Includes = nil, nil
	saved, err := statechart.Compare(a, b)
	if err != nil || saved.Fingerprint != checked.Fingerprint {
		t.Fatalf("absent includes changed the comparison: %v", err)
	}
}

// A report names each change by its reader label and counts, never by the
// concrete fingerprints beneath it, so it grows with the chart. Unprintable
// source names are refused before they reach a renderer.
func TestComparisonReportStaysReadable(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", comparison648View)
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	md := c.Markdown()
	report := md[:strings.Index(md, "Complete candidate chart:")]
	if strings.Contains(report, `"members"`) || !strings.Contains(report, "Added state") || !strings.Contains(report, "concrete states") {
		t.Fatalf("report doesn't summarize changes by label:\n%s", report)
	}
	b.Name = "Proposal \x01"
	if _, err := statechart.Compare(a, b); err == nil {
		t.Fatal("an unprintable source name was accepted")
	}
}
