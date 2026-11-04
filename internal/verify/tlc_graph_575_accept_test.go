package verify_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Ordinary acceptance runs always exercise the new parser. The optional
// branch is also called by TestGraph575RealIntegration in the planned new
// tlc_graph_integration_test.go, tagged integration, so CI's existing real
// verifier shards run it without an environment opt-in. That bridge is:
//
//	func TestGraph575RealIntegration(t *testing.T) { graph575Real(t) }
//
// Local real-tool command:
// INVARIANT_TLC_GRAPH_REAL=1 go test ./internal/verify -run '^TestGraph575PinnedTLC$' -count=1 -timeout 5m
func TestGraph575PinnedTLC(t *testing.T) {
	const dot = `strict digraph DiskGraph {
nodesep=0.35;
subgraph cluster_graph {
color="white";
-39061 [label="/\\ buf = <<>>\n/\\ n = 0",style = filled]
-39061 -> 1090 [label="Write",color="black",fontcolor="black"];
1090 [label="/\\ buf = <<\"a\">>\n/\\ n = 1"];
1090 -> 2207 [label="Write",color="black",fontcolor="black"];
2207 [label="/\\ buf = <<\"a\", \"b\">>\n/\\ n = 2"];
2207 -> 2207 [label="Stay",color="black",fontcolor="black"];
1090 -> -39061 [label="Ship",color="black",fontcolor="black"];
}
}`
	g, err := tlc.ParseGraphDOT(dot)
	if err != nil || g == nil || len(g.States) != 3 || len(g.Initial) != 1 || len(g.Steps) != 4 {
		t.Fatalf("pinned-format fixture: %+v, %v", g, err)
	}
	if os.Getenv("INVARIANT_TLC_GRAPH_REAL") == "1" {
		graph575Real(t)
	}
}

func graph575Real(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err) // Opted-in and integration runs must not silently skip.
	}
	dir := t.TempDir()
	const model = `---- MODULE ReaderGraph575 ----
EXTENDS Naturals, Sequences
CONSTANT Limit
VARIABLES n, payload
vars == <<n, payload>>
Init == /\ n \in {0, 1}
        /\ payload = [message |-> "a  b \"quoted\" \\path",
                       sequence |-> <<1, TRUE, [ok |-> FALSE]>>,
                       members |-> {1, 2},
                       mapping |-> [i \in {1, 2} |-> <<i>>]]
Advance == /\ n < Limit
           /\ n' = n + 1
           /\ UNCHANGED payload
Alternate == /\ n < Limit
             /\ n' = n + 1
             /\ UNCHANGED payload
Stay == UNCHANGED vars
Reset == /\ n = Limit
         /\ n' = 0
         /\ UNCHANGED payload
Next == Advance \/ Alternate \/ Stay \/ Reset
Spec == Init /\ [][Next]_vars
====
`
	if err := os.WriteFile(filepath.Join(dir, "ReaderGraph575.tla"), []byte(model), 0644); err != nil {
		t.Fatal(err)
	}
	r := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	cfg := tlc.Config{Specification: "Spec", Constants: map[string]string{"Limit": "2"}}
	g, err := r.DumpGraph(ctx, dir, "ReaderGraph575", cfg)
	if err != nil || g == nil {
		t.Fatalf("real pinned TLC dump: %+v, %v", g, err)
	}
	if g.Run.Outcome != tlc.Passed || g.Run.DistinctStates != 3 || g.Run.StatesGenerated != 10 || g.Run.Depth != 2 || g.StateCount != 3 || g.StepCount != 8 {
		t.Fatalf("wrong completed statistics or graph counts: %+v", g)
	}
	if g.Tool.Image != toolchain.JavaImage || g.Tool.JarSHA256 != toolchain.TLCJarSHA256 || !strings.Contains(g.Run.Version, "TLC2 Version 2.19") || g.Module != "ReaderGraph575" || !reflect.DeepEqual(g.Config, cfg) {
		t.Fatalf("wrong actual tool/configuration provenance: %+v", g)
	}
	byID := map[string]string{}
	payload := ""
	values := map[string]bool{}
	for _, s := range g.Graph.States {
		if s.ID == "" || s.Label == "" || len(s.Values) != 2 {
			t.Fatalf("real state lost its identity or variables: %+v", s)
		}
		byID[s.ID] = s.Values["n"]
		values[s.Values["n"]] = true
		if payload == "" {
			payload = s.Values["payload"]
		}
		if s.Values["payload"] != payload || !strings.Contains(s.Label, payload) || !strings.Contains(payload, `"a  b \"quoted\" \\path"`) || !strings.Contains(payload, "<<") || !strings.Contains(payload, "|->") {
			t.Fatalf("real structured value changed or lost escapes/spacing: %+v", s)
		}
	}
	if len(byID) != 3 || !reflect.DeepEqual(values, map[string]bool{"0": true, "1": true, "2": true}) {
		t.Fatalf("real states collapsed: %v", byID)
	}
	initial := map[string]int{}
	for _, id := range g.Graph.Initial {
		initial[byID[id]]++
	}
	if !reflect.DeepEqual(initial, map[string]int{"0": 1, "1": 1}) {
		t.Fatalf("real dump lost initial states: %v", initial)
	}
	type step struct{ from, action, to string }
	got := map[step]int{}
	for _, e := range g.Graph.Steps {
		got[step{byID[e.From], e.Action, byID[e.To]}]++
	}
	want := map[step]int{
		{"0", "Advance", "1"}: 1, {"0", "Alternate", "1"}: 1,
		{"1", "Advance", "2"}: 1, {"1", "Alternate", "2"}: 1,
		{"0", "Stay", "0"}: 1, {"1", "Stay", "1"}: 1, {"2", "Stay", "2"}: 1,
		{"2", "Reset", "0"}: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("real dump lost loops or distinct actions on identical endpoints: %v; want %v", got, want)
	}
	// Cross-check the unchanged raw-DOT caller on a fresh pinned run.
	dot, err := r.Dump(ctx, dir, "ReaderGraph575", cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tlc.ParseGraphDOT(dot)
	if err != nil || raw == nil || len(raw.States) != g.StateCount || len(raw.Steps) != g.StepCount {
		t.Fatalf("legacy caller disagrees with complete result: %+v, %v", raw, err)
	}
}
