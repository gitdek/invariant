package verify_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// The builder adds internal/verify/statechart_integration_test.go, tagged
// integration, in package verify_test, with:
// func TestReader586RealIntegration(t *testing.T) { reader586Real(t) }
// This is discovered by the existing verifier integration shards. Locally:
// INVARIANT_STATECHART_REAL=1 go test ./internal/verify -run '^TestReader586PinnedTLC$' -count=1 -timeout 5m
// Default acceptance is offline but still calls the new computation API.
func TestReader586PinnedTLC(t *testing.T) {
	g, err := tlc.ParseGraphDOT(`strict digraph DiskGraph { subgraph cluster_graph {
1 [label="phase = 0",style=filled];
1 -> 2 [label="Begin"];
2 [label="phase = 1"];
2 -> 2 [label="Stay"];
}}`)
	if err != nil {
		t.Fatal(err)
	}
	in := statechart.Input{Graph: tlc.GraphResult{Graph: *g, StateCount: 2, StepCount: 2, Module: "Reader586", Config: tlc.Config{Specification: "Spec", Constants: map[string]string{"Limit": "1"}}, Tool: tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)}, Run: tlc.Result{Outcome: tlc.Passed, Version: "TLC2 Version 2.19", DistinctStates: 2, StatesGenerated: 3, Depth: 2}}, Lock: project.Lock{Bounds: map[string]string{"Limit": "1"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "Begin and stay.", SHA256: strings.Repeat("c", 64)}}}, Invariants: map[string]map[string]bool{"1": {}, "2": {}}}
	out, err := statechart.Compute(t.Context(), in, []byte(`{"version":1,"state":[{"id":"all","when":true,"label":"Work"}],"step":[{"id":"all","when":true,"label":"Continue"}]}`))
	if err != nil || out == nil {
		t.Fatalf("offline computation: %+v %v", out, err)
	}
	if os.Getenv("INVARIANT_STATECHART_REAL") == "1" {
		reader586Real(t)
	}
}

func reader586Real(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	} // Opt-in/CI cannot skip missing tools.
	const model = `---- MODULE Reader586 ----
EXTENDS Naturals, Sequences
CONSTANT Limit
VARIABLES n, payload
vars == <<n, payload>>
Init == /\ n = 0
        /\ payload = [name |-> "a  b", sequence |-> <<1, TRUE>>, members |-> {1, 2}]
Advance == /\ n < Limit
           /\ n' = n + 1
           /\ UNCHANGED payload
Stay == UNCHANGED vars
Next == Advance \/ Stay
Spec == Init /\ [][Next]_vars
AvoidOne == n # 1
AvoidTwo == n # 2
PayloadShape == /\ payload.sequence = <<1, TRUE>>
                /\ payload.members = {1, 2}
                /\ payload.name = "a  b"
====
`
	dir := t.TempDir()
	source := filepath.Join(dir, "Reader586.tla")
	if err := os.WriteFile(source, []byte(model), 0600); err != nil {
		t.Fatal(err)
	}
	lock := project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "Advance or stay."},
		{Name: "AvoidOne", Kind: project.Invariant, Says: "One is forbidden."},
		{Name: "AvoidTwo", Kind: project.Invariant, Says: "Two is forbidden."},
		{Name: "PayloadShape", Kind: project.Invariant, Says: "The payload keeps its exact structure."},
	}}
	for i := range lock.Statements {
		s := &lock.Statements[i]
		s.SHA256, err = tla.PinHash(model, s.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	// The graph explores the model without stopping at its first bad rule.
	// These diagnostic runs do not replace or weaken the verification gate.
	g, err := runner.DumpGraph(ctx, dir, "Reader586", tlc.Config{Specification: "Spec", Constants: lock.Bounds})
	if err != nil || g == nil {
		t.Fatalf("complete graph: %+v %v", g, err)
	}
	if g.StateCount != 3 || g.StepCount != 5 {
		t.Fatalf("real TLC graph counts: %+v", g)
	}
	matrix, err := statechart.EvaluateRules(ctx, runner, dir, *g, lock)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range g.Graph.States {
		want := map[string]bool{"AvoidOne": s.Values["n"] != "1", "AvoidTwo": s.Values["n"] != "2", "PayloadShape": true}
		if !reflect.DeepEqual(matrix[s.ID], want) {
			t.Fatalf("TLC rule truth at %s (%v): %v want %v", s.ID, s.Values, matrix[s.ID], want)
		}
	}
	view := []byte(`{"version":1,"state":[{"id":"same-name","when":true,"label":"Stage"}],"step":[{"id":"every-step","when":true,"label":"Continue"}]}`)
	out, err := statechart.Compute(ctx, statechart.Input{Graph: *g, Lock: lock, Invariants: matrix}, view)
	if err != nil || out == nil {
		t.Fatalf("real reader computation: %+v %v", out, err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		States []struct {
			Members  []string `json:"members"`
			Violated []string `json:"violated_rules"`
		} `json:"states"`
		Report struct {
			Counts map[string]int `json:"counts"`
		} `json:"report"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.States) != 3 || decoded.Report.Counts["tlc_steps"] != 5 || decoded.Report.Counts["shown_steps"] != 5 {
		t.Fatalf("real rules merged or steps lost: %s", b)
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != model {
		t.Fatal("rule evaluation modified the original module")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "Reader586.tla" {
		t.Fatalf("rule evaluation left artifacts: %v %v", entries, err)
	}
}
