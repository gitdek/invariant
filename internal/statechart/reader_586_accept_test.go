package statechart_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

// Public computation contract for this step:
//
// Compute(context.Context, Input, []byte) (*Result, error)
// Input has Graph tlc.GraphResult, Lock project.Lock,
// Invariants map[string]map[string]bool (state ID -> invariant name -> holds),
// and Temporal []tlc.TraceFile. Input is trusted TLC/lock evidence, never part
// of the drafting agent's view. EvaluateRules supplies the invariant matrix.
// Each invariant in the lock needs exactly one boolean for every graph state;
// properties must never occur in that matrix. The graph/config/bounds and
// matrix are validated together. A failed computation returns nil, not a
// partial Result. Refusal implements error and has Code string, States []string
// (concrete IDs), Steps []tlc.GraphStep, Rules []string (view rule IDs), and
// Actual, Limit int. Cancellation remains available through errors.Is.
//
// Version 1 views have exactly version, state and step fields. State/step are
// ordered nonempty rule arrays, each rule having a nonblank id unique within
// its array, when, and a nonblank literal label; step rules may additionally
// have hide: bool.
// First matching rule wins; no match refuses. No view field supplies graph,
// invariant or temporal evidence. All rules are validated before evaluation.
// when is a boolean literal or exactly one of eq:[term,term], all:[when,...],
// any:[when,...], not:when. Terms are string/bool literals or a single lookup:
// {"var":"name"} for state, or {"from":"name"}, {"to":"name"},
// {"action":true} for step. Variable lookups yield EXACT TLC TLA+ text strings;
// action yields the concrete model action. Eq requires like types; boolean
// operators require booleans. Missing variables and type errors refuse.
// There are no calls, scripts, interpolation, loops, globals or host access.
//
// Partition concrete states by (reader label, sorted false-invariant names).
// A chart ID is stable and unique for that pair, independent of graph order.
// Partition steps by (source chart ID, reader label, target chart ID), BEFORE
// hiding, preserving duplicate concrete occurrences. Each bucket must have
// uniform visibility, even across different original actions or view rules.
// Then require every chart state reachable from the chart initials and every
// chart state with an outgoing bucket before hiding to retain an arrow.
// No shortcut edges, sampled members or inferred stutters are permitted.
// Collections are canonically ordered; immutable snapshots survive caller
// mutations and concurrent calls. The JSON result is specified below. It may
// include additional documented fields, but must preserve these meanings.

type reader586State struct {
	ID                 string   `json:"id"`
	Label              string   `json:"label"`
	Members            []string `json:"members"`
	InitialMembers     []string `json:"initial_members"`
	ViolatedRules      []string `json:"violated_rules"`
	UnavailableActions []string `json:"unavailable_actions"`
}

type reader586Arrow struct {
	From        string          `json:"from"`
	To          string          `json:"to"`
	Label       string          `json:"label"`
	Members     []tlc.GraphStep `json:"members"`
	Count       int             `json:"count"`
	HidingRules []string        `json:"hiding_rules"`
}

type reader586Result struct {
	Version int              `json:"version"`
	States  []reader586State `json:"states"`
	Initial []string         `json:"initial"`
	Arrows  []reader586Arrow `json:"arrows"`
	Report  struct {
		Rules           []project.Statement `json:"rules"`
		Temporal        []tlc.TraceFile     `json:"temporal"`
		Hidden          []reader586Arrow    `json:"hidden"`
		Counts          map[string]int64    `json:"counts"`
		CountMeanings   map[string]string   `json:"count_meanings"`
		ArrowTarget     int                 `json:"arrow_target"`
		OverArrowTarget bool                `json:"over_arrow_target"`
		Source          struct {
			Module string        `json:"module"`
			Config tlc.Config    `json:"config"`
			Tool   tlc.GraphTool `json:"tool"`
			TLC    string        `json:"tlc_version"`
		} `json:"source"`
	} `json:"report"`
}

