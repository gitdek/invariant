package formalize_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// Keep using the existing executable TLC fixture: its real graph/replay
// protocol and argument assertions are part of the contract, not replaced here.
func comparison717Checked(t *testing.T) (*formalize.Proposal, *formalize.Proposal, toolchain.Toolchain) {
	t.Helper()
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _ := reader603Checked(t, ws, tc)
	cur := &formalize.Current{Dir: "examples/reader", Manifest: old.Manifest(), ModuleText: old.ModuleText,
		View: old.View, Includes: old.Includes, Reader: old.Reader,
		Lock: project.Lock{Ratified: &project.Ratification{Issue: 571, By: "owner", Proposal: old.Hash}, Bounds: old.Bounds, Statements: old.Statements}}
	authority := (formalize.Request{Current: cur}).CheckContext()
	reader603Write(t, filepath.Join(ws, "view.json"), strings.ReplaceAll(reader603View, "Working", "Revised working"))
	p, r, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil || p == nil || r == nil || !r.Passed {
		t.Fatalf("comparison fixture: %+v %v", r, err)
	}
	if err := p.Amend(cur); err != nil {
		t.Fatal(err)
	}
	p.Baseline = authority.Baseline
	if err := p.ValidateReader(); err != nil {
		t.Fatal(err)
	}
	return old, p, tc
}

func comparison717Current(t *testing.T, dir string, p *formalize.Proposal) (*formalize.Current, error) {
	t.Helper()
	loaded, err := project.Load(dir)
	if err != nil {
		return nil, err
	}
	c := &formalize.Current{Manifest: loaded.Manifest, Lock: loaded.Lock, ModuleText: p.ModuleText}
	return c, c.ReadReader(dir)
}

func comparison717WriteProject(t *testing.T, p *formalize.Proposal) string {
	t.Helper()
	dir := t.TempDir()
	if err := p.Write(dir, "comparison storage", &project.Ratification{By: "owner", Issue: 717, Proposal: p.Hash}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func comparison717Reference(t *testing.T, p *formalize.Proposal) map[string]any {
	t.Helper()
	wire := reader603Object(t, p.Reader)
	delete(wire, "comparison")
	wire["comparison_ref"] = map[string]any{"version": 1, "file": "comparison.json", "fingerprint": p.Reader.Comparison.Fingerprint}
	return wire
}

func comparison717AssertReference(t *testing.T, dir string, p *formalize.Proposal) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".invariant/reader.json"))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["comparison"]; exists {
		t.Fatal("project reader duplicates the full comparison instead of referencing its sibling")
	}
	var ref struct {
		Version           int
		File, Fingerprint string
	}
	if err := json.Unmarshal(wire["comparison_ref"], &ref); err != nil {
		t.Fatalf("missing comparison_ref: %v", err)
	}
	if ref.Version != 1 || ref.File != "comparison.json" || ref.Fingerprint != p.Reader.Comparison.Fingerprint {
		t.Fatalf("unbound or unsupported comparison reference: %+v", ref)
	}
	if !bytes.Equal(reader603JSON(t, reader603Object(t, comparison717Reference(t, p))), reader603JSON(t, reader603Object(t, json.RawMessage(b)))) {
		t.Fatal("reference writing changed ordinary reader evidence")
	}
}

