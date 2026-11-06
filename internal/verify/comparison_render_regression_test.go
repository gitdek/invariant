package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

const comparisonRenderView = `{"version":1,"state":[{"id":"ready","when":{"eq":[{"var":"n"},"0"]},"label":"Ready"},{"id":"rest","when":true,"label":"Working"}],"step":[{"id":"all","when":true,"label":"Move"}]}`

func comparisonRenderSnapshot(t *testing.T, n int) statechart.ComparisonSnapshot {
	t.Helper()
	model := fmt.Sprintf("---- MODULE Render648 ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nvars == <<n>>\nInit == n = 0\nAdvance == /\\ n = 0 /\\ n' = %d\nReset == /\\ n = %d /\\ n' = 0\nNext == Advance \\/ Reset\nSpec == Init /\\ [][Next]_vars\n====\n", n, n)
	pin, err := tla.PinHash(model, "Spec", project.Model)
	if err != nil {
		t.Fatal(err)
	}
	lock := project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "Advance or reset.", SHA256: pin}}}
	id := fmt.Sprint(n)
	g := tlc.GraphResult{Module: "Render648", Config: tlc.Config{Specification: "Spec", Constants: lock.Bounds}, Tool: tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)},
		Run: tlc.Result{Outcome: tlc.Passed, Version: "TLC fixture", DistinctStates: 2, StatesGenerated: 3, Depth: 2}, StateCount: 2, StepCount: 2,
		Graph: tlc.Graph{States: []tlc.GraphState{{ID: "0", Label: "n = 0", Values: map[string]string{"n": "0"}}, {ID: id, Label: "n = " + id, Values: map[string]string{"n": id}}}, Initial: []string{"0"},
			Steps: []tlc.GraphStep{{From: "0", To: id, Action: "Advance"}, {From: id, To: "0", Action: "Reset"}}}}
	c, err := statechart.Compute(t.Context(), statechart.Input{Graph: g, Lock: lock, Invariants: map[string]map[string]bool{"0": {}, id: {}}}, []byte(comparisonRenderView))
	if err != nil {
		t.Fatal(err)
	}
	return statechart.ComparisonSnapshot{Kind: "proposal", Name: fmt.Sprintf("Proposal %d", n), Module: "Render648", Model: model, View: comparisonRenderView, Includes: map[string]string{}, Lock: lock, Chart: c}
}

// Damaged comparison evidence becomes a printable diagnostic, so the
// independently checked final chart beside it still draws (#648).
func TestDamagedComparisonKeepsTheFinalChartDrawable(t *testing.T) {
	a, b := comparisonRenderSnapshot(t, 1), comparisonRenderSnapshot(t, 2)
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	damaged := bytes.Replace(raw, []byte(`"name":"Spec"`), []byte(`"name":"Spec\u0001"`), 1)
	if bytes.Equal(damaged, raw) {
		t.Fatal("the fixture has no statement to damage")
	}
	r := &Reader{Version: 1, Chart: b.Chart}
	r.captureComparison(damaged, nil, b.Lock)
	if r.Comparison != nil || r.ComparisonDiagnostic == nil || strings.ContainsRune(r.ComparisonDiagnostic.Message, '\x01') {
		t.Fatalf("damaged evidence wasn't a printable diagnostic: %+v", r.ComparisonDiagnostic)
	}
	svg, err := r.SVG()
	if err != nil || !strings.Contains(svg, "Proposal comparison unavailable") {
		t.Fatalf("the final chart didn't draw beside the diagnostic: %v", err)
	}
}