func reader586JSON(t *testing.T, x any) []byte {
	t.Helper()
	b, err := json.Marshal(x)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func reader586Read(t *testing.T, result any) reader586Result {
	t.Helper()
	var out reader586Result
	if err := json.Unmarshal(reader586JSON(t, result), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func reader586Compute(t *testing.T, in statechart.Input, view string) reader586Result {
	t.Helper()
	out, err := statechart.Compute(t.Context(), in, []byte(view))
	if err != nil || out == nil {
		t.Fatalf("compute: result=%v error=%v", out, err)
	}
	return reader586Read(t, out)
}

func reader586Input() statechart.Input {
	in := statechart.Input{Graph: tlc.GraphResult{
		Graph: tlc.Graph{Initial: []string{"10", "11"}, States: []tlc.GraphState{
			{ID: "10", Label: `/\ phase = "idle" /\ worker = 0`, Values: map[string]string{"phase": `"idle"`, "worker": "0"}},
			{ID: "11", Label: `/\ phase = "idle" /\ worker = 1`, Values: map[string]string{"phase": `"idle"`, "worker": "1"}},
			{ID: "20", Label: `/\ phase = "busy" /\ worker = 0`, Values: map[string]string{"phase": `"busy"`, "worker": "0"}},
			{ID: "21", Label: `/\ phase = "busy" /\ worker = 1`, Values: map[string]string{"phase": `"busy"`, "worker": "1"}},
			{ID: "30", Label: `/\ phase = "done" /\ worker = 0`, Values: map[string]string{"phase": `"done"`, "worker": "0"}},
		}, Steps: []tlc.GraphStep{
			{From: "10", Action: "Start", To: "20"}, {From: "11", Action: "Start", To: "21"},
			{From: "10", Action: "Start", To: "20"}, // a repeated dump entry still counts
			{From: "20", Action: "Finish", To: "30"}, {From: "21", Action: "Finish", To: "30"},
			{From: "30", Action: "Reset", To: "10"},
			{From: "20", Action: "Poll", To: "20"}, {From: "21", Action: "Poll", To: "21"},
			{From: "20", Action: "Cancel", To: "10"}, {From: "30", Action: "Stay", To: "30"},
		}},
		Module: "Lifecycle", Config: tlc.Config{Specification: "Spec", Constants: map[string]string{"Workers": "2"}},
		Tool: tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)},
		Run:  tlc.Result{Version: "TLC2 Version 2.19", Outcome: tlc.Passed, Depth: 3},
	}, Lock: project.Lock{Bounds: map[string]string{"Workers": "2"}, Statements: []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "A worker starts, finishes, and resets.", SHA256: strings.Repeat("c", 64)},
		{Name: "Safe", Kind: project.Invariant, Says: "A finished job keeps  its result.\nExactly as ratified.", SHA256: strings.Repeat("d", 64)},
	}}, Invariants: map[string]map[string]bool{}}
	reader586Counts(&in)
	for _, s := range in.Graph.Graph.States {
		in.Invariants[s.ID] = map[string]bool{"Safe": true}
	}
	return in
}

func reader586Counts(in *statechart.Input) {
	in.Graph.StateCount = len(in.Graph.Graph.States)
	in.Graph.StepCount = len(in.Graph.Graph.Steps)
	in.Graph.Run.DistinctStates = int64(in.Graph.StateCount)
	in.Graph.Run.StatesGenerated = int64(in.Graph.StepCount + len(in.Graph.Graph.Initial))
}

