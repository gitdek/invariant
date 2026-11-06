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
	"github.com/gitdek/invariant/internal/receipt"
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

// Comparison baselines use real complete dumps and replay at their own bounds,
// including the legacy repair path. No saved chart substitutes for those runs.
func TestComparison648PinnedTLC(t *testing.T) {
	tc, err := toolchain.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	old, r, err := Check(t.Context(), workspace(t, nil), tc)
	if err != nil || r == nil || !r.Passed {
		t.Fatalf("baseline: %+v %v", r, err)
	}
	ws := workspace(t, map[string]func(string) string{
		"view.json":     replace("Buffer", "Queued buffer"),
		"proposal.json": replace(`"Cap": "2"`, `"Cap": "1"`),
	})
	cur := &Current{Dir: "examples/buffer", Manifest: old.Manifest(), ModuleText: old.ModuleText, Includes: old.Includes,
		Lock: project.Lock{Ratified: &project.Ratification{Issue: 571, By: "owner"}, Bounds: old.Bounds, Statements: old.Statements}}
	authority := (Request{Current: cur}).CheckContext()
	if _, _, err := CheckWithContext(t.Context(), ws, tc, authority); err == nil || !strings.Contains(err.Error(), "baseline/view.json") {
		t.Fatalf("missing legacy view: %v", err)
	}
	if err := writeFile(filepath.Join(ws, "baseline/view.json"), old.View); err != nil {
		t.Fatal(err)
	}
	p, r, err := CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil || r == nil || !r.Passed {
		t.Fatalf("comparison: %+v %v", r, err)
	}
	c := p.Reader.Comparison
	if c == nil || !c.BoundsChanged || !c.ViewChanged || c.Scope == "" || c.Baseline.Chart.Report.Counts["tlc_states"] != 7 || c.Candidate.Chart.Report.Counts["tlc_states"] != 3 || c.Baseline.Chart.Report.Source.Tool.JarSHA256 != toolchain.TLCJarSHA256 {
		t.Fatalf("wrong bounded comparison: %+v", c)
	}
	cur.View = old.View
	if err := p.Amend(cur); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := p.Write(dir, "comparison", nil); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, p.Manifest().Module), p.ModuleText+"\\* independently synthesized source\n"); err != nil {
		t.Fatal(err)
	}
	r, err = verify.RunModel(t.Context(), dir, tc)
	if err != nil || !r.Passed || r.Reader.Comparison == nil || r.Reader.Comparison.Fingerprint != c.Fingerprint {
		t.Fatalf("retained receipt: %+v %v", r, err)
	}
	if err := receipt.Write(t.TempDir(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SVG(); err != nil {
		t.Fatal(err)
	}
}
