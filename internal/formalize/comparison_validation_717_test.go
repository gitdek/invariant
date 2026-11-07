package formalize

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

func validation717Proposal(t testing.TB, n int) *Proposal {
	t.Helper()
	p := &Proposal{Draft: Draft{Name: "Validation", Slug: "validation", Module: "Validation", Package: "validation",
		Bounds: map[string]string{"Limit": fmt.Sprint(n)}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "Advance or poll."}, {Name: "Safe", Kind: project.Invariant, Says: "Stay within the limit."}}},
		ModuleText: "---- MODULE Validation ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nvars == <<n>>\nInit == n = 0\nAdvance == n' = (n + 1) % Limit\nPoll == UNCHANGED n\nNext == Advance \\/ Poll\nSafe == n < Limit\nSpec == Init /\\ [][Next]_vars\n====\n",
		Includes:   map[string]string{"_Helper.tla": "---- MODULE _Helper ----\nValue == 1\n====\n"}}
	if err := p.Pin(); err != nil {
		t.Fatal(err)
	}
	g := tlc.GraphResult{Module: p.Module, Config: tlc.Config{Specification: "Spec", Invariants: []string{"Safe"}, Constants: p.Bounds},
		Tool:       tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)},
		Run:        tlc.Result{Outcome: tlc.Passed, Version: "TLC fixture", DistinctStates: int64(n), StatesGenerated: int64(3*n + 1), Depth: n},
		StateCount: n, StepCount: 3 * n, Graph: tlc.Graph{Initial: []string{"0"}}}
	var states []map[string]any
	invariants := map[string]map[string]bool{}
	for i := 0; i < n; i++ {
		id := fmt.Sprint(i)
		g.Graph.States = append(g.Graph.States, tlc.GraphState{ID: id, Label: "n = " + id, Values: map[string]string{"n": id}})
		g.Graph.Steps = append(g.Graph.Steps, tlc.GraphStep{From: id, To: fmt.Sprint((i + 1) % n), Action: "Advance"},
			tlc.GraphStep{From: id, To: id, Action: "Poll"}, tlc.GraphStep{From: id, To: id, Action: "Poll"})
		invariants[id] = map[string]bool{"Safe": true}
		states = append(states, map[string]any{"id": "s" + id, "when": map[string]any{"eq": []any{map[string]any{"var": "n"}, id}}, "label": "State " + id})
	}
	view, err := json.Marshal(map[string]any{"version": 1, "state": states, "step": []map[string]any{
		{"id": "poll", "when": map[string]any{"eq": []any{map[string]any{"action": true}, "Poll"}}, "label": "Poll", "hide": true},
		{"id": "advance", "when": true, "label": "Advance"}}})
	if err != nil {
		t.Fatal(err)
	}
	p.View = string(view)
	chart, err := statechart.Compute(context.Background(), statechart.Input{Graph: g, Lock: project.Lock{Bounds: p.Bounds, Statements: p.Statements}, Invariants: invariants}, view)
	if err != nil {
		t.Fatal(err)
	}
	p.Reader = &Reader{Version: 1, Chart: chart}
	current := &Current{Dir: "examples/validation", Manifest: p.Manifest(), ModuleText: p.ModuleText, Includes: p.Includes, View: p.View,
		Lock: project.Lock{Ratified: &project.Ratification{Issue: 717, By: "owner", Proposal: p.Hash}, Bounds: p.Bounds, Statements: p.Statements}}
	if err := p.Amend(current); err != nil {
		t.Fatal(err)
	}
	baseline := currentSnapshot(current)
	baseline.Chart = chart
	p.Baseline = detachedSnapshot(baseline)
	p.Reader.Comparison, err = statechart.Compare(*baseline, p.comparisonSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	validation717Seal(t, p)
	if err := p.ValidateReader(); err != nil {
		t.Fatal(err)
	}
	return p
}

func validation717Seal(t testing.TB, p *Proposal) {
	t.Helper()
	var err error
	p.Reader.Fingerprint, err = p.readerFingerprint()
	if err != nil {
		t.Fatal(err)
	}
}

