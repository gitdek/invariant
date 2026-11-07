package formalize

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func detachCurrent(c *Current) *Current {
	if c == nil {
		return nil
	}
	copyCurrent := *c
	// The old project may itself have been an amendment. Its history is not
	// needed to check this baseline and must never grow recursively.
	copyCurrent.Reader = nil
	b, _ := json.Marshal(copyCurrent)
	var out Current
	_ = json.Unmarshal(b, &out)
	return &out
}

func detachedSnapshot(s *statechart.ComparisonSnapshot) *statechart.ComparisonSnapshot {
	if s == nil {
		return nil
	}
	b, _ := json.Marshal(s)
	var out statechart.ComparisonSnapshot
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	_ = d.Decode(&out)
	// As in a checked comparison, no includes and an empty set are one source.
	if out.Includes == nil {
		out.Includes = map[string]string{}
	}
	return &out
}

// CapturedCurrent is the project an amendment changes: the ratification an
// earlier draft of it captured, when that draft amended the same project,
// or else the project as the caller found it. A request without a current
// project changes none, whatever an earlier draft amended. The caller's
// saved reader stays as workspace context when it describes these exact
// sources, and never becomes part of a captured baseline.
func (r Request) CapturedCurrent() *Current {
	if r.Current == nil {
		return nil
	}
	c := detachCurrent(r.Current)
	if p := r.Previous; p != nil && p.Target != nil && p.Target.Baseline != nil && p.Target.Dir == r.Current.Dir {
		c = detachCurrent(p.Target.Baseline)
	}
	if sameSources(c, r.Current) {
		c.Reader = r.Current.Reader
	}
	return c
}

func sameSources(a, b *Current) bool {
	x, _ := json.Marshal(a.Lock)
	y, _ := json.Marshal(b.Lock)
	return a.ModuleText == b.ModuleText && maps.Equal(a.Includes, b.Includes) && a.View == b.View && bytes.Equal(x, y)
}

func currentSnapshot(c *Current) *statechart.ComparisonSnapshot {
	if c == nil {
		return nil
	}
	name := "Ratification " + c.Previous() + " of " + c.Dir
	if c.Commit != "" {
		name += " at " + c.Commit
	}
	return detachedSnapshot(&statechart.ComparisonSnapshot{Kind: "ratification", Name: name,
		Module: c.ModuleName(), Model: c.ModuleText, Includes: c.Includes, View: c.View, Lock: c.Lock})
}

func (p *Proposal) comparisonSnapshot() statechart.ComparisonSnapshot {
	// A view-only revision keeps its statement hash, so the view's hash tells
	// its two sides apart.
	view := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(p.View)))
	s := statechart.ComparisonSnapshot{Kind: "proposal", Name: "Proposal " + p.Hash + " with view " + view[:19], Module: p.Module, Model: p.ModuleText, Includes: p.Includes, View: p.View, Lock: project.Lock{Bounds: p.Bounds, Statements: p.Statements}}
	if p.Reader != nil {
		s.Chart, s.Limitation = p.Reader.Chart, p.Reader.Limitation
	}
	return s
}

func (r Request) comparisonBaseline() *statechart.ComparisonSnapshot {
	if c := r.CapturedCurrent(); c != nil {
		return currentSnapshot(c)
	}
	if p := r.Previous; p != nil {
		if p.Baseline != nil {
			return detachedSnapshot(p.Baseline)
		}
		// Only a draft that passed its checks is a selected model. A failed
		// first draft carries no baseline, so its revision starts afresh.
		if p.ModuleText != "" && p.Plan == nil && p.IssuePlan == nil && len(p.Forks) == 0 && p.Unsupported == "" && p.Reader != nil && p.ValidateReader() == nil {
			s := p.comparisonSnapshot()
			return detachedSnapshot(&s)
		}
	}
	return nil
}

func stageBaseline(ws string, s *statechart.ComparisonSnapshot) error {
	if s == nil {
		return nil
	}
	dir := filepath.Join(ws, "baseline")
	if err := validateIncludes(s.Includes, s.Module); err != nil {
		return fmt.Errorf("baseline: %w", err)
	}
	if !moduleRE.MatchString(s.Module) {
		return fmt.Errorf("baseline module is invalid")
	}
	if err := writeIncludes(dir, s.Includes); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, s.Module+".tla"), s.Model); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.Lock, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "ratified.lock"), string(b)+"\n"); err != nil {
		return err
	}
	if s.View != "" {
		return writeFile(filepath.Join(dir, "view.json"), s.View)
	}
	return nil
}