const reader586View = `{"version":1,"state":[
 {"id":"idle","when":{"eq":[{"var":"phase"},"\"idle\""]},"label":"Ready"},
 {"id":"busy","when":{"eq":[{"var":"phase"},"\"busy\""]},"label":"Working"},
 {"id":"done","when":{"eq":[{"var":"phase"},"\"done\""]},"label":"Finished"}
],"step":[
 {"id":"poll-detail","when":{"eq":[{"action":true},"Poll"]},"label":"Check progress","hide":true},
 {"id":"start","when":{"eq":[{"action":true},"Start"]},"label":"Start work"},
 {"id":"finish","when":{"eq":[{"action":true},"Finish"]},"label":"Finish work"},
 {"id":"reset","when":{"eq":[{"action":true},"Reset"]},"label":"Use again"},
 {"id":"cancel","when":{"eq":[{"action":true},"Cancel"]},"label":"Cancel work"},
 {"id":"stay","when":{"eq":[{"action":true},"Stay"]},"label":"Keep result"}
]}`

func reader586StateNamed(t *testing.T, out reader586Result, label string) reader586State {
	t.Helper()
	var found []reader586State
	for _, s := range out.States {
		if s.Label == label {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one %q state: %+v", label, out.States)
	}
	return found[0]
}

func reader586SameStrings(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func reader586Steps(steps []tlc.GraphStep) map[tlc.GraphStep]int {
	m := map[tlc.GraphStep]int{}
	for _, e := range steps {
		m[e]++
	}
	return m
}

func reader586Accounting(t *testing.T, in statechart.Input, out reader586Result) {
	t.Helper()
	owner := map[string]string{}
	chartIDs := map[string]bool{}
	initial := map[string]bool{}
	for _, id := range in.Graph.Graph.Initial {
		initial[id] = true
	}
	var chartInitial []string
	for _, s := range out.States {
		if s.ID == "" || s.Label == "" || chartIDs[s.ID] || len(s.Members) == 0 {
			t.Fatalf("bad chart state: %+v", s)
		}
		chartIDs[s.ID] = true
		var wantInitial []string
		for _, id := range s.Members {
			if owner[id] != "" {
				t.Fatalf("state %s mapped twice", id)
			}
			owner[id] = s.ID
			if initial[id] {
				wantInitial = append(wantInitial, id)
			}
		}
		if !reader586SameStrings(s.InitialMembers, wantInitial) {
			t.Fatalf("initial membership lost: %+v", s)
		}
		if len(wantInitial) > 0 {
			chartInitial = append(chartInitial, s.ID)
		}
	}
	if !reader586SameStrings(out.Initial, chartInitial) {
		t.Fatalf("chart initials: %v want %v", out.Initial, chartInitial)
	}
	if len(owner) != len(in.Graph.Graph.States) {
		t.Fatalf("members: %v", owner)
	}
	for _, s := range in.Graph.Graph.States {
		if owner[s.ID] == "" {
			t.Fatalf("lost %s", s.ID)
		}
	}
	var represented []tlc.GraphStep
	for _, a := range append(slices.Clone(out.Arrows), out.Report.Hidden...) {
		if a.Count != len(a.Members) || a.Count == 0 || !chartIDs[a.From] || !chartIDs[a.To] || a.Label == "" {
			t.Fatalf("bad arrow: %+v", a)
		}
		for _, e := range a.Members {
			if owner[e.From] != a.From || owner[e.To] != a.To {
				t.Fatalf("invented arrow: %+v", a)
			}
		}
		represented = append(represented, a.Members...)
	}
	if !reflect.DeepEqual(reader586Steps(represented), reader586Steps(in.Graph.Graph.Steps)) {
		t.Fatalf("lost or invented concrete steps: %+v", represented)
	}
}

func TestReader586LifecycleAndCounts(t *testing.T) {
	in := reader586Input()
	// TLC generated initials can contain duplicates; generated is not steps.
	in.Graph.Run.StatesGenerated++
	out := reader586Compute(t, in, reader586View)
	reader586Accounting(t, in, out)
	if out.Version != 1 || len(out.States) != 3 || len(out.Arrows) != 5 || len(out.Report.Hidden) != 1 {
		t.Fatalf("projection: %+v", out)
	}
	ready := reader586StateNamed(t, out, "Ready")
	if !reader586SameStrings(ready.Members, []string{"10", "11"}) || !reader586SameStrings(ready.InitialMembers, ready.Members) {
		t.Fatalf("ready: %+v", ready)
	}
	working := reader586StateNamed(t, out, "Working")
	if !reader586SameStrings(working.Members, []string{"20", "21"}) {
		t.Fatalf("working: %+v", working)
	}
	h := out.Report.Hidden[0]
	if h.Label != "Check progress" || h.Count != 2 || h.From != working.ID || h.To != working.ID || !reader586SameStrings(h.HidingRules, []string{"poll-detail"}) {
		t.Fatalf("hidden detail: %+v", h)
	}
	want := map[string]int64{"tlc_states": 5, "tlc_steps": 10, "tlc_initial_states": 2, "tlc_generated_states": 13, "chart_states": 3, "chart_arrows": 5, "chart_arrows_before_hiding": 6, "shown_steps": 8, "hidden_steps": 2}
	for key, n := range want {
		if out.Report.Counts[key] != n || strings.TrimSpace(out.Report.CountMeanings[key]) == "" {
			t.Errorf("%s: count=%d meaning=%q want %d", key, out.Report.Counts[key], out.Report.CountMeanings[key], n)
		}
	}
	if !reflect.DeepEqual(out.Report.Rules, in.Lock.Statements) {
		t.Fatalf("lock words changed: %+v", out.Report.Rules)
	}
	src := out.Report.Source
	if src.Module != in.Graph.Module || !reflect.DeepEqual(src.Config, in.Graph.Config) || src.Tool != in.Graph.Tool || src.TLC != in.Graph.Run.Version {
		t.Fatalf("provenance: %+v", src)
	}
	if out.Report.ArrowTarget != 20 || out.Report.OverArrowTarget {
		t.Fatalf("arrow advice: %+v", out.Report)
	}
	// Initial membership belongs to concrete members, even when a view merges
	// initial and noninitial states under one reader name.
	merged := reader586Compute(t, in, reader586AllView)
	reader586Accounting(t, in, merged)
	if len(merged.States) != 1 || len(merged.Initial) != 1 ||
		!reader586SameStrings(merged.States[0].InitialMembers, []string{"10", "11"}) {
		t.Fatalf("merged initial membership: %+v", merged)
	}
}

func reader586Refusal(t *testing.T, in statechart.Input, view, code string) *statechart.Refusal {
	t.Helper()
	out, err := statechart.Compute(t.Context(), in, []byte(view))
	var refusal *statechart.Refusal
	if out != nil || err == nil || !errors.As(err, &refusal) || refusal.Code != code {
		t.Fatalf("want %s refusal and no chart; got %+v, %v", code, out, err)
	}
	return refusal
}

func reader586Small(states []string, initials []string, steps []tlc.GraphStep) statechart.Input {
	in := reader586Input()
	in.Graph.Graph = tlc.Graph{Initial: initials, Steps: steps}
	in.Invariants = map[string]map[string]bool{}
	for _, id := range states {
		in.Graph.Graph.States = append(in.Graph.Graph.States, tlc.GraphState{ID: id, Label: "phase = " + id, Values: map[string]string{"phase": id}})
		in.Invariants[id] = map[string]bool{"Safe": true}
	}
	in.Graph.Run.Depth = 1
	reader586Counts(&in)
	return in
}

func TestReader586HidingRefusals(t *testing.T) {
	stateRules := `"state":[{"id":"a","when":{"eq":[{"var":"phase"},"1"]},"label":"A"},{"id":"b","when":true,"label":"B"}]`
	for _, tc := range []struct {
		name, code, view, offender string
		in                         statechart.Input
		step                       tlc.GraphStep
	}{
		{"disconnect", "unreachable_chart", `{"version":1,` + stateRules + `,"step":[{"id":"hide-go","when":{"eq":[{"action":true},"Go"]},"label":"Go","hide":true},{"id":"loops","when":true,"label":"Stay"}]}`, "2", reader586Small([]string{"1", "2"}, []string{"1"}, []tlc.GraphStep{{From: "1", Action: "Stay", To: "1"}, {From: "1", Action: "Go", To: "2"}, {From: "2", Action: "Stay", To: "2"}}), tlc.GraphStep{From: "1", Action: "Go", To: "2"}},
		{"last outgoing", "empty_outgoing", `{"version":1,` + stateRules + `,"step":[{"id":"hide-go","when":true,"label":"Go","hide":true}]}`, "1", reader586Small([]string{"1", "2"}, []string{"1", "2"}, []tlc.GraphStep{{From: "1", Action: "Go", To: "2"}}), tlc.GraphStep{From: "1", Action: "Go", To: "2"}},
		{"partial arrow", "partial_arrow", `{"version":1,"state":[{"id":"all","when":true,"label":"Work"}],"step":[{"id":"hide-go","when":{"eq":[{"action":true},"Go"]},"label":"Keep going","hide":true},{"id":"show","when":true,"label":"Keep going"}]}`, "1", reader586Small([]string{"1", "2"}, []string{"1"}, []tlc.GraphStep{{From: "1", Action: "Go", To: "2"}, {From: "2", Action: "Stay", To: "2"}}), tlc.GraphStep{From: "1", Action: "Go", To: "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := reader586Refusal(t, tc.in, tc.view, tc.code)
			if !slices.Contains(r.States, tc.offender) || !slices.Contains(r.Rules, "hide-go") || !slices.Contains(r.Steps, tc.step) {
				t.Fatalf("refusal cannot guide a view repair: %+v", r)
			}
			for _, text := range []string{tc.offender, "hide-go", tc.step.Action} {
				if !strings.Contains(r.Error(), text) {
					t.Errorf("diagnostic omits %q: %v", text, r)
				}
			}
		})
	}
	// Partial visibility within ONE original action must also be refused.
	in := reader586Input()
	view := strings.Replace(reader586View, `{"eq":[{"action":true},"Poll"]}`, `{"all":[{"eq":[{"action":true},"Poll"]},{"eq":[{"from":"worker"},"0"]}]}`, 1)
	view = strings.Replace(view, `{"id":"start"`, `{"id":"other-poll","when":{"eq":[{"action":true},"Poll"]},"label":"Check progress"},{"id":"start"`, 1)
	r := reader586Refusal(t, in, view, "partial_arrow")
	if !slices.Contains(r.Rules, "poll-detail") || !slices.Contains(r.Steps, tlc.GraphStep{From: "20", Action: "Poll", To: "20"}) || !slices.Contains(r.Steps, tlc.GraphStep{From: "21", Action: "Poll", To: "21"}) {
		t.Fatalf("partial members: %+v", r)
	}
	// Genuine terminal states do not need synthetic outgoing arrows.
	terminal := reader586Small([]string{"1", "2"}, []string{"1"}, []tlc.GraphStep{{From: "1", Action: "Go", To: "2"}})
	out := reader586Compute(t, terminal, `{"version":1,`+stateRules+`,"step":[{"id":"go","when":true,"label":"Go"}]}`)
	reader586Accounting(t, terminal, out)
}

func TestReader586UnavailableActions(t *testing.T) {
	out := reader586Compute(t, reader586Input(), reader586View)
	wants := map[string][]string{
		"Ready":    {"Cancel", "Finish", "Poll", "Reset", "Stay"},
		"Working":  {"Reset", "Start", "Stay"}, // Cancel enabled for only one member; Poll hidden
		"Finished": {"Cancel", "Finish", "Poll", "Start"},
	}
	for label, want := range wants {
		s := reader586StateNamed(t, out, label)
		if !reader586SameStrings(s.UnavailableActions, want) {
			t.Errorf("%s unavailable=%v want %v", label, s.UnavailableActions, want)
		}
	}
}

func TestReader586RulesAndTemporalFacts(t *testing.T) {
	in := reader586Input()
	in.Lock.Statements = append(in.Lock.Statements, project.Statement{Name: "Independent", Kind: project.Invariant, Says: "Another rule.", SHA256: strings.Repeat("e", 64)}, project.Statement{Name: "EventuallyDone", Kind: project.Property, Says: "A started job eventually finishes.", SHA256: strings.Repeat("f", 64)})
	for _, row := range in.Invariants {
		row["Independent"] = true
	}
	in.Invariants["20"]["Safe"] = false
	in.Invariants["21"]["Independent"] = false
	in.Temporal = []tlc.TraceFile{{Module: "Lifecycle", Check: "EventuallyDone", Violated: "EventuallyDone", TLC: in.Graph.Run.Version, Loop: 2, States: []tlc.TraceState{
		{Index: 1, Action: "Initial predicate", Vars: map[string]any{"phase": "idle"}, TLA: map[string]string{"phase": `"idle"`}},
		{Index: 2, Action: "Start", Vars: map[string]any{"phase": "busy"}, TLA: map[string]string{"phase": `"busy"`}},
	}}}
	out := reader586Compute(t, in, reader586View)
	reader586Accounting(t, in, out)
	if len(out.States) != 4 {
		t.Fatalf("rule-distinct concrete states merged: %+v", out.States)
	}
	for _, s := range out.States {
		var want []string
		if slices.Contains(s.Members, "20") {
			want = []string{"Safe"}
		}
		if slices.Contains(s.Members, "21") {
			want = []string{"Independent"}
		}
		if !reader586SameStrings(s.ViolatedRules, want) {
			t.Errorf("wrong invariant signature: %+v", s)
		}
	}
	if !reflect.DeepEqual(out.Report.Temporal, in.Temporal) || !reflect.DeepEqual(out.Report.Rules, in.Lock.Statements) {
		t.Fatalf("temporal trace or lock altered: %+v", out.Report)
	}
	noTrace := in
	noTrace.Temporal = nil
	without := reader586Compute(t, noTrace, reader586View)
	if !reflect.DeepEqual(out.States, without.States) || !reflect.DeepEqual(out.Arrows, without.Arrows) {
		t.Fatal("temporal counterexample was converted into a state predicate")
	}
}

func TestReader586TotalityAndEvidenceRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		edit       func(*statechart.Input)
		view       string
	}{
		{"unmapped state", "unmapped_state", nil, strings.Replace(reader586View, `"\"done\""`, `"\"missing\""`, 1)},
		{"unmapped step", "unmapped_step", nil, strings.Replace(reader586View, `"Stay"]`, `"Absent"]`, 1)},
		{"missing variable", "evaluation", nil, strings.Replace(reader586View, `"var":"phase"`, `"var":"missing"`, 1)},
		{"missing state evidence", "invalid_evidence", func(in *statechart.Input) { delete(in.Invariants, "21") }, reader586View},
		{"missing invariant", "invalid_evidence", func(in *statechart.Input) { delete(in.Invariants["21"], "Safe") }, reader586View},
		{"foreign invariant", "invalid_evidence", func(in *statechart.Input) { in.Invariants["21"]["Invented"] = false }, reader586View},
		{"foreign state evidence", "invalid_evidence", func(in *statechart.Input) { in.Invariants["999"] = map[string]bool{"Safe": true} }, reader586View},
		{"bounds mismatch", "invalid_evidence", func(in *statechart.Input) { in.Lock.Bounds["Workers"] = "3" }, reader586View},
		{"false after TLC passed invariant", "invalid_evidence", func(in *statechart.Input) {
			in.Graph.Config.Invariants = []string{"Safe"}
			in.Invariants["20"]["Safe"] = false
		}, reader586View},
		{"unknown temporal rule", "invalid_evidence", func(in *statechart.Input) { in.Temporal = []tlc.TraceFile{{Module: "Lifecycle", Violated: "Invented"}} }, reader586View},
		{"duplicate state", "invalid_graph", func(in *statechart.Input) {
			in.Graph.Graph.States = append(in.Graph.Graph.States, in.Graph.Graph.States[0])
			reader586Counts(in)
		}, reader586View},
		{"bad endpoint", "invalid_graph", func(in *statechart.Input) { in.Graph.Graph.Steps[0].To = "999" }, reader586View},
		{"no initial", "invalid_graph", func(in *statechart.Input) { in.Graph.Graph.Initial = nil }, reader586View},
		{"unknown initial", "invalid_graph", func(in *statechart.Input) { in.Graph.Graph.Initial = []string{"999"} }, reader586View},
		{"wrong count", "invalid_graph", func(in *statechart.Input) { in.Graph.StepCount++ }, reader586View},
		{"failed graph run", "invalid_graph", func(in *statechart.Input) { in.Graph.Run.Outcome = tlc.Failed }, reader586View},
		{"unreachable concrete state", "invalid_graph", func(in *statechart.Input) {
			in.Graph.Graph.States = append(in.Graph.Graph.States, tlc.GraphState{ID: "999", Label: "isolated", Values: map[string]string{"phase": `"done"`, "worker": "9"}})
			in.Invariants["999"] = map[string]bool{"Safe": true}
			reader586Counts(in)
		}, reader586View},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := reader586Input()
			if tc.edit != nil {
				tc.edit(&in)
			}
			r := reader586Refusal(t, in, tc.view, tc.code)
			if tc.name == "unmapped state" && !slices.Contains(r.States, "30") {
				t.Fatalf("lost missing state: %+v", r)
			}
			if tc.name == "unmapped step" && !slices.Contains(r.Steps, tlc.GraphStep{From: "30", Action: "Stay", To: "30"}) {
				t.Fatalf("lost missing step: %+v", r)
			}
		})
	}
}