func TestComparison717ProjectReferenceRoundTrip(t *testing.T) {
	old, p, tc := comparison717Checked(t)
	before := reader603JSON(t, p)
	dir := comparison717WriteProject(t, p)
	comparison717AssertReference(t, dir, p)
	c, err := comparison717Current(t, dir, p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reader603JSON(t, c.Reader), reader603JSON(t, p.Reader)) || c.View != p.View || !reflect.DeepEqual(c.Includes, p.Includes) {
		t.Fatal("reference round trip lost exact reader, sources or fingerprint")
	}
	if !bytes.Equal(before, reader603JSON(t, p)) || project.ProposalHash(c.Lock.Bounds, c.Lock.Statements) != p.Hash || !reflect.DeepEqual(c.Lock.Statements, p.Statements) {
		t.Fatal("storage mutated the proposal or its ratified statements")
	}
	b, err := os.ReadFile(filepath.Join(dir, ".invariant/comparison.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved statechart.Comparison
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reader603JSON(t, &saved), reader603JSON(t, p.Reader.Comparison)) {
		t.Fatal("comparison bytes or fingerprint changed")
	}
	var result formalize.Result
	if err := json.Unmarshal(reader603JSON(t, formalize.Result{Proposal: p}), &result); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reader603JSON(t, result.Proposal), before) {
		t.Fatal("formalization result stopped being self-contained")
	}
	r, err := verify.RunModel(t.Context(), dir, tc)
	if err != nil || r == nil || !r.Passed || r.Reader == nil || r.Reader.Comparison == nil {
		t.Fatalf("receipt: %+v %v", r, err)
	}
	archive := t.TempDir()
	if err := receipt.Write(archive, r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"receipt.json", "reader.json"} {
		data, err := os.ReadFile(filepath.Join(archive, name))
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		if name == "receipt.json" {
			if err := json.Unmarshal(wire["reader"], &wire); err != nil {
				t.Fatal(err)
			}
		}
		var got statechart.Comparison
		if err := json.Unmarshal(wire["comparison"], &got); err != nil {
			t.Fatalf("%s is not self-contained: %v", name, err)
		}
		if !bytes.Equal(reader603JSON(t, &got), reader603JSON(t, &saved)) || len(wire["comparison_ref"]) != 0 {
			t.Fatalf("%s changed comparison provenance", name)
		}
	}
	without := *r
	reader := *r.Reader
	reader.Comparison = nil
	without.Reader = &reader
	if verify.Fingerprint(without) != verify.Fingerprint(*r) || without.Claim() != r.Claim() {
		t.Fatal("explanation changed verification identity or claim")
	}
	if err := old.Write(dir, "ordinary reader", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".invariant/comparison.json")); !os.IsNotExist(err) {
		t.Fatalf("stale comparison retained: %v", err)
	}
	c, err = comparison717Current(t, dir, old)
	if err != nil || !reflect.DeepEqual(c.Reader, old.Reader) {
		t.Fatalf("ordinary reader rewrite: %v", err)
	}
	legacy := reader603Copy(t, old, func(m map[string]any) { delete(m, "reader"); delete(m, "view"); delete(m, "includes") })
	if err := legacy.Write(dir, "legacy", nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"reader.json", "comparison.json"} {
		if _, err := os.Stat(filepath.Join(dir, ".invariant", name)); !os.IsNotExist(err) {
			t.Fatalf("legacy rewrite retained %s: %v", name, err)
		}
	}
	c, err = comparison717Current(t, dir, legacy)
	if err != nil || c.Reader != nil {
		t.Fatalf("legacy project needs no invented reader: %v", err)
	}
}

func TestComparison717ReferencedEvidenceRefusesDamage(t *testing.T) {
	_, p, _ := comparison717Checked(t)
	for _, mode := range []string{"reference", "inline only", "inline and sibling", "inline mismatch", "inline damaged sibling", "missing", "malformed", "trailing JSON", "wrong fingerprint", "unsupported version", "wrong file", "empty fingerprint", "wrong comparison", "damaged comparison", "symlink", "hardlink"} {
		t.Run(mode, func(t *testing.T) {
			dir := comparison717WriteProject(t, p)
			wire := comparison717Reference(t, p)
			ref := wire["comparison_ref"].(map[string]any)
			sibling := filepath.Join(dir, ".invariant/comparison.json")
			good := mode == "reference" || mode == "inline only" || mode == "inline and sibling"
			if strings.HasPrefix(mode, "inline") {
				wire = reader603Object(t, p.Reader)
			}
			switch mode {
			case "missing", "inline only":
				if err := os.Remove(sibling); err != nil {
					t.Fatal(err)
				}
			case "malformed", "inline damaged sibling":
				reader603Write(t, sibling, "{")
			case "trailing JSON":
				reader603Write(t, sibling, string(reader603JSON(t, p.Reader.Comparison))+" {}")
			case "wrong fingerprint":
				ref["fingerprint"] = "sha256:" + strings.Repeat("0", 64)
			case "empty fingerprint":
				ref["fingerprint"] = ""
			case "unsupported version":
				ref["version"] = 2
			case "wrong file":
				ref["file"] = "../comparison.json"
			case "wrong comparison", "inline mismatch":
				other, err := statechart.Compare(p.Reader.Comparison.Baseline, p.Reader.Comparison.Baseline)
				if err != nil {
					t.Fatal(err)
				}
				reader603Write(t, sibling, string(reader603JSON(t, other)))
			case "damaged comparison":
				other := reader603Object(t, p.Reader.Comparison)
				other["view_changed"] = false
				reader603Write(t, sibling, string(reader603JSON(t, other)))
			case "symlink", "hardlink":
				outside := filepath.Join(t.TempDir(), "evidence.json")
				reader603Write(t, outside, string(reader603JSON(t, p.Reader.Comparison)))
				if err := os.Remove(sibling); err != nil {
					t.Fatal(err)
				}
				link := os.Symlink
				if mode == "hardlink" {
					link = os.Link
				}
				if err := link(outside, sibling); err != nil {
					t.Fatal(err)
				}
			}
			reader603Write(t, filepath.Join(dir, ".invariant/reader.json"), string(reader603JSON(t, wire)))
			c, err := comparison717Current(t, dir, p)
			if good {
				if err != nil || !bytes.Equal(reader603JSON(t, c.Reader), reader603JSON(t, p.Reader)) {
					t.Fatalf("historical/reference reader failed: %v", err)
				}
			} else if err == nil || !strings.Contains(strings.ToLower(err.Error()), "comparison") {
				t.Fatalf("required comparison damage was silently ignored or unnamed: %v", err)
			}
		})
	}
}

