package formalize_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// Shared wire contract: formalize.Reader and verify.Reader optionally retain
// comparison, the complete statechart.Comparison. Changes.reader is that same
// comparison beside the existing statement/bound diff. Target.baseline keeps
// the captured Current, detached from later branch reads. A revision's source
// is its immediately previous selected modeled proposal; an amendment always
// uses Target.baseline once captured. Fork-only drafts are not empty models.
// baseline/ presents immutable old model/includes and ratified.lock to the
// drafter. Only baseline/view.json may be supplied for a legacy missing view.
// The check tool and final check retain caller-owned baseline authority;
// baseline files, previous/, proposal.json and check-context.json cannot
// replace it. Proposal.Write stores .invariant/comparison.json for receipts.
// This file is explanatory provenance, separate from the new ratified lock.
type comparison648Side struct {
	Kind, Name, Module, Model, View string
	Includes                        map[string]string
	Lock                            project.Lock
	Chart                           json.RawMessage
}
type comparison648Record struct {
	Version             int
	Fingerprint         string
	Baseline, Candidate comparison648Side
	ViewChanged         bool `json:"view_changed"`
	BoundsChanged       bool `json:"bounds_changed"`
	Scope               string
}

func comparison648Of(t *testing.T, x any) (comparison648Record, json.RawMessage) {
	t.Helper()
	var wire struct {
		Reader *struct{ Comparison json.RawMessage }
	}
	if err := json.Unmarshal(reader603JSON(t, x), &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Reader == nil || len(wire.Reader.Comparison) == 0 || string(wire.Reader.Comparison) == "null" {
		t.Fatal("checked result has no reader comparison")
	}
	var c comparison648Record
	if err := json.Unmarshal(wire.Reader.Comparison, &c); err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || !strings.HasPrefix(c.Fingerprint, "sha256:") || c.Baseline.Model == "" || c.Candidate.Model == "" {
		t.Fatalf("incomplete comparison: %+v", c)
	}
	return c, wire.Reader.Comparison
}

func comparison648Draft(t *testing.T, tc toolchain.Toolchain, req formalize.Request, label string) (*formalize.Result, string) {
	t.Helper()
	out := t.TempDir()
	var fromTool json.RawMessage
	a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
		reader603Workspace(t, j.Workspace)
		reader603Write(t, filepath.Join(j.Workspace, "view.json"), strings.ReplaceAll(reader603View, "Working", label))
		// This is the context passed to the actual MCP check process, not the
		// agent-editable convenience copy. Exercise it as well as direct Check.
		authority := loadDraftContext(t, ctx, j, req.ParentPlan)
		reader603Write(t, filepath.Join(j.Workspace, "check-context.json"), `{}`)
		p, r, err := formalize.CheckWithContext(ctx, j.Workspace, tc, authority)
		if err != nil || r == nil || !r.Passed {
			t.Fatalf("draft check: %+v %v", r, err)
		}
		_, fromTool = comparison648Of(t, p)
		// Derived files are never authority for the independent final check.
		reader603Write(t, filepath.Join(j.Workspace, "reader.json"), `{"comparison":{"baseline":{"model":"forged"}}}`)
		return synth.Usage{}, nil
	}}
	r, err := (formalize.Formalizer{Backend: a, Toolchain: tc, CheckRuns: 4}).Formalize(t.Context(), req, out)
	if err != nil || r == nil || r.Problem != "" {
		t.Fatalf("formalization: %+v %v", r, err)
	}
	_, final := comparison648Of(t, r.Proposal)
	var one, two any
	_ = json.Unmarshal(fromTool, &one)
	_ = json.Unmarshal(final, &two)
	if !reflect.DeepEqual(one, two) {
		t.Fatal("tool check and independent final check compared different snapshots")
	}
	return r, out
}

