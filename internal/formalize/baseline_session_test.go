package formalize_test

import (
	"context"
	"sync"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
)

func TestBaselineSessionConcurrentColdChecks(t *testing.T) {
	tc, log := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	p, err := formalize.Read(ws)
	if err != nil {
		t.Fatal(err)
	}
	model := p.ModuleText + "\\* concurrent baseline\n"
	c := (formalize.Request{Current: &formalize.Current{Dir: "examples/reader", Manifest: p.Manifest(),
		ModuleText: model, Includes: p.Includes, View: p.View,
		Lock: project.Lock{Bounds: p.Bounds, Statements: p.Statements}}}).CheckContext()
	// Captured or returned artifacts never supply the session's evidence.
	c.Baseline.Chart = &statechart.Result{}
	session := formalize.NewCheckSession(c)
	c.Baseline.Model = "changed caller memory"
	c.Baseline.Lock.Bounds["Limit"] = "999"
	const calls = 6
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]*formalize.Proposal, calls)
	errors := make([]error, calls)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], _, errors[i] = session.Check(t.Context(), ws, tc)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, p := range results {
		if errors[i] != nil || p == nil {
			t.Fatalf("concurrent check %d: %v", i, errors[i])
		}
		if err := p.ValidateReader(); err != nil {
			t.Fatal(err)
		}
		if p.Reader.Comparison.Baseline.Model != model || p.Reader.Comparison.Baseline.Lock.Bounds["Limit"] != "2" {
			t.Fatal("session used changed caller memory")
		}
	}
	results[0].Reader.Comparison.Baseline.Chart.Report.Counts["tlc_states"] = 999
	for _, p := range results[1:] {
		if p.Reader.Comparison.Baseline.Chart.Report.Counts["tlc_states"] != 3 {
			t.Fatal("concurrent callers share chart evidence")
		}
	}
	var gate, graph, replay, candidates int
	for _, call := range reader603Calls(t, log) {
		if call["main"] == reader603Model && call["dump"] == "" {
			candidates++
		}
		if call["main"] != model {
			continue
		}
		switch {
		case call["dump"] == "":
			gate++
		case call["module"] == "Reader603":
			graph++
		default:
			replay++
		}
	}
	if gate != 1 || graph != 1 || replay != 1 || candidates != calls {
		t.Fatalf("cold concurrent work: baseline %d/%d/%d, candidates %d", gate, graph, replay, candidates)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := session.Check(ctx, ws, tc); err != context.Canceled {
		t.Fatalf("canceled warm check: %v", err)
	}
}
