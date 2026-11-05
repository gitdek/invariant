package dashboard

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestGraph575DashboardCompatibility(t *testing.T) {
	// The existing fixture and its existing tests continue to pin the display
	// behavior; the reusable data must agree before any display compaction.
	full, err := tlc.ParseGraphDOT(dump)
	if err != nil {
		t.Fatal(err)
	}
	display, err := ParseDot(dump)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.States) != display.States || len(full.Initial) != len(display.Init) || len(full.Steps)*3 != len(display.Edges) {
		t.Fatalf("new graph disagrees with existing dashboard: %+v / %+v", full, display)
	}
	byID := map[string]tlc.GraphState{}
	for _, s := range full.States {
		byID[s.ID] = s
	}
	labels := map[string]bool{}
	for _, label := range display.Labels {
		labels[label] = true
	}
	for _, s := range full.States {
		if !labels[s.Label] {
			t.Errorf("state %s lost its dashboard label: %q", s.ID, s.Label)
		}
	}
	fullInitial, displayInitial := map[string]bool{}, map[string]bool{}
	for _, id := range full.Initial {
		fullInitial[byID[id].Label] = true
	}
	for _, i := range display.Init {
		displayInitial[display.Labels[i]] = true
	}
	if !reflect.DeepEqual(fullInitial, displayInitial) {
		t.Fatalf("initial states changed: %v / %v", fullInitial, displayInitial)
	}
	type step struct{ from, action, to string }
	fullSteps, displaySteps := map[step]int{}, map[step]int{}
	for _, e := range full.Steps {
		fullSteps[step{byID[e.From].Label, e.Action, byID[e.To].Label}]++
	}
	for e := 0; e < len(display.Edges); e += 3 {
		from, to, action := display.Edges[e], display.Edges[e+1], display.Edges[e+2]
		displaySteps[step{display.Labels[from], display.Actions[action], display.Labels[to]}]++
	}
	if !reflect.DeepEqual(fullSteps, displaySteps) {
		t.Fatalf("action-labeled steps changed: %v / %v", fullSteps, displaySteps)
	}
	trace := tlc.TraceFile{States: []tlc.TraceState{
		{Action: "Initial predicate", TLA: map[string]string{"buf": "<<>>", "n": "0"}},
		{Action: "Write", TLA: map[string]string{"buf": `<<"a">>`, "n": "1"}},
		{Action: "Write", TLA: map[string]string{"buf": `<<"a",   "b">>`, "n": "2"}},
		{Action: "WriteOverCapacity", TLA: map[string]string{"buf": `<<"a", "b", "c">>`, "n": "3"}},
	}}
	path, action, escape := display.Trace(trace)
	if fmt.Sprint(path) != "[0 1 2]" || action != "WriteOverCapacity" || !strings.Contains(escape, "n = 3") {
		t.Fatalf("dashboard trace behavior changed: %v, %q, %q", path, action, escape)
	}

	// Cross both display thresholds. Statecharts must get all the evidence,
	// even when the display drops labels and most of the repeated steps.
	const states, steps = 2001, 65001
	var b strings.Builder
	b.WriteString("strict digraph DiskGraph {\nsubgraph cluster_graph {\n")
	for i := 0; i < states; i++ {
		style := ""
		if i == 0 {
			style = ",style = filled"
		}
		fmt.Fprintf(&b, "%d [label=\"n = %d\"%s];\n", i+1, i, style)
	}
	for i := 0; i < steps; i++ {
		fmt.Fprintf(&b, "%d -> %d [label=\"Next\",color=\"black\",fontcolor=\"black\"];\n", i%states+1, (i+1)%states+1)
	}
	b.WriteString("}\n}\n")
	full, err = tlc.ParseGraphDOT(b.String())
	if err != nil {
		t.Fatal(err)
	}
	display, err = ParseDot(b.String())
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	display.Compact()
	if len(display.Labels) != 0 || display.AllEdges != steps || len(display.Edges)/3 > 60000 {
		t.Fatalf("existing dashboard compaction changed: labels=%d edges=%d all=%d", len(display.Labels), len(display.Edges)/3, display.AllEdges)
	}
	after, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.States) != states || len(full.Steps) != steps || string(before) != string(after) {
		t.Fatalf("display compaction leaked into complete evidence: states=%d steps=%d", len(full.States), len(full.Steps))
	}
	for _, s := range full.States {
		if s.Label == "" || s.Values["n"] == "" {
			t.Fatalf("large graph lost exact state values: %+v", s)
		}
	}
}
