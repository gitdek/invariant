package statechart_test

import (
	"bytes"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
)

func TestComparison717DetachedNestedEvidence(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	a.Includes["_Helper.tla"] = "---- MODULE _Helper ----\n====\n"
	a.Lock.Ratified = &project.Ratification{Issue: 717, By: "owner", Comment: "exact comment"}
	b := comparison648Snapshot(t, 2, "Poll", comparison648View)
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	before := comparison648JSON(t, c)
	for name, mutate := range map[string]func(*statechart.Comparison){
		"includes":           func(d *statechart.Comparison) { d.Baseline.Includes["_Helper.tla"] += "changed" },
		"identity includes":  func(d *statechart.Comparison) { d.BaselineIdentity.Includes["_Helper.tla"] = "changed" },
		"ratification":       func(d *statechart.Comparison) { d.Baseline.Lock.Ratified.Comment += "changed" },
		"lock bounds":        func(d *statechart.Comparison) { d.Baseline.Lock.Bounds["Limit"] = "9" },
		"lock statements":    func(d *statechart.Comparison) { d.Baseline.Lock.Statements[0].Says += "changed" },
		"chart rules":        func(d *statechart.Comparison) { d.Baseline.Chart.Report.Rules[0].Says += "changed" },
		"chart counts":       func(d *statechart.Comparison) { d.Baseline.Chart.Report.Counts["tlc_steps"]++ },
		"source bounds":      func(d *statechart.Comparison) { d.Baseline.Chart.Report.Source.Config.Constants["Limit"] = "9" },
		"initials":           func(d *statechart.Comparison) { d.Baseline.Chart.Initial[0] = "changed" },
		"members":            func(d *statechart.Comparison) { d.Baseline.Chart.States[0].Members[0] = "changed" },
		"visible occurrence": func(d *statechart.Comparison) { d.Baseline.Chart.Arrows[0].Members[0].Action = "changed" },
		"hidden occurrence":  func(d *statechart.Comparison) { d.Baseline.Chart.Report.Hidden[0].Members[0].Action = "changed" },
		"hiding rules":       func(d *statechart.Comparison) { d.Baseline.Chart.Report.Hidden[0].HidingRules[0] = "changed" },
		"added state":        func(d *statechart.Comparison) { d.Visible.AddedStates[0].Members[0] = "changed" },
		"removed state":      func(d *statechart.Comparison) { d.Visible.RemovedStates[0].Members[0] = "changed" },
		"added arrow":        func(d *statechart.Comparison) { d.Visible.AddedArrows[0].Members[0].Action = "changed" },
		"removed arrow":      func(d *statechart.Comparison) { d.Visible.RemovedArrows[0].Members[0].Action = "changed" },
		"before hiding":      func(d *statechart.Comparison) { d.BeforeHiding.RemovedArrows[0].Members[0].Action = "changed" },
	} {
		t.Run(name, func(t *testing.T) {
			d, err := c.Validated()
			if err != nil {
				t.Fatal(err)
			}
			if err := d.Validate(); err != nil {
				t.Fatal(err)
			}
			mutate(d)
			if !bytes.Equal(before, comparison648JSON(t, c)) {
				t.Fatal("validated nested evidence aliases its source")
			}
			if _, err := d.Validated(); err == nil {
				t.Fatal("nested mutation survived a fresh validation")
			}
			if err := d.Validate(); err == nil {
				t.Fatal("standalone validation trusted prior success")
			}
		})
	}
	// The caller also cannot change a previously returned validation result.
	d, err := c.Validated()
	if err != nil {
		t.Fatal(err)
	}
	c.Candidate.Chart.States[0].Members[0] = "changed by caller"
	if !bytes.Equal(before, comparison648JSON(t, d)) {
		t.Fatal("source mutation reached the detached result")
	}
}