func TestComparison717CandidateOrderingAndBaselineBindings(t *testing.T) {
	p := validation717Proposal(t, 4)
	before, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	clone := func(t *testing.T) *Proposal {
		t.Helper()
		var q Proposal
		if err := json.Unmarshal(before, &q); err != nil {
			t.Fatal(err)
		}
		if err := q.ValidateReader(); err != nil {
			t.Fatal(err)
		}
		return &q
	}
	t.Run("candidate order", func(t *testing.T) {
		q := clone(t)
		slices.Reverse(q.Reader.Chart.States)
		slices.Reverse(q.Reader.Chart.Arrows)
		slices.Reverse(q.Reader.Chart.Report.Hidden)
		for i := range q.Reader.Chart.Report.Hidden {
			slices.Reverse(q.Reader.Chart.Report.Hidden[i].Members)
		}
		validation717Seal(t, q)
		if err := q.ValidateReader(); err != nil {
			t.Fatalf("ordering changed candidate identity: %v", err)
		}
	})
	mutations := map[string]func(*statechart.ComparisonSnapshot){
		"kind":   func(s *statechart.ComparisonSnapshot) { s.Kind = "proposal" },
		"name":   func(s *statechart.ComparisonSnapshot) { s.Name += " changed" },
		"module": func(s *statechart.ComparisonSnapshot) { s.Module = "Other" },
		"model":  func(s *statechart.ComparisonSnapshot) { s.Model += "\n" },
		"include name": func(s *statechart.ComparisonSnapshot) {
			s.Includes["Other.tla"] = s.Includes["_Helper.tla"]
			delete(s.Includes, "_Helper.tla")
		},
		"include bytes":     func(s *statechart.ComparisonSnapshot) { s.Includes["_Helper.tla"] += "\n" },
		"view":              func(s *statechart.ComparisonSnapshot) { s.View += "\n" },
		"bounds":            func(s *statechart.ComparisonSnapshot) { s.Lock.Bounds["Limit"] = "5" },
		"rule words":        func(s *statechart.ComparisonSnapshot) { s.Lock.Statements[0].Says += " changed" },
		"rule pin":          func(s *statechart.ComparisonSnapshot) { s.Lock.Statements[0].SHA256 += "changed" },
		"decision":          func(s *statechart.ComparisonSnapshot) { s.Lock.Decision = "D-0345" },
		"ratifier":          func(s *statechart.ComparisonSnapshot) { s.Lock.Ratified.By = "another" },
		"issue":             func(s *statechart.ComparisonSnapshot) { s.Lock.Ratified.Issue++ },
		"comment":           func(s *statechart.ComparisonSnapshot) { s.Lock.Ratified.Comment = "another" },
		"ratified proposal": func(s *statechart.ComparisonSnapshot) { s.Lock.Ratified.Proposal += "changed" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			q := clone(t)
			mutate(q.Baseline)
			validation717Seal(t, q)
			if err := q.ValidateReader(); err == nil {
				t.Fatal("valid target concealed a changed carried baseline")
			}
			if err := q.ValidateComparisonContext(CheckContext{Baseline: q.Baseline}); err == nil {
				t.Fatal("standalone validation trusted the earlier baseline")
			}
		})
	}
	for name, mutate := range map[string]func(*Current){
		"commit":    func(c *Current) { c.Commit = strings.Repeat("a", 40) },
		"directory": func(c *Current) { c.Dir = "examples/other" },
		"module":    func(c *Current) { c.Manifest.Module = ".invariant/specs/Other.tla" },
		"model":     func(c *Current) { c.ModuleText += "\n" },
		"view":      func(c *Current) { c.View += "\n" },
		"includes":  func(c *Current) { c.Includes["_Helper.tla"] += "\n" },
		"lock":      func(c *Current) { c.Lock.Ratified.Comment = "another" },
	} {
		t.Run("target "+name, func(t *testing.T) {
			q := clone(t)
			mutate(q.Target.Baseline)
			validation717Seal(t, q)
			if err := q.ValidateReader(); err == nil {
				t.Fatal("valid carried baseline concealed a changed target")
			}
		})
	}
	after, _ := json.Marshal(p)
	if !bytes.Equal(before, after) {
		t.Fatal("validation changed the retained proposal")
	}
}

// This is the original checkout's validation path, retained only to measure
// the four comparison recomputations against the candidate on identical data.
func validation717Base(p *Proposal) error {
	r := p.Reader
	if r == nil || p.View == "" || r.Version != 1 || (r.Chart == nil) == (r.Limitation == nil) {
		return fmt.Errorf("missing reader")
	}
	if r.Chart != nil && r.Chart.Version != 1 {
		return fmt.Errorf("unsupported chart")
	}
	if l := r.Limitation; l != nil && (l.Code != "too_large" || l.Limit != statechart.MaxStates || l.Actual <= l.Limit) {
		return fmt.Errorf("invalid limitation")
	}
	want, err := p.readerFingerprint()
	if err != nil || r.Fingerprint != want {
		return fmt.Errorf("stale reader: %v", err)
	}
	if c := r.Comparison; c != nil {
		if err := c.Validate(); err != nil {
			return err
		}
		want, err := statechart.Compare(c.Baseline, p.comparisonSnapshot())
		if err != nil {
			return err
		}
		if want.Fingerprint != c.Fingerprint {
			return fmt.Errorf("stale candidate")
		}
	}
	if p.Target != nil && p.Target.Baseline != nil {
		if err := p.ValidateComparisonContext(CheckContext{Baseline: currentSnapshot(p.Target.Baseline)}); err != nil {
			return err
		}
	}
	if p.Baseline != nil {
		if err := p.ValidateComparisonContext(CheckContext{Baseline: p.Baseline}); err != nil {
			return err
		}
	}
	return validateIncludes(p.Includes, p.Module)
}

func BenchmarkComparison717ValidateReader(b *testing.B) {
	for _, n := range []int{4, 100, 500} {
		p := validation717Proposal(b, n)
		for _, run := range []struct {
			name     string
			validate func(*Proposal) error
		}{{"base", validation717Base}, {"candidate", (*Proposal).ValidateReader}} {
			b.Run(fmt.Sprintf("%d/%s", n, run.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := run.validate(p); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