func (p *Proposal) computeComparison(ctx context.Context, ws string, tc toolchain.Toolchain, authority CheckContext, memo *baselineMemo) error {
	if authority.Baseline == nil {
		return nil
	}
	s := detachedSnapshot(authority.Baseline)
	if s.View == "" {
		b, err := regular.ReadFile(ws, "baseline/view.json")
		if err != nil {
			return fmt.Errorf("baseline needs a pure baseline/view.json for its captured model and original bounds: %w", err)
		}
		s.View = string(b)
	}
	if err := memo.check(ctx, s, tc); err != nil {
		var refusal *baselineReaderRefusal
		if errors.As(err, &refusal) {
			err = fmt.Errorf("%w; %s", err, authority.baselineViewRepair())
		}
		return fmt.Errorf("comparison baseline %s: %w", s.Name, err)
	}
	c, err := statechart.Compare(*s, p.comparisonSnapshot())
	if err != nil {
		return err
	}
	p.Reader.Comparison = c
	p.Reader.Fingerprint, err = p.readerFingerprint()
	return err
}

func (c CheckContext) baselineViewRepair() string {
	if c.Baseline != nil && c.Baseline.View == "" {
		return "supply or repair baseline/view.json for the captured model and original bounds, then check again"
	}
	if c.BaselineViewPath != "" {
		return "repair the project's saved " + c.BaselineViewPath + " in a separate change, then open a new amendment issue; revising the existing issue retains its captured view and cannot capture a view-only fix"
	}
	return "the captured proposal view is fixed; staged baseline/view.json cannot replace it"
}

type baselineReaderRefusal struct{ diagnostic verify.ReaderDiagnostic }

func (e *baselineReaderRefusal) Error() string {
	return e.diagnostic.Code + ": " + e.diagnostic.Message
}

// Recompute from caller-owned sources, including the original lock. Staged
// old files are conveniences for the drafter, never comparison authority.
func checkBaseline(ctx context.Context, s *statechart.ComparisonSnapshot, tc toolchain.Toolchain) error {
	s.Chart, s.Limitation = nil, nil
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "invariant-baseline-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if !moduleRE.MatchString(s.Module) {
		return fmt.Errorf("invalid baseline module")
	}
	if err := validateIncludes(s.Includes, s.Module); err != nil {
		return err
	}
	m := project.Manifest{Name: s.Name, Module: ".invariant/specs/" + s.Module + ".tla", Code: "baseline", Language: "go"}
	// This is a model-only check, not another ratification check. Keep the
	// original lock verbatim in the snapshot while checking precisely its
	// statements and bounds, including legacy ratifications without a hash.
	modelLock := project.Lock{Bounds: s.Lock.Bounds, Statements: s.Lock.Statements}
	for path, value := range map[string]any{".invariant/invariant.json": m, ".invariant/ratified.lock": modelLock} {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, path), string(b)); err != nil {
			return err
		}
	}
	if err := writeFile(filepath.Join(dir, m.Module), s.Model); err != nil {
		return err
	}
	if err := writeIncludes(filepath.Join(dir, filepath.Dir(m.Module)), s.Includes); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, ".invariant/view.json"), s.View); err != nil {
		return err
	}
	r, err := verify.RunModel(ctx, dir, tc)
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("model check returned no baseline receipt")
	}
	if !r.Passed {
		return fmt.Errorf("%s", verify.Feedback(r))
	}
	if r.Reader == nil {
		return fmt.Errorf("model check returned no baseline reader")
	}
	if d := r.Reader.Unavailable; d != nil {
		return &baselineReaderRefusal{diagnostic: *d}
	}
	s.Chart, s.Limitation = r.Reader.Chart, r.Reader.Limitation
	// A successful gate without complete reader evidence is not reusable.
	// The comparison validator also checks pins, bounds and source identity.
	if _, err := statechart.Compare(*s, *s); err != nil {
		return err
	}
	return ctx.Err()
}

// ValidateComparisonContext is the publication boundary for fresh and recovered
// drafts. A valid ordinary chart alone cannot satisfy a required comparison.
func (p *Proposal) ValidateComparisonContext(c CheckContext) error {
	if c.Baseline == nil {
		return nil
	}
	var comparison *statechart.Comparison
	if p.Reader != nil && p.Reader.Comparison != nil {
		var err error
		comparison, err = p.Reader.Comparison.Validated()
		if err != nil {
			return err
		}
	}
	return validateComparisonBaseline(comparison, c.Baseline)
}

// The comparison belongs to this validation call and has already been
// recomputed. Neither mutable proposals nor earlier calls grant authority.
func validateComparisonBaseline(d *statechart.Comparison, baseline *statechart.ComparisonSnapshot) error {
	if d == nil {
		return fmt.Errorf("comparison with baseline %s is missing; check the draft against that captured baseline before publishing", baseline.Name)
	}
	expected, got := detachedSnapshot(baseline), detachedSnapshot(&d.Baseline)
	// The legacy view is the only repairable input. The check, not the agent,
	// supplies both chart outputs, so compare captured sources here.
	if expected.View == "" {
		expected.View = got.View
	}
	expected.Chart, expected.Limitation, got.Chart, got.Limitation = nil, nil, nil, nil
	a, _ := json.Marshal(expected)
	b, _ := json.Marshal(got)
	if !bytes.Equal(a, b) {
		return fmt.Errorf("comparison baseline is stale: expected %s; retain its captured model, includes, view and lock and check again", expected.Name)
	}
	return nil
}
