//go:build integration

package formalize

import (
	"context"
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
