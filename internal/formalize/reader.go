package formalize

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Reader is computed by the check, never read from an agent's derived files.
// Its fingerprint binds explanatory evidence independently of ratification.
type Reader struct {
	Version     int                    `json:"version"`
	Fingerprint string                 `json:"fingerprint"`
	Chart       *statechart.Result     `json:"chart,omitempty"`
	Limitation  *ReaderLimitation      `json:"limitation,omitempty"`
	Comparison  *statechart.Comparison `json:"comparison,omitempty"`
}

type ReaderLimitation = statechart.Limitation

// Only a project's reader uses this wire reference. In-memory readers and
// saved proposals, formalizations and receipts keep the complete comparison.
type comparisonReference struct {
	Version     int    `json:"version"`
	File        string `json:"file"`
	Fingerprint string `json:"fingerprint"`
}

func (p *Proposal) computeReader(ctx context.Context, dir string, tc toolchain.Toolchain, states int64) error {
	p.Reader = nil
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	chart, limitation, err := statechart.Read(ctx, runner, dir, p.Module,
		project.Lock{Bounds: p.Bounds, Statements: p.Statements}, []byte(p.View), states)
	if err != nil {
		return err
	}
	reader := &Reader{Version: 1, Chart: chart, Limitation: limitation}
	p.Reader = reader
	reader.Fingerprint, err = p.readerFingerprint()
	if err != nil {
		p.Reader = nil
	}
	return err
}

func (p *Proposal) readerFingerprint() (string, error) {
	// Factory metadata (target, language, agent, directory) can change after
	// checking. Only the inputs to the computation and its output belong here.
	b, err := json.Marshal(struct {
		Version    int                    `json:"version"`
		Module     string                 `json:"module"`
		Model      string                 `json:"model"`
		Includes   map[string]string      `json:"includes,omitempty"`
		View       string                 `json:"view"`
		Bounds     map[string]string      `json:"bounds"`
		Statements []project.Statement    `json:"statements"`
		Chart      *statechart.Result     `json:"chart,omitempty"`
		Limitation *ReaderLimitation      `json:"limitation,omitempty"`
		Comparison *statechart.Comparison `json:"comparison,omitempty"`
	}{p.Reader.Version, p.Module, p.ModuleText, p.Includes, p.View, p.Bounds, p.Statements, p.Reader.Chart, p.Reader.Limitation, p.Reader.Comparison})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b)), nil
}

// ValidateReader refuses missing or stale evidence before new publication.
// Older proposals without a reader remain readable and writable as before.
func (p *Proposal) ValidateReader() error {
	r := p.Reader
	if r == nil || p.View == "" {
		return fmt.Errorf("reader evidence is missing: check the modeled draft with view.json before publishing")
	}
	if r.Version != 1 || (r.Chart == nil) == (r.Limitation == nil) {
		return fmt.Errorf("reader evidence must contain exactly one version 1 chart or limitation; check the draft again")
	}
	if r.Chart != nil && r.Chart.Version != 1 {
		return fmt.Errorf("reader chart version is unsupported; check the draft again")
	}
	if l := r.Limitation; l != nil && (l.Code != "too_large" || l.Limit != statechart.MaxStates || l.Actual <= l.Limit) {
		return fmt.Errorf("reader size limitation is invalid; check the draft again")
	}
	want, err := p.readerFingerprint()
	if err != nil {
		return err
	}
	if r.Fingerprint != want {
		return fmt.Errorf("reader evidence is stale: model, includes, view, bounds, statements, tools or artifact changed; check the draft again")
	}
	var comparison *statechart.Comparison
	if c := r.Comparison; c != nil {
		// Recompute against the current candidate once. Comparing the whole
		// record checks every retained field as well as the candidate binding.
		comparison, err = statechart.Compare(c.Baseline, p.comparisonSnapshot())
		if err != nil {
			return err
		}
		got, err := json.Marshal(c)
		if err != nil {
			return err
		}
		want, err := json.Marshal(comparison)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("comparison candidate or retained evidence is stale; check against the captured baseline again")
		}
	}
	if p.Target != nil && p.Target.Baseline != nil {
		if err := validateComparisonBaseline(comparison, currentSnapshot(p.Target.Baseline)); err != nil {
			return err
		}
	}
	if p.Baseline != nil {
		if err := validateComparisonBaseline(comparison, p.Baseline); err != nil {
			return err
		}
	}
	return validateIncludes(p.Includes, p.Module)
}

// ReaderFeedback accompanies the model gate's feedback on a passing draft.
func (p *Proposal) ReaderFeedback() string {
	if p.Reader == nil {
		return ""
	}
	var b strings.Builder
	if l := p.Reader.Limitation; l != nil {
		fmt.Fprintf(&b, "Reader too_large: TLC reached %d states, more than the %d charts are drawn for. The chart is not drawn.", l.Actual, l.Limit)
	} else {
		c := p.Reader.Chart.Report.Counts
		fmt.Fprintf(&b, "Reader checked: %d TLC states, %d TLC steps, %d generated states; %d chart states, %d visible arrows, %d hidden steps.",
			c["tlc_states"], c["tlc_steps"], c["tlc_generated_states"], c["chart_states"], c["chart_arrows"], c["hidden_steps"])
	}
	b.WriteString(" Checked bounds:")
	for _, name := range slices.Sorted(maps.Keys(p.Bounds)) {
		fmt.Fprintf(&b, " %s = %s;", name, p.Bounds[name])
	}
	if c := p.Reader.Comparison; c != nil {
		fmt.Fprintf(&b, " Comparison checked against %s. %s", c.Baseline.Name, strings.Join(c.Qualifications(), " "))
	}
	return b.String()
}

