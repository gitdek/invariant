package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

func isPythonApplication(p *project.Project) bool {
	return p.Manifest.Language == "python" && len(p.Manifest.Existing) > 0
}

func pythonApplicationPreflight(p *project.Project) error {
	if p.Manifest.Exhaustive {
		return fmt.Errorf("Python applications support sampled checks, not exhaustive checks")
	}
	inv, err := project.ResolveComponents(p)
	if err != nil {
		return err
	}
	app, err := project.ResolvePythonApplication(p)
	if err != nil {
		return err
	}
	_, err = pythonApplicationInvocations(p, app.Root, inv)
	return err
}

func pythonApplicationTargetSources(p *project.Project, root string, target project.Component) ([]string, error) {
	selected := func(source string) bool {
		for _, name := range p.Manifest.Existing {
			if source == name || strings.HasPrefix(source, name+"/") {
				return true
			}
		}
		return false
	}
	if selected(target.Source) {
		return nil, nil
	}
	// Existing instance adapters may select the surrounding method that calls
	// the named source. Keep that form, but require the delegate on every step.
	if strings.Contains(target.Symbol, ".") {
		imports, err := project.PythonImports(root, target.Source)
		if err != nil {
			return nil, err
		}
		var sources []string
		for _, source := range imports {
			if selected(source) {
				sources = append(sources, source)
			}
		}
		if len(sources) > 0 {
			return sources, nil
		}
	}
	return nil, fmt.Errorf("target is outside existing sources")
}

// Legacy binding discovery supplies the collector with lexical anchors only.
// ResolveComponents and receipts continue to have no component inventory.
func pythonApplicationLegacyBindings(p *project.Project, root string) (targets, drivers []project.Component, err error) {
	seen := map[string]bool{}
	for _, selection := range p.Manifest.Existing {
		err = filepath.WalkDir(filepath.Join(root, filepath.FromSlash(selection)), func(name string, entry fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.IsDir() {
				if entry.Name() == ".git" || entry.Name() == ".venv" || entry.Name() == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, ".py") {
				return nil
			}
			rel, e := filepath.Rel(root, name)
			if e != nil {
				return e
			}
			rel = filepath.ToSlash(rel)
			if seen[rel] {
				return nil
			}
			seen[rel] = true
			cs, e := project.PythonDeclarations(root, rel)
			if e != nil {
				return e
			}
			targets = append(targets, cs...)
			return nil
		})
		if err != nil {
			return
		}
	}
	driver, err := filepath.Rel(root, filepath.Join(p.Dir, p.Manifest.Conformance))
	if err != nil {
		return
	}
	drivers, err = project.PythonDeclarations(root, filepath.ToSlash(driver))
	if err == nil && (len(targets) == 0 || len(drivers) == 0) {
		err = fmt.Errorf("existing sources and driver require lexical callable bindings")
	}
	return
}

func pythonApplicationLegacyInvocations(p *project.Project, root string) ([]PythonApplicationInvocation, error) {
	if _, _, err := pythonApplicationLegacyBindings(p, root); err != nil {
		return nil, err
	}
	dir, err := filepath.Rel(root, p.Dir)
	if err != nil {
		return nil, err
	}
	var out []PythonApplicationInvocation
	for _, v := range []project.ResolvedComponentInvocation{
		{ID: "discovery", Kind: "test", Check: "discovery", Command: "python", Args: []string{"-m", "unittest", "discover"}, WorkingDir: "."},
		{ID: "sample", Kind: "conformance", Check: "driver", Command: "python", Args: []string{p.Manifest.Conformance}, WorkingDir: filepath.ToSlash(dir)},
	} {
		sampling := map[string]string{"runs": "1"}
		if v.Kind == "conformance" {
			sampling = map[string]string{"runs": "1000", "steps": "40", "seed": "1"}
		}
		out = append(out, PythonApplicationInvocation{Key: PythonApplicationInvocationKey{Target: "existing", Check: v.Check, Kind: v.Kind, ID: v.ID}, Declared: v, Sampling: sampling})
	}
	return out, nil
}

