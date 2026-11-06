package formalize_test

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
)

func TestComparisonFailedAmendmentRetainsCapturedAuthority(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _ := reader603Checked(t, ws, tc)
	cur := &formalize.Current{Dir: "examples/reader", Commit: strings.Repeat("a", 40), Manifest: old.Manifest(), ModuleText: old.ModuleText, Includes: old.Includes,
		Lock: project.Lock{Ratified: &project.Ratification{Issue: 571, By: "owner"}, Bounds: old.Bounds, Statements: old.Statements}}
	a := reader603Agent{run: func(_ context.Context, job synth.Job) (synth.Usage, error) {
		reader603Workspace(t, job.Workspace)
		return synth.Usage{}, nil // legacy baseline repair deliberately missing
	}}
	r, err := (formalize.Formalizer{Backend: a, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Current: cur}, t.TempDir())
	if err != nil || r == nil || !strings.Contains(r.Problem, "baseline/view.json") || r.Proposal == nil || r.Proposal.Target == nil || r.Proposal.Target.Baseline == nil {
		t.Fatalf("failed amendment lost baseline: %+v %v", r, err)
	}
	if !reflect.DeepEqual(r.Proposal.Target.Baseline.Lock, cur.Lock) || r.Proposal.Target.Baseline.Commit != cur.Commit {
		t.Fatal("failure changed captured baseline")
	}
	moved := *cur
	moved.ModuleText += "\\* branch moved\n"
	moved.Commit = strings.Repeat("b", 40)
	a = reader603Agent{run: func(_ context.Context, job synth.Job) (synth.Usage, error) {
		reader603Workspace(t, job.Workspace)
		reader603Write(t, filepath.Join(job.Workspace, "baseline/view.json"), reader603View)
		// The original caller's maps are mutable, but final-check authority is
		// already detached. The pending branch is not authority either.
		moved.Lock.Bounds = map[string]string{"Limit": "99"}
		return synth.Usage{}, nil
	}}
	q, err := (formalize.Formalizer{Backend: a, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Current: &moved, Previous: r.Proposal}, t.TempDir())
	if err != nil || q == nil || q.Problem != "" {
		t.Fatalf("repair: %+v %v", q, err)
	}
	c, _ := comparison648Of(t, q.Proposal)
	if c.Baseline.Model != cur.ModuleText || c.Baseline.Lock.Bounds["Limit"] != "2" || !strings.Contains(c.Baseline.Name, cur.Commit) || q.Proposal.Target.Baseline.View != reader603View {
		t.Fatal("repair replaced the captured authority")
	}
	if err := q.Proposal.ValidateComparisonContext((formalize.Request{Current: cur}).CheckContext()); err != nil {
		t.Fatal(err)
	}
	q.Proposal.Target.Baseline.Commit = moved.Commit
	if err := q.Proposal.ValidateComparisonContext((formalize.Request{Current: cur}).CheckContext()); err == nil {
		t.Fatal("substituted target commit accepted")
	}
}

func TestComparisonContextDoesNotCopyEarlierHistories(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _ := reader603Checked(t, ws, tc)
	first, _ := comparison648Draft(t, tc, formalize.Request{Previous: old}, "first")
	ctx := (formalize.Request{Previous: first.Proposal}).CheckContext()
	b := reader603JSON(t, ctx)
	if strings.Contains(string(b), `"comparison"`) || ctx.Baseline.View != first.Proposal.View {
		t.Fatal("context recursively copied comparison history")
	}
	first.Proposal.Bounds["Limit"] = "99"
	first.Proposal.Reader.Chart.States[0].Label = "mutated"
	if ctx.Baseline.Lock.Bounds["Limit"] != "2" || ctx.Baseline.Chart.States[0].Label == "mutated" {
		t.Fatal("context aliases previous proposal")
	}
}

func TestProposalWriteBindsOnlyAnOmittedRatificationHash(t *testing.T) {
	ws := t.TempDir()
	reader603Workspace(t, ws)
	p, err := formalize.Read(ws)
	if err != nil {
		t.Fatal(err)
	}
	rat := &project.Ratification{Issue: 648, By: "owner"}
	dir := t.TempDir()
	if err := p.Write(dir, "request", rat); err != nil {
		t.Fatal(err)
	}
	loaded, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Lock.Ratified.Proposal != p.Hash || rat.Proposal != "" {
		t.Fatal("writing did not bind the proposal independently of caller attribution")
	}
	rat.Proposal = "sha256:" + strings.Repeat("0", 64)
	if err := p.Write(dir, "request", rat); err != nil {
		t.Fatal(err)
	}
	loaded, err = project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Lock.Ratified.Proposal != rat.Proposal {
		t.Fatal("writing silently replaced an explicit ratification hash")
	}
}