func TestComparison648ConsecutiveRevisionsAndSavedResults(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _ := reader603Checked(t, ws, tc)
	first, out := comparison648Draft(t, tc, formalize.Request{Repo: "o/r", Issue: 648, Previous: old}, "First revision")
	c, raw := comparison648Of(t, first.Proposal)
	if c.Baseline.Kind != "proposal" || !strings.Contains(c.Baseline.Name, old.Hash) || c.Baseline.View != old.View || c.Candidate.View != first.Proposal.View || !c.ViewChanged {
		t.Fatalf("wrong preceding proposal: %+v", c)
	}
	var changes struct{ Reader json.RawMessage }
	if first.Changes == nil {
		t.Fatal("revision lost its statement/bound diff")
	}
	_ = json.Unmarshal(reader603JSON(t, first.Changes), &changes)
	if !reflect.DeepEqual(changes.Reader, raw) {
		t.Fatal("statement diff and reader comparison identify different revisions")
	}
	var saved formalize.Result
	b, err := os.ReadFile(filepath.Join(out, "formalization.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	_, again := comparison648Of(t, saved.Proposal)
	if !reflect.DeepEqual(raw, again) {
		t.Fatal("saved result lost the comparison")
	}
	second, _ := comparison648Draft(t, tc, formalize.Request{Repo: "o/r", Issue: 648, Previous: saved.Proposal}, "Second revision")
	c2, _ := comparison648Of(t, second.Proposal)
	if c2.Baseline.View != first.Proposal.View || c2.Baseline.View == old.View || c2.Candidate.View != second.Proposal.View {
		t.Fatal("second revision used the first draft's ancestor")
	}
	// The proposal hash can stay equal for view-only revisions, so model/view
	// identities, not the statement hash alone, must distinguish the snapshots.
	if old.Hash != first.Proposal.Hash || first.Proposal.Hash != second.Proposal.Hash || c.Fingerprint == c2.Fingerprint {
		t.Fatal("view identity was conflated with ratification identity")
	}
}

func TestComparison648LegacyBaselineRepairAndAuthority(t *testing.T) {
	tc, log := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	p, _ := reader603Checked(t, ws, tc)
	oldModel := p.ModuleText + "\\* captured old model\n"
	cur := &formalize.Current{Dir: "examples/reader", Manifest: p.Manifest(), ModuleText: oldModel, Includes: p.Includes, Lock: project.Lock{Ratified: &project.Ratification{Issue: 571, By: "owner"}, Bounds: p.Bounds, Statements: p.Statements}}
	for _, mode := range []string{"missing", "invalid", "valid", "tampered old files"} {
		t.Run(mode, func(t *testing.T) {
			a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
				reader603Workspace(t, j.Workspace)
				reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"3"`, 1))
				if mode != "missing" {
					view := reader603View
					if mode == "invalid" {
						view = strings.Replace(reader603View, `"label":"Continue"`, `"label":"Continue","hide":true`, 1)
					}
					reader603Write(t, filepath.Join(j.Workspace, "baseline/view.json"), view)
				}
				if mode == "tampered old files" {
					reader603Write(t, filepath.Join(j.Workspace, "baseline/Reader603.tla"), reader603Model)
					reader603Write(t, filepath.Join(j.Workspace, "baseline/ratified.lock"), `{"bounds":{"Limit":"3"},"statements":[]}`)
				}
				q, r, err := formalize.Check(ctx, j.Workspace, tc)
				if mode == "missing" || mode == "invalid" {
					if err == nil || !strings.Contains(strings.ToLower(err.Error()), "baseline") {
						t.Fatalf("baseline failure not actionable in check: %+v %+v %v", q, r, err)
					}
					if mode == "missing" && !strings.Contains(err.Error(), "view.json") {
						t.Fatal("missing-view repair path absent")
					}
					if mode == "invalid" && !strings.Contains(err.Error(), "unreachable_chart") {
						t.Fatal("baseline hiding refusal lost its cause")
					}
				} else if err != nil && mode != "tampered old files" {
					t.Fatal(err)
				}
				return synth.Usage{}, nil
			}}
			r, err := (formalize.Formalizer{Backend: a, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Repo: "o/r", Issue: 648, Current: cur}, t.TempDir())
			if err != nil || r == nil {
				t.Fatalf("formalizer: %+v %v", r, err)
			}
			if mode == "missing" || mode == "invalid" {
				if !strings.Contains(strings.ToLower(r.Problem), "baseline") {
					t.Fatalf("final check accepted bad baseline: %+v", r)
				}
				return
			}
			if mode == "tampered old files" && r.Problem != "" {
				if !strings.Contains(strings.ToLower(r.Problem), "baseline") {
					t.Fatal(r.Problem)
				}
				return
			}
			if r.Problem != "" {
				t.Fatal(r.Problem)
			}
			c, _ := comparison648Of(t, r.Proposal)
			if c.Baseline.Kind != "ratification" || !strings.Contains(c.Baseline.Name, "#571") || c.Baseline.Model != oldModel || !reflect.DeepEqual(c.Baseline.Lock, cur.Lock) || c.Baseline.View != reader603View || !c.BoundsChanged || !strings.Contains(strings.ToLower(c.Scope), "scope") {
				t.Fatalf("legacy baseline edited or not checked: %+v", c)
			}
			if c.Candidate.Lock.Bounds["Limit"] != "3" {
				t.Fatal("candidate bounds lost")
			}
		})
	}
	var oldDump, oldReplay bool
	for _, call := range reader603Calls(t, log) {
		if call["main"] == oldModel && strings.Contains(call["config"], "Limit = 2") && call["dump"] != "" {
			if call["module"] == "Reader603" {
				oldDump = true
			} else {
				oldReplay = true
			}
		}
	}
	if !oldDump || !oldReplay {
		t.Fatal("legacy baseline view was not checked by complete TLC graph and invariant replay at old bounds")
	}
}

func TestComparison648AmendmentBaselineAndReceiptSources(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	p, _ := reader603Checked(t, ws, tc)
	cur := &formalize.Current{Dir: "examples/reader", Manifest: p.Manifest(), ModuleText: p.ModuleText, View: p.View, Includes: p.Includes, Reader: p.Reader, Lock: project.Lock{Ratified: &project.Ratification{Issue: 571, By: "owner"}, Bounds: p.Bounds, Statements: p.Statements}}
	first, _ := comparison648Draft(t, tc, formalize.Request{Repo: "o/r", Issue: 648, Current: cur}, "Amendment one")
	if first.Proposal.Target == nil {
		t.Fatal("amendment lost its target")
	}
	// Simulate a new branch tip after saving the first amendment.
	moved := *cur
	moved.ModuleText += "\\* new branch content\n"
	moved.View = strings.ReplaceAll(p.View, "Working", "Branch working")
	second, _ := comparison648Draft(t, tc, formalize.Request{Repo: "o/r", Issue: 648, Current: &moved, Previous: first.Proposal}, "Amendment two")
	c, original := comparison648Of(t, second.Proposal)
	if c.Baseline.Kind != "ratification" || c.Baseline.Model != cur.ModuleText || c.Baseline.View != cur.View || !reflect.DeepEqual(c.Baseline.Lock, cur.Lock) {
		t.Fatal("amendment revision replaced captured ratification with latest branch or prior proposal")
	}
	dir := t.TempDir()
	if err := second.Proposal.Write(dir, "request", &project.Ratification{Issue: 648, By: "owner"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".invariant/comparison.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved, want any
	_ = json.Unmarshal(b, &saved)
	_ = json.Unmarshal(original, &want)
	if !reflect.DeepEqual(saved, want) {
		t.Fatal("project writing lost proposal comparison")
	}
	// Verification recomputes the final chart. The proposal comparison still
	// names its original candidate; it never silently changes its meaning.
	finalModel := second.Proposal.ModuleText + "\\* synthesized model source\n"
	reader603Write(t, filepath.Join(dir, second.Proposal.Manifest().Module), finalModel)
	r, err := verify.RunModel(t.Context(), dir, tc)
	if err != nil || r == nil || !r.Passed {
		t.Fatalf("model receipt: %+v %v", r, err)
	}
	got, raw := comparison648Of(t, r)
	if got.Candidate.Model != second.Proposal.ModuleText || got.Candidate.Model == finalModel || !reflect.DeepEqual(raw, original) {
		t.Fatal("receipt relabeled a proposal comparison as a different synthesized model")
	}
	md := receipt.Markdown(r)
	if !strings.Contains(md, second.Proposal.Reader.Chart.Markdown()) {
		t.Fatal("receipt comparison dropped the complete proposed Mermaid chart")
	}
	for _, word := range []string{"proposal comparison", "final", c.Baseline.Name, "view change"} {
		if !strings.Contains(strings.ToLower(md), strings.ToLower(word)) {
			t.Errorf("receipt lacks qualification %q", word)
		}
	}
	var plain verify.Report
	wire := reader603Object(t, r)
	delete(wire["reader"].(map[string]any), "comparison")
	if err := json.Unmarshal(reader603JSON(t, wire), &plain); err != nil {
		t.Fatal(err)
	}
	if verify.Fingerprint(plain) != verify.Fingerprint(*r) || plain.Passed != r.Passed || plain.Claim() != r.Claim() {
		t.Fatal("comparison changed verification evidence or assurance")
	}
	archive := t.TempDir()
	if err := receipt.Write(archive, r); err != nil {
		t.Fatal(err)
	}
	var archived verify.Report
	b, err = os.ReadFile(filepath.Join(archive, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &archived); err != nil {
		t.Fatal(err)
	}
	_, stored := comparison648Of(t, &archived)
	if !reflect.DeepEqual(original, stored) {
		t.Fatal("structured receipt lost proposal comparison")
	}
	b, err = os.ReadFile(filepath.Join(archive, "reader.json"))
	if err != nil {
		t.Fatal(err)
	}
	var companion map[string]json.RawMessage
	if err := json.Unmarshal(b, &companion); err != nil {
		t.Fatal(err)
	}
	var companionValue, originalValue any
	_ = json.Unmarshal(companion["comparison"], &companionValue)
	_ = json.Unmarshal(original, &originalValue)
	if !reflect.DeepEqual(companionValue, originalValue) {
		t.Fatal("reader companion differs from receipt comparison")
	}
	for _, name := range []string{"receipt.md", "reader.svg"} {
		b, err := os.ReadFile(filepath.Join(archive, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, word := range []string{"proposal comparison", "view change", "Amendment two", c.Baseline.Name} {
			if !strings.Contains(strings.ToLower(string(b)), strings.ToLower(word)) {
				t.Errorf("%s lacks %q", name, word)
			}
		}
	}
	// Reusing a project directory for a proposal without a comparison clears
	// obsolete provenance rather than attaching it to unrelated work.
	if err := p.Write(dir, "new unrelated proposal", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".invariant/comparison.json")); !os.IsNotExist(err) {
		t.Fatal("stale project comparison survives reuse")
	}
}