func TestReader586DeterministicReplay(t *testing.T) {
	in := reader586Input()
	before := reader586JSON(t, in)
	first, err := statechart.Compute(t.Context(), in, []byte(reader586View))
	if err != nil {
		t.Fatal(err)
	}
	want := reader586JSON(t, first)
	slices.Reverse(in.Graph.Graph.States)
	slices.Reverse(in.Graph.Graph.Steps)
	slices.Reverse(in.Graph.Graph.Initial)
	for i := 0; i < 5; i++ {
		got, err := statechart.Compute(t.Context(), in, []byte(reader586View))
		if err != nil || !bytes.Equal(reader586JSON(t, got), want) {
			t.Fatalf("replay %d changed: %v", i, err)
		}
	}
	slices.Reverse(in.Graph.Graph.States)
	slices.Reverse(in.Graph.Graph.Steps)
	slices.Reverse(in.Graph.Graph.Initial)
	if !bytes.Equal(reader586JSON(t, in), before) {
		t.Fatal("computation mutated input")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := statechart.Compute(context.Background(), in, []byte(reader586View))
			if err != nil {
				failures <- err
				return
			}
			b, err := json.Marshal(out)
			if err != nil || !bytes.Equal(b, want) {
				failures <- fmt.Errorf("concurrent replay changed: %v", err)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	in.Graph.Graph.States[0].Values["phase"] = "changed"
	in.Graph.Graph.Steps[0].Action = "changed"
	in.Graph.Config.Constants["Workers"] = "99"
	in.Lock.Statements[1].Says = "changed"
	in.Invariants["10"]["Safe"] = false
	if !bytes.Equal(reader586JSON(t, first), want) {
		t.Fatal("result retained mutable caller evidence")
	}
}
