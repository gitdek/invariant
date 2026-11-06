//go:build integration

package formalize

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// The factory checks a draft with the gate's model checks before asking
// anyone to ratify it.
func TestCheckRunsTheModelChecks(t *testing.T) {
	ctx := context.Background()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, r, err := Check(ctx, workspace(t, nil), tc)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed || !r.ModelOnly || r.Design.DistinctStates != 7 || !r.Witnesses[0].Reached || !r.Bugs[0].Caught {
		t.Fatalf("the draft should check out: %s", verify.Feedback(r))
	}
	if err := p.ValidateReader(); err != nil {
		t.Fatal(err)
	}
	chart := p.Reader.Chart
	if chart == nil || chart.Report.Counts["tlc_states"] != 7 || chart.Report.Counts["tlc_steps"] != 12 ||
		chart.Report.Counts["tlc_generated_states"] != 13 || len(chart.Initial) != 1 ||
		chart.Report.Source.Tool.Image != tc.JavaImage || chart.Report.Source.Tool.JarSHA256 != toolchain.TLCJarSHA256 {
		t.Fatalf("the checked reader must retain the complete pinned-TLC graph: %+v", chart)
	}
	if len(chart.Report.Rules) != len(p.Statements) || chart.Report.Rules[1].Says != p.Statements[1].Says {
		t.Fatal("reader lost the checked rule wording")
	}

	// A model that lets a producer overfill the buffer breaks WithinCap.
	_, r, err = Check(ctx, workspace(t, map[string]func(string) string{"BoundedBuffer.tla": replace("Put(m) == Len(buf) < Cap /\\ ", "Put(m) == ")}), tc)
	if err != nil {
		t.Fatal(err)
	}
	if r.Passed || !strings.Contains(verify.Feedback(r), "Invariant WithinCap is violated") {
		t.Fatalf("an overfilling model must fail: %s", verify.Feedback(r))
	}
}

// Compare complete pinned-TLC graphs at both original scopes, including a
// repaired legacy baseline. No old source or bound comes from staged files.
func TestComparison648PinnedTLC(t *testing.T) {
	tc, err := toolchain.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	oldWS := workspace(t, map[string]func(string) string{"proposal.json": replace(`"Cap": "2"`, `"Cap": "1"`)})
	old, r, err := Check(t.Context(), oldWS, tc)
	if err != nil || r == nil || !r.Passed {
		t.Fatalf("baseline: %+v %v", r, err)
	}
	ws := workspace(t, nil)
	authority := (Request{Previous: old}).CheckContext()
	p, r, err := CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil || r == nil || !r.Passed {
		t.Fatalf("candidate: %+v %v", r, err)
	}
	if err := p.ValidateComparisonContext(authority); err != nil {
		t.Fatal(err)
	}
	c := p.Reader.Comparison
	if !c.BoundsChanged || c.Scope == "" || c.Baseline.Chart.Report.Counts["tlc_states"] != 3 || c.Candidate.Chart.Report.Counts["tlc_states"] != 7 ||
		len(c.Visible.ChangedStates) == 0 || len(c.BeforeHiding.ChangedArrows) == 0 {
		t.Fatalf("lost complete scopes or occurrences: %+v", c)
	}
	for _, side := range []string{c.Baseline.Chart.Report.Source.Tool.JarSHA256, c.Candidate.Chart.Report.Source.Tool.JarSHA256} {
		if side != toolchain.TLCJarSHA256 {
			t.Fatal("comparison did not use pinned TLC")
		}
	}
	if _, err := c.SVG(); err != nil {
		t.Fatal(err)
	}
	cur := &Current{Dir: "examples/buffer", Manifest: old.Manifest(), ModuleText: old.ModuleText, Includes: old.Includes,
		Lock: project.Lock{Ratified: &project.Ratification{Issue: 571, By: "owner"}, Bounds: old.Bounds, Statements: old.Statements}}
	legacy := (Request{Current: cur}).CheckContext()
	if _, _, err := CheckWithContext(t.Context(), ws, tc, legacy); err == nil || !strings.Contains(err.Error(), "baseline/view.json") {
		t.Fatalf("missing legacy repair: %v", err)
	}
	if err := writeFile(filepath.Join(ws, "baseline/view.json"), strings.Replace(old.View, `"label":"Update buffer"`, `"label":"Update buffer","hide":true`, 1)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CheckWithContext(t.Context(), ws, tc, legacy); err == nil || !strings.Contains(err.Error(), "baseline") {
		t.Fatalf("invalid baseline hiding accepted: %v", err)
	}
	if err := writeFile(filepath.Join(ws, "baseline/view.json"), old.View); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(ws, "baseline/ratified.lock"), `{"bounds":{"Cap":"99"}}`); err != nil {
		t.Fatal(err)
	}
	q, r, err := CheckWithContext(t.Context(), ws, tc, legacy)
	if err != nil || r == nil || !r.Passed {
		t.Fatalf("legacy repair: %+v %v", r, err)
	}
	if q.Reader.Comparison.Baseline.Lock.Bounds["Cap"] != "1" || q.Reader.Comparison.Baseline.Chart.Report.Counts["tlc_states"] != 3 {
		t.Fatal("legacy repair changed captured scope")
	}
}

// Alternatives use the real pinned tools, with distinct bounded graphs.
func TestPreview639PinnedTLC(t *testing.T) {
	tc, err := toolchain.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fork := Fork{ID: "F1", Question: "How much may the buffer hold?"}
	for i, id := range []string{"A", "B"} {
		m, err := Read(workspace(t, nil))
		if err != nil {
			t.Fatal(err)
		}
		m.Bounds["Cap"] = []string{"1", "2"}[i]
		m.Statements[1].Says = "Candidate " + id + " keeps its own capacity."
		fork.Options = append(fork.Options, Option{ID: id, Says: "Use capacity " + m.Bounds["Cap"] + ".",
			Preview: &Preview{Fork: fork.ID, Option: id, Model: m}})
	}
	ws := t.TempDir()
	b, err := json.Marshal(Draft{Forks: []Fork{fork}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "proposal.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	p, r, err := Check(t.Context(), ws, tc)
	if err != nil || r != nil {
		t.Fatalf("alternatives: %v %+v", err, r)
	}
	if p.Ratifiable() {
		t.Fatal("alternatives became ratifiable")
	}
	if err := p.ValidatePreviews(); err != nil {
		t.Fatal(err)
	}
	for i, o := range p.Forks[0].Options {
		v := o.Preview
		c := v.Model.Reader.Chart
		if c == nil || c.Report.Counts["tlc_states"] != []int64{3, 7}[i] ||
			c.Report.Source.Tool.JarSHA256 != toolchain.TLCJarSHA256 ||
			c.Preview.Fork != "F1" || c.Preview.Option != o.ID {
			t.Fatalf("wrong alternative graph: %+v", c)
		}
	}
	if err := p.WritePreviews(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