func pythonApplicationTraces(x PythonApplicationExecutionResult) ([]byte, error) {
	if !x.Success || !x.Complete || x.ObservationMode != "sampled" || len(x.Observations) == 0 {
		return nil, fmt.Errorf("no complete authenticated sampled observations")
	}
	t := conformance.Traces{}
	for _, row := range x.Observations {
		if row.Kind == "initial" {
			if row.Run != len(t.Traces)+1 {
				return nil, fmt.Errorf("invalid observation run")
			}
			t.Traces = append(t.Traces, []map[string]any{row.State})
		} else {
			if row.Run < 1 || row.Run > len(t.Traces) {
				return nil, fmt.Errorf("invalid observation run")
			}
			t.Traces[row.Run-1] = append(t.Traces[row.Run-1], row.After)
		}
	}
	return json.Marshal(t)
}

func runPythonApplication(ctx context.Context, p *project.Project, outDir string, tc toolchain.Toolchain) (*Report, error) {
	if err := pythonApplicationPreflight(p); err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp("", "invariant-application-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	s, err := PreparePythonApplicationSession(ctx, p, filepath.Join(work, "src"), PythonApplicationSessionOptions{})
	if err != nil {
		return nil, err
	}
	defer s.Close()
	p = s.project
	r, err := gateSelected(ctx, p.Dir, outDir, tc, true, nil, false)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if r == nil {
		return nil, err
	}
	r.ModelOnly = false
	r.Application = &PythonApplicationReport{}
	if err != nil {
		r.Application.ModelError = strings.ReplaceAll(err.Error(), work, "/application")
		r.Passed = false
	}
	r.Toolchain.PythonImage = toolchain.PythonImage
	r.Toolchain.PythonDependencies = s.Provenance().Dependencies
	r.Existing, err = HashExisting(s.root, p.Manifest.Existing)
	if err != nil {
		return nil, err
	}
	r.Build.Passed = true
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	raw, err := os.ReadFile(p.ModulePath())
	if err != nil {
		return nil, err
	}
	all := true
	for i, v := range s.Invocations() {
		x, runErr := s.Run(ctx, v.Key)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if x == nil {
			return nil, runErr
		}
		r.Application.Executions = append(r.Application.Executions, *x)
		all = all && x.Success
		if v.Key.Kind == "test" {
			r.Build.Steps = append(r.Build.Steps, BuildStep{Name: v.Key.ID, Passed: x.Success})
			r.Build.Passed = r.Build.Passed && x.Success
			continue
		}
		c := PythonApplicationConformance{Key: x.Key, Invocation: x.Declared, Bounds: p.Lock.Bounds,
			Result: conformance.Result{ModelStates: r.Design.DistinctStates}}
		rel, _ := filepath.Rel(s.root, p.ModulePath())
		c.Model.Module = filepath.ToSlash(rel)
		lock, _ := filepath.Rel(s.root, filepath.Join(p.Dir, ".invariant", "ratified.lock"))
		for _, f := range s.Provenance().Files {
			if f.Path == filepath.ToSlash(lock) {
				c.BoundsSources = append(c.BoundsSources, f)
			}
			if path.Dir(f.Path) == path.Dir(c.Model.Module) && strings.HasSuffix(f.Path, ".tla") {
				c.Model.Inputs = append(c.Model.Inputs, f)
			}
		}
		traces, traceErr := pythonApplicationTraces(*x)
		if traceErr == nil {
			d, e := stage(work, fmt.Sprintf("conformance-%d", i), p, string(raw), "")
			if e != nil {
				return nil, e
			}
			checks := &pythonApplicationModelRunner{runner: runner, root: d}
			c.Result, e = conformance.Check(ctx, checks, d, p.ModuleName(), p.Lock.Bounds, tla.Variables(string(raw)), r.Design.DistinctStates, traces, nil)
			c.Checks = checks.results
			slices.SortFunc(c.Checks, func(a, b PythonApplicationModelCheck) int {
				return strings.Compare(a.Batch+"/"+a.Module, b.Batch+"/"+b.Module)
			})
			if len(c.Checks) > 0 {
				c.Tool = &ComponentProofTool{Name: "TLC", Version: c.Checks[0].Result.Version, Pins: map[string]string{"release": toolchain.TLCRelease, "jar_sha256": toolchain.TLCJarSHA256, "java_image": tc.JavaImage}}
			}
			for _, check := range c.Checks {
				c.OperationalError = c.OperationalError || check.Error != "" || check.Result.Outcome == tlc.Failed
			}
			if e != nil {
				c.Result.Passed = false
				c.Result.Message = e.Error()
				c.OperationalError = true
			}
		} else {
			c.Result.Message = traceErr.Error()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		all = all && c.Result.Passed
		r.Application.Conformance = append(r.Application.Conformance, c)
	}
	if s.inventory.Mode == "explicit" {
		if err := r.attributePythonApplication(s.inventory); err != nil {
			return nil, err
		}
	}
	r.Passed = r.Passed && all && r.Build.Passed && len(r.Application.Conformance) > 0
	r.Assurance = r.CodeAssurance()
	r.Fingerprint = Fingerprint(*r)
	r.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	return r, nil
}

// Batch completion order is not evidence order. Retain each actual TLC
// invocation, then sort by its stable relative batch path for the receipt.
type pythonApplicationModelRunner struct {
	mu      sync.Mutex
	runner  conformance.Runner
	root    string
	results []PythonApplicationModelCheck
	next    int
	changed chan struct{}
	failed  bool
}

func (r *pythonApplicationModelRunner) Check(ctx context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
	// The shared checker can schedule batches concurrently. Execute application
	// batches in order so a failure has a deterministic actual invocation prefix,
	// with no schedule-dependent canceled TLC runs in its provenance.
	batch, _ := filepath.Rel(r.root, dir)
	order := 0
	if module == "Invariant_Conformance" {
		var err error
		order, err = strconv.Atoi(strings.TrimPrefix(filepath.Base(dir), "steps-"))
		if err != nil {
			return tlc.Result{}, err
		}
	}
	r.mu.Lock()
	for order != r.next && !r.failed && ctx.Err() == nil {
		if r.changed == nil {
			r.changed = make(chan struct{})
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		r.mu.Lock()
	}
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return tlc.Result{}, err
	}
	if r.failed {
		return tlc.Result{}, fmt.Errorf("an earlier application model batch failed")
	}
	defer func() {
		r.next++
		if r.changed != nil {
			close(r.changed)
			r.changed = nil
		}
	}()
	raw, err := os.ReadFile(filepath.Join(dir, module+".tla"))
	if err != nil {
		r.failed = true
		return tlc.Result{}, err
	}
	result, err := r.runner.Check(ctx, dir, module, cfg)
	r.failed = err != nil || result.Outcome != tlc.Passed
	record := PythonApplicationModelCheck{Module: module, Batch: filepath.ToSlash(batch), InputSHA256: pythonApplicationHash(raw), Config: cfg, Result: result}
	// TLC's unstructured progress includes temporary extraction paths. Retain
	// its parsed verdict, diagnostics and counterexample, not progress output.
	record.Result.Printed = nil
	if err != nil {
		record.Error = strings.ReplaceAll(err.Error(), r.root, "/model")
	}
	r.results = append(r.results, record)
	return result, err
}

func checkPythonApplication(ctx context.Context, p *project.Project) (Build, Evidence, error) {
	if err := pythonApplicationPreflight(p); err != nil {
		return Build{}, Evidence{}, err
	}
	work, err := os.MkdirTemp("", "invariant-application-check-")
	if err != nil {
		return Build{}, Evidence{}, err
	}
	defer os.RemoveAll(work)
	s, err := PreparePythonApplicationSession(ctx, p, filepath.Join(work, "src"), PythonApplicationSessionOptions{})
	if err != nil {
		return Build{}, Evidence{}, err
	}
	defer s.Close()
	b, evidence := Build{Passed: true}, Evidence{}
	var combined conformance.Traces
	for _, v := range s.Invocations() {
		x, e := s.Run(ctx, v.Key)
		if ctx.Err() != nil {
			return b, evidence, ctx.Err()
		}
		if x == nil {
			return b, evidence, e
		}
		b.Passed = b.Passed && x.Success
		b.Steps = append(b.Steps, BuildStep{Name: v.Key.ID, Passed: x.Success})
		if v.Key.Kind == "conformance" {
			raw, e := pythonApplicationTraces(*x)
			if e != nil {
				evidence.Message = e.Error()
				continue
			}
			var t conformance.Traces
			d := json.NewDecoder(strings.NewReader(string(raw)))
			d.UseNumber()
			if e := d.Decode(&t); e != nil {
				return b, evidence, e
			}
			combined.Traces = append(combined.Traces, t.Traces...)
		}
	}
	if len(combined.Traces) > 0 {
		evidence.Traces, err = json.Marshal(combined)
	}
	return b, evidence, err
}
