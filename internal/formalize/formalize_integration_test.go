//go:build integration

package formalize

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
