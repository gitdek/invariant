package statechart

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
)

type Report struct {
	Rules           []project.Statement `json:"rules"`
	Temporal        []tlc.TraceFile     `json:"temporal"`
	Hidden          []Arrow             `json:"hidden"`
	Counts          map[string]int64    `json:"counts"`
	CountMeanings   map[string]string   `json:"count_meanings"`
	ArrowTarget     int                 `json:"arrow_target"`
	OverArrowTarget bool                `json:"over_arrow_target"`
	Source          Source              `json:"source"`
}

type Source struct {
	Module string        `json:"module"`
	Config tlc.Config    `json:"config"`
	Tool   tlc.GraphTool `json:"tool"`
	TLC    string        `json:"tlc_version"`
}

func makeReport(in Input, chart *Result, hidden []Arrow) (Report, error) {
	r := Report{
		Rules: slices.Clone(in.Lock.Statements), Hidden: hidden,
		ArrowTarget: ArrowTarget, OverArrowTarget: len(chart.Arrows) > ArrowTarget,
		Source: Source{Module: in.Graph.Module, Config: cloneConfig(in.Graph.Config), Tool: in.Graph.Tool, TLC: in.Graph.Run.Version},
		Counts: map[string]int64{
			"tlc_states": int64(len(in.Graph.Graph.States)), "tlc_steps": int64(len(in.Graph.Graph.Steps)),
			"tlc_initial_states": int64(len(in.Graph.Graph.Initial)), "tlc_generated_states": in.Graph.Run.StatesGenerated,
			"chart_states": int64(len(chart.States)), "chart_arrows": int64(len(chart.Arrows)),
			"chart_arrows_before_hiding": int64(len(chart.Arrows) + len(hidden)), "shown_steps": 0, "hidden_steps": 0,
		},
		CountMeanings: map[string]string{
			"tlc_states":                 "Distinct reachable concrete states in the complete TLC graph.",
			"tlc_steps":                  "Dumped concrete step occurrences, including duplicates and self-loops.",
			"tlc_initial_states":         "Distinct concrete initial states.",
			"tlc_generated_states":       "TLC generated states: initial generation (including duplicates) plus dumped step occurrences.",
			"chart_states":               "Partitions by reader label and violated invariant set, including all concrete states.",
			"chart_arrows":               "Visible source/reader-label/target buckets after hiding.",
			"chart_arrows_before_hiding": "All source/reader-label/target buckets before hiding.",
			"shown_steps":                "Concrete step occurrences belonging to visible arrows.",
			"hidden_steps":               "Concrete step occurrences belonging to fully hidden arrows.",
		},
	}
	for _, a := range chart.Arrows {
		r.Counts["shown_steps"] += int64(a.Count)
	}
	for _, a := range hidden {
		r.Counts["hidden_steps"] += int64(a.Count)
	}
	// Trace Vars is JSON data, potentially with nested maps and slices. UseNumber
	// retains integer precision while detaching the entire counterexample.
	b, err := json.Marshal(in.Temporal)
	if err != nil {
		return Report{}, refuse("invalid_evidence", "temporal trace is not JSON data: "+err.Error())
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&r.Temporal); err != nil {
		return Report{}, refuse("invalid_evidence", "cannot snapshot temporal trace: "+err.Error())
	}
	slices.SortFunc(r.Temporal, func(a, b tlc.TraceFile) int {
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return bytes.Compare(x, y)
	})
	return r, nil
}

func cloneConfig(c tlc.Config) tlc.Config {
	c.Constants = maps.Clone(c.Constants)
	c.Invariants = slices.Clone(c.Invariants)
	c.Properties = slices.Clone(c.Properties)
	return c
}
