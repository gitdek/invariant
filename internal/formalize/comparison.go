package formalize

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func detachedCurrent(c *Current) *Current {
	if c == nil {
		return nil
	}
	copyCurrent := *c
	// Saved reader files are not baseline authority, and may contain an older
	// comparison. Only exact sources and the captured lock are carried forward.
	copyCurrent.Reader = nil
	b, _ := json.Marshal(copyCurrent)
	var out Current
	_ = json.Unmarshal(b, &out)
	return &out
}

func (r Request) comparisonCurrent() *Current {
	if r.Previous != nil && r.Previous.Target != nil && r.Previous.Target.Baseline != nil &&
		(r.Current == nil || r.Current.Dir == r.Previous.Target.Dir) {
		return detachedCurrent(r.Previous.Target.Baseline)
	}
	return detachedCurrent(r.Current)
}

func (c *Current) comparisonSnapshot() statechart.ComparisonSnapshot {
	name := "Ratification " + c.Previous()
	if c.Dir != "" {
		name += " in " + c.Dir
	}
	if c.Commit != "" {
		name += " at " + c.Commit
	}
	return statechart.ComparisonSnapshot{Kind: "ratification", Name: name, Module: c.ModuleName(),
		Model: c.ModuleText, Includes: c.Includes, View: c.View, Lock: c.Lock}
}

func (p *Proposal) comparisonSnapshot() statechart.ComparisonSnapshot {
	s := statechart.ComparisonSnapshot{Kind: "proposal", Name: "Proposal " + p.Hash, Module: p.Module,
		Model: p.ModuleText, Includes: p.Includes, View: p.View, Lock: project.Lock{Bounds: p.Bounds, Statements: p.Statements}}
	if p.Reader != nil {
		s.Chart, s.Limitation = p.Reader.Chart, p.Reader.Limitation
	}
	return s
}

func (r Request) comparisonBaseline() *statechart.ComparisonSnapshot {
	var s *statechart.ComparisonSnapshot
	if c := r.comparisonCurrent(); c != nil {
		x := c.comparisonSnapshot()
		s = &x
	} else if p := r.Previous; p != nil && p.Plan == nil && p.IssuePlan == nil {
		if p.Baseline != nil {
			s = p.Baseline
		} else if p.Ratifiable() && p.ModuleText != "" {
			x := p.comparisonSnapshot()
			s = &x
		}
	}
	if s == nil {
		return nil
	}
	b, _ := json.Marshal(s)
	var out statechart.ComparisonSnapshot
	_ = json.Unmarshal(b, &out)
	return &out
}

func stageBaseline(ws string, s *statechart.ComparisonSnapshot) error {
	if s == nil {
		return nil
	}
	if !moduleRE.MatchString(s.Module) {
		return fmt.Errorf("comparison baseline has invalid module %q", s.Module)
	}
	if err := validateIncludes(s.Includes, s.Module); err != nil {
		return err
	}
	dir := filepath.Join(ws, "baseline")
	if err := writeFile(filepath.Join(dir, s.Module+".tla"), s.Model); err != nil {
		return err
	}
	if err := writeIncludes(dir, s.Includes); err != nil {
		return err
	}
	if s.View != "" {
		if err := writeFile(filepath.Join(dir, "view.json"), s.View); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(s.Lock, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, "ratified.lock"), string(b)+"\n")
}

