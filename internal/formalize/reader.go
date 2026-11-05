package formalize

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Reader is computed by the check, never read from an agent's derived files.
// Its fingerprint binds explanatory evidence independently of ratification.
type Reader struct {
	Version     int                `json:"version"`
	Fingerprint string             `json:"fingerprint"`
	Chart       *statechart.Result `json:"chart,omitempty"`
	Limitation  *ReaderLimitation  `json:"limitation,omitempty"`
}

type ReaderLimitation struct {
	Code   string            `json:"code"`
	Actual int               `json:"actual"`
	Limit  int               `json:"limit"`
	Source statechart.Source `json:"source"`
}

func (p *Proposal) computeReader(ctx context.Context, dir string, tc toolchain.Toolchain, states int64) error {
	p.Reader = nil
	cfg := tlc.Config{Constants: maps.Clone(p.Bounds)}
	for _, s := range p.Statements {
		switch s.Kind {
		case project.Spec:
			cfg.Specification = s.Name
		case project.Invariant:
			cfg.Invariants = append(cfg.Invariants, s.Name)
		}
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	graph, err := runner.DumpGraph(ctx, dir, p.Module, cfg)
	if err != nil {
		return fmt.Errorf("reader graph: %w", err)
	}
	if int64(graph.StateCount) != states {
		return fmt.Errorf("reader graph: reachable count differs from the model check")
	}
	in := statechart.Input{Graph: *graph, Lock: project.Lock{Bounds: p.Bounds, Statements: p.Statements}}
	if graph.StateCount <= statechart.MaxStates {
		in.Invariants, err = statechart.EvaluateRules(ctx, runner, dir, *graph, in.Lock)
		if err != nil {
			return err
		}
	}
	// Compute validates the entire view before refusing an oversized graph.
	// This keeps the grammar in one place without evaluating large mappings.
	chart, err := statechart.Compute(ctx, in, []byte(p.View))
	reader := &Reader{Version: 1, Chart: chart}
	if err != nil {
		var refusal *statechart.Refusal
		if !errors.As(err, &refusal) || refusal.Code != "too_large" {
			return err
		}
		reader.Limitation = &ReaderLimitation{Code: refusal.Code, Actual: refusal.Actual, Limit: refusal.Limit,
			Source: statechart.Source{Module: graph.Module, Config: graph.Config, Tool: graph.Tool, TLC: graph.Run.Version}}
	}
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
		Version    int                 `json:"version"`
		Module     string              `json:"module"`
		Model      string              `json:"model"`
		Includes   map[string]string   `json:"includes,omitempty"`
		View       string              `json:"view"`
		Bounds     map[string]string   `json:"bounds"`
		Statements []project.Statement `json:"statements"`
		Chart      *statechart.Result  `json:"chart,omitempty"`
		Limitation *ReaderLimitation   `json:"limitation,omitempty"`
	}{p.Reader.Version, p.Module, p.ModuleText, p.Includes, p.View, p.Bounds, p.Statements, p.Reader.Chart, p.Reader.Limitation})
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
	return b.String()
}

var includeName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\.tla$`)

func validateIncludes(includes map[string]string, module string) error {
	for name := range includes {
		if !includeName.MatchString(name) || name == module+".tla" {
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

func writeReader(dir, view string, reader *Reader) error {
	if view != "" {
		if err := writeFile(filepath.Join(dir, "view.json"), view); err != nil {
			return err
		}
	}
	if reader != nil {
		b, err := json.MarshalIndent(reader, "", "  ")
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
	return json.Unmarshal(b, &c.Reader)
}