func TestComparison717PreviousHasOneDerivedRecord(t *testing.T) {
	_, p, tc := comparison717Checked(t)
	before := reader603JSON(t, p)
	for _, amend := range []bool{false, true} {
		req := formalize.Request{Repo: "o/r", Issue: 717, Previous: p}
		if amend {
			req.Current = p.Target.Baseline
		}
		inspected := false
		a := reader603Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
			root := filepath.Join(j.Workspace, "previous")
			b, err := os.ReadFile(filepath.Join(root, "proposal.json"))
			if err != nil {
				t.Fatal(err)
			}
			var saved formalize.Proposal
			if err := json.Unmarshal(b, &saved); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(reader603JSON(t, &saved), before) {
				t.Fatal("previous proposal lost sources or derived evidence")
			}
			for name, want := range map[string]string{p.Module + ".tla": p.ModuleText, "Helper603.tla": p.Includes["Helper603.tla"], "view.json": p.View} {
				b, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || string(b) != want {
					t.Fatalf("staged %s differs: %v", name, err)
				}
			}
			for _, name := range []string{"reader.json", "comparison.json"} {
				if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
					t.Errorf("previous/%s duplicates derived evidence: %v", name, err)
				}
			}
			if !strings.Contains(j.Prompt, "previous/proposal.json") {
				t.Error("drafting guidance does not identify the complete previous record")
			}
			// Staged edits remain context, never authority supplied to the checker.
			reader603Write(t, filepath.Join(root, "proposal.json"), `{"reader":{"comparison":{"fingerprint":"forged"}}}`)
			reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), `{"unsupported":"Fixture stops after inspecting staged input."}`)
			inspected = true
			return synth.Usage{}, nil
		}}
		if _, err := (formalize.Formalizer{Backend: a, Toolchain: tc}).Formalize(t.Context(), req, t.TempDir()); err != nil || !inspected {
			t.Fatalf("staging failed: %v", err)
		}
	}
	if !bytes.Equal(before, reader603JSON(t, p)) {
		t.Fatal("staging mutated the caller's retained proposal")
	}
	carrier := p.Carrier()
	var saved formalize.Proposal
	if err := json.Unmarshal(reader603JSON(t, carrier), &saved); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Baseline, p.Baseline) || !reflect.DeepEqual(saved.Target, p.Target) {
		t.Fatal("partial-answer carrier lost captured sources")
	}
}

// Seal only the outer reader using its existing public wire contract. This
// makes the mutation checks reach comparison/candidate/baseline validation
// instead of succeeding through the outer stale-fingerprint shortcut.
func comparison717Seal(t *testing.T, p *formalize.Proposal) {
	t.Helper()
	b := reader603JSON(t, struct {
		Version    int                         `json:"version"`
		Module     string                      `json:"module"`
		Model      string                      `json:"model"`
		Includes   map[string]string           `json:"includes,omitempty"`
		View       string                      `json:"view"`
		Bounds     map[string]string           `json:"bounds"`
		Statements []project.Statement         `json:"statements"`
		Chart      *statechart.Result          `json:"chart,omitempty"`
		Limitation *formalize.ReaderLimitation `json:"limitation,omitempty"`
		Comparison *statechart.Comparison      `json:"comparison,omitempty"`
	}{p.Reader.Version, p.Module, p.ModuleText, p.Includes, p.View, p.Bounds, p.Statements, p.Reader.Chart, p.Reader.Limitation, p.Reader.Comparison})
	p.Reader.Fingerprint = fmt.Sprintf("sha256:%x", sha256.Sum256(b))
}