func (p *Proposal) checkComparison(ctx context.Context, ws string, tc toolchain.Toolchain, authority CheckContext) error {
	if authority.Baseline == nil {
		return nil
	}
	s := *authority.Baseline
	if s.View == "" {
		view, err := regular.ReadFile(ws, "baseline/view.json")
		if err != nil {
			return fmt.Errorf("comparison baseline needs a supplied baseline/view.json for its captured model and original bounds: %w", err)
		}
		s.View = string(view)
	}
	// Recheck the captured source, never baseline/*.tla, previous/ or a derived
	// reader file. The old model gate and reader run retain the original lock.
	dir, err := os.MkdirTemp("", "invariant-comparison-baseline-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if !moduleRE.MatchString(s.Module) {
		return fmt.Errorf("comparison baseline has invalid module")
	}
	if err := validateIncludes(s.Includes, s.Module); err != nil {
		return fmt.Errorf("comparison baseline: %w", err)
	}
	m := project.Manifest{Name: s.Name, Module: ".invariant/specs/" + s.Module + ".tla", Code: "core", Language: "go"}
	// This is a model-only check of captured rules, not a new ratification
	// claim. Legacy attribution may predate proposal hashes. Keep it verbatim
	// in s.Lock while the gate checks every original pin and checking bound.
	checkLock := project.Lock{Bounds: s.Lock.Bounds, Statements: s.Lock.Statements}
	for name, value := range map[string]any{".invariant/invariant.json": m, ".invariant/ratified.lock": checkLock} {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, name), string(b)); err != nil {
			return err
		}
	}
	if err := writeFile(filepath.Join(dir, m.Module), s.Model); err != nil {
		return err
	}
	if err := writeIncludes(filepath.Join(dir, ".invariant/specs"), s.Includes); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, ".invariant/view.json"), s.View); err != nil {
		return err
	}
	r, err := verify.RunModel(ctx, dir, tc)
	if err != nil {
		return fmt.Errorf("comparison baseline model check: %w", err)
	}
	if r == nil {
		return fmt.Errorf("comparison baseline model check returned no receipt")
	}
	if !r.Passed {
		return fmt.Errorf("comparison baseline model check: %s", verify.Feedback(r))
	}
	if r.Reader == nil {
		return fmt.Errorf("comparison baseline returned no checked reader")
	}
	if d := r.Reader.Unavailable; d != nil {
		return fmt.Errorf("comparison baseline %s: %s", d.Code, d.Message)
	}
	s.Chart, s.Limitation = r.Reader.Chart, r.Reader.Limitation
	p.Reader.Comparison, err = statechart.Compare(s, p.comparisonSnapshot())
	if err != nil {
		return err
	}
	p.Reader.Fingerprint, err = p.readerFingerprint()
	return err
}

// ValidateComparisonContext is the publication boundary for fresh and recovered
// results. A valid ordinary chart cannot satisfy a required comparison.
func (p *Proposal) ValidateComparisonContext(c CheckContext) error {
	if c.Baseline == nil {
		return nil
	}
	if p.Reader == nil || p.Reader.Comparison == nil {
		return fmt.Errorf("comparison with baseline %s is missing; check the draft against its captured baseline before publication", c.Baseline.Name)
	}
	if err := p.ValidateReader(); err != nil {
		return err
	}
	got, want := p.Reader.Comparison.Baseline, *c.Baseline
	if want.View == "" {
		want.View = got.View
	}
	if got.Kind != want.Kind || got.Name != want.Name || got.Module != want.Module || got.Model != want.Model || got.View != want.View ||
		!maps.Equal(got.Includes, want.Includes) || !reflect.DeepEqual(got.Lock, want.Lock) {
		return fmt.Errorf("comparison baseline is stale: expected %s with its captured model, includes, view and lock; check again", want.Name)
	}
	if c.Current != nil {
		if p.Target == nil || p.Target.Baseline == nil {
			return fmt.Errorf("comparison baseline target is missing; capture the ratified project before publication")
		}
		target, current := detachedCurrent(p.Target.Baseline), detachedCurrent(c.Current)
		if current.View == "" {
			current.View = got.View
		}
		if !reflect.DeepEqual(target, current) || p.Target.Dir != current.Dir || !reflect.DeepEqual(p.Target.Manifest, current.Manifest) ||
			p.Target.Amends != project.ProposalHash(current.Lock.Bounds, current.Lock.Statements) || p.Target.Previous != current.Previous() {
			return fmt.Errorf("comparison baseline differs from captured amendment target")
		}
	}
	return nil
}

func writeComparison(dir string, reader *Reader) error {
	name := filepath.Join(dir, "comparison.json")
	if reader == nil || reader.Comparison == nil {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := reader.Comparison.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(reader.Comparison, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(name, string(b)+"\n")
}