func validateIncludes(includes map[string]string, module string) error {
	for name := range includes {
		if !tla.ValidIncludeName(name, module) {
			return fmt.Errorf("reader include %q must be a sibling TLA+ file distinct from the model", name)
		}
	}
	return nil
}

func readIncludes(dir, module string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	includes := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".tla") || name == module+".tla" {
			continue
		}
		b, err := regular.ReadFile(dir, name)
		if err != nil {
			return nil, err
		}
		includes[name] = string(b)
	}
	return includes, validateIncludes(includes, module)
}

func writeIncludes(dir string, includes map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(includes)) {
		if err := writeFile(filepath.Join(dir, name), includes[name]); err != nil {
			return err
		}
	}
	return nil
}

// A checked amendment replaces the include set, including removals. Leaving
// an old sibling behind would write inputs different from the saved snapshot.
func removeOldIncludes(dir, module string, includes map[string]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".tla") || name == module+".tla" {
			continue
		}
		if _, keep := includes[name]; !keep {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeReader(dir, view string, reader *Reader, reference bool) error {
	for _, name := range []string{"reader.json", "comparison.json"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if view != "" {
		if err := writeFile(filepath.Join(dir, "view.json"), view); err != nil {
			return err
		}
	}
	if reader != nil {
		if reader.Comparison != nil {
			b, err := json.MarshalIndent(reader.Comparison, "", "  ")
			if err != nil {
				return err
			}
			if err := writeFile(filepath.Join(dir, "comparison.json"), string(b)+"\n"); err != nil {
				return err
			}
		}
		var record any = reader
		if reference && reader.Comparison != nil {
			copyReader := *reader
			copyReader.Comparison = nil
			record = struct {
				Reader
				ComparisonRef comparisonReference `json:"comparison_ref"`
			}{copyReader, comparisonReference{Version: 1, File: "comparison.json", Fingerprint: reader.Comparison.Fingerprint}}
		}
		b, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return err
		}
		return writeFile(filepath.Join(dir, "reader.json"), string(b)+"\n")
	}
	return nil
}

// ReadReader loads a current project's optional saved explanation and its
// sibling includes. Legacy projects need no invented view or evidence.
func (c *Current) ReadReader(dir string) error {
	c.Reader = nil
	var err error
	c.Includes, err = readIncludes(filepath.Join(dir, filepath.Dir(c.Manifest.Module)), c.ModuleName())
	if err != nil {
		return err
	}
	b, err := regular.ReadFile(dir, ".invariant/view.json")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	c.View = string(b)
	b, err = regular.ReadFile(dir, ".invariant/reader.json")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved struct {
		*Reader
		ComparisonRef json.RawMessage `json:"comparison_ref"`
	}
	if err := json.Unmarshal(b, &saved); err != nil {
		return fmt.Errorf("reader comparison record: %w", err)
	}
	var ref comparisonReference
	if len(saved.ComparisonRef) != 0 {
		if err := json.Unmarshal(saved.ComparisonRef, &ref); err != nil {
			return fmt.Errorf("comparison reference: %w", err)
		}
		if ref.Version != 1 || ref.File != "comparison.json" || ref.Fingerprint == "" || saved.Reader == nil || saved.Version != 1 {
			return fmt.Errorf("comparison reference requires a version 1 reader and {version:1, file:comparison.json, fingerprint}")
		}
	}
	// A sibling alone cannot add a comparison to an ordinary or absent reader.
	if saved.Reader == nil || saved.Comparison == nil && len(saved.ComparisonRef) == 0 {
		c.Reader = saved.Reader
		return nil
	}
	if saved.Comparison != nil {
		if err := saved.Comparison.Validate(); err != nil {
			return err
		}
	}
	data, err := regular.ReadFile(dir, ".invariant/comparison.json")
	if os.IsNotExist(err) && len(saved.ComparisonRef) == 0 {
		c.Reader = saved.Reader
		return nil
	}
	if err != nil {
		return fmt.Errorf("comparison evidence: %w", err)
	}
	var sibling statechart.Comparison
	if err := json.Unmarshal(data, &sibling); err != nil {
		return fmt.Errorf("comparison evidence: %w", err)
	}
	comparison, err := sibling.Validated()
	if err != nil {
		return err
	}
	if len(saved.ComparisonRef) != 0 && ref.Fingerprint != comparison.Fingerprint {
		return fmt.Errorf("comparison reference fingerprint differs from comparison.json")
	}
	if saved.Comparison != nil {
		inline, err := json.Marshal(saved.Comparison)
		if err != nil {
			return err
		}
		want, err := json.Marshal(comparison)
		if err != nil {
			return err
		}
		if !bytes.Equal(inline, want) {
			return fmt.Errorf("inline comparison differs from comparison.json")
		}
	}
	saved.Comparison = comparison
	c.Reader = saved.Reader
	return nil
}