func TestComparison717ReaderBindingsSurviveRoundTrip(t *testing.T) {
	_, p, _ := comparison717Checked(t)
	dir := comparison717WriteProject(t, p)
	comparison717AssertReference(t, dir, p)
	c, err := comparison717Current(t, dir, p)
	if err != nil {
		t.Fatal(err)
	}
	p.Reader = c.Reader
	copyProposal := func() *formalize.Proposal {
		var q formalize.Proposal
		if err := json.Unmarshal(reader603JSON(t, p), &q); err != nil {
			t.Fatal(err)
		}
		if err := q.ValidateReader(); err != nil {
			t.Fatal(err)
		}
		if err := q.ValidateComparisonContext(formalize.CheckContext{Baseline: q.Baseline}); err != nil {
			t.Fatal(err)
		}
		return &q
	}
	for name, mutate := range map[string]func(*formalize.Proposal){
		"candidate model":             func(q *formalize.Proposal) { q.ModuleText += "\\* changed\n" },
		"candidate view":              func(q *formalize.Proposal) { q.View += " " },
		"candidate includes":          func(q *formalize.Proposal) { q.Includes["Helper603.tla"] += "\\* changed\n" },
		"candidate bounds":            func(q *formalize.Proposal) { q.Bounds["Limit"] = "3" },
		"candidate wording":           func(q *formalize.Proposal) { q.Statements[0].Says += " changed" },
		"candidate hash":              func(q *formalize.Proposal) { q.Hash += "changed" },
		"candidate chart":             func(q *formalize.Proposal) { q.Reader.Chart.States[0].Label += " changed" },
		"target baseline":             func(q *formalize.Proposal) { q.Target.Baseline.Lock.Ratified.By = "someone else" },
		"carried baseline":            func(q *formalize.Proposal) { q.Baseline.View += " " },
		"missing required comparison": func(q *formalize.Proposal) { q.Reader.Comparison = nil },
	} {
		t.Run(name, func(t *testing.T) {
			q := copyProposal()
			mutate(q)
			comparison717Seal(t, q)
			if err := q.ValidateReader(); err == nil {
				t.Fatal("mutation passed after prior successful validation")
			}
		})
	}
	// Every top-level comparison field is independently covered with a fresh
	// copy, after both public validation boundaries have already succeeded.
	for field := range reader603Object(t, p.Reader.Comparison) {
		t.Run("comparison "+field, func(t *testing.T) {
			q := copyProposal()
			m := reader603Object(t, q.Reader.Comparison)
			delete(m, field)
			q.Reader.Comparison = &statechart.Comparison{}
			if err := json.Unmarshal(reader603JSON(t, m), q.Reader.Comparison); err != nil {
				t.Fatal(err)
			}
			// Zero-valued booleans/deltas may already be absent in meaning;
			// make each field differ while retaining a well-typed record.
			v := reflect.ValueOf(q.Reader.Comparison).Elem()
			orig := reflect.ValueOf(p.Reader.Comparison).Elem()
			for i := 0; i < v.NumField(); i++ {
				if strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0] == field && reflect.DeepEqual(v.Field(i).Interface(), orig.Field(i).Interface()) {
					switch v.Field(i).Kind() {
					case reflect.Bool:
						v.Field(i).SetBool(!v.Field(i).Bool())
					case reflect.String:
						v.Field(i).SetString("damaged")
					case reflect.Struct:
						if d, ok := v.Field(i).Addr().Interface().(*statechart.Delta); ok {
							d.AddedStates = []statechart.State{{ID: "invented", Label: "invented"}}
						}
					}
				}
			}
			comparison717Seal(t, q)
			if err := q.ValidateReader(); err == nil {
				t.Error("comparison field mutation accepted by ValidateReader")
			}
			if err := q.ValidateComparisonContext(formalize.CheckContext{Baseline: q.Baseline}); err == nil {
				t.Error("standalone context validation trusted an earlier success")
			}
		})
	}
}
