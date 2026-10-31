package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
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

// pythonApplicationGateOptions are the options the gate prepares its
// session with. The gate's own are the zero value: every selected proof runs
// the pinned Nagini, and the dependency image's own records name its
// distributions. This package's tests substitute doubles; no caller can.
var pythonApplicationGateOptions PythonApplicationSessionOptions

func pythonApplicationPreflight(p *project.Project) error {
	_, err := pythonApplicationAdmit(p)
	return err
}

// pythonApplicationAdmit checks a Python application's configuration before
// any tool runs, and returns its inventory.
func pythonApplicationAdmit(p *project.Project) (*project.ComponentInventory, error) {
	inv, err := project.ResolveComponents(p)
	if err != nil {
		return nil, err
	}
	// The gate qualifies an in-place proof only beside conformance on the
	// same function (#359): its collector binds what conformance observes to
	// the function's exact staged declaration, the same bytes Nagini's input
	// copies, so the function proved is the function the application runs.
	for _, check := range inv.Checks {
		if check.Proof != nil && len(check.Conformance) == 0 {
			return nil, fmt.Errorf("%s selects a proof and no conformance: the gate qualifies an in-place proof only beside conformance on the same function, which shows the application runs the declaration Nagini proved", check.Target)
		}
	}
	app, err := project.ResolvePythonApplication(p)
	if err != nil {
		return nil, err
	}
	if _, err := pythonApplicationInvocations(p, app.Root, inv); err != nil {
		return nil, err
	}
	return inv, nil
}

// pythonApplicationProofTargets are an inventory's in-place proof selections,
// in the inventory's order.
func pythonApplicationProofTargets(inv *project.ComponentInventory) []string {
	var out []string
	if inv != nil {
		for _, check := range inv.Checks {
			if check.Proof != nil {
				out = append(out, check.Target)
			}
		}
	}
	return out
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
	dir, err := filepath.Abs(p.Dir)
	if err != nil {
		return
	}
	driver, err := filepath.Rel(root, filepath.Join(dir, p.Manifest.Conformance))
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
	dir, err := filepath.Abs(p.Dir)
	if err != nil {
		return nil, err
	}
	dir, err = filepath.Rel(root, dir)
	if err != nil {
		return nil, err
	}
	// An exhaustive manifest's driver explores rather than samples (#342).
	id := "sample"
	if p.Manifest.Exhaustive {
		id = "explore"
	}
	var out []PythonApplicationInvocation
	for _, v := range []project.ResolvedComponentInvocation{
		{ID: "discovery", Kind: "test", Check: "discovery", Command: "python", Args: []string{"-m", "unittest", "discover"}, WorkingDir: "."},
		{ID: id, Kind: "conformance", Check: "driver", Command: "python", Args: []string{p.Manifest.Conformance}, WorkingDir: filepath.ToSlash(dir)},
	} {
		sampling := map[string]string{"runs": "1"}
		if v.Kind == "conformance" {
			sampling = map[string]string{"runs": "1000", "steps": "40", "seed": "1"}
		}
		out = append(out, PythonApplicationInvocation{Key: PythonApplicationInvocationKey{Target: "existing", Check: v.Check, Kind: v.Kind, ID: v.ID}, Declared: v, Sampling: sampling,
			Explored: v.Kind == "conformance" && p.Manifest.Exhaustive})
	}
	return out, nil
}

var pythonApplicationRecordField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Only the module boundary gives lists sequence semantics. Application keys
// are ordinary record fields, never instructions to the conformance encoder.
func pythonApplicationModelValue(value any) (any, error) {
	switch v := value.(type) {
	case []any:
		items := make([]any, len(v))
		for i, item := range v {
			converted, err := pythonApplicationModelValue(item)
			if err != nil {
				return nil, err
			}
			items[i] = converted
		}
		return map[string]any{"$seq": items}, nil
	case map[string]any:
		fields := make(map[string]any, len(v))
		for key, item := range v {
			if !pythonApplicationRecordField.MatchString(key) {
				return nil, fmt.Errorf("module observation record field %q is not a TLA+ identifier", key)
			}
			converted, err := pythonApplicationModelValue(item)
			if err != nil {
				return nil, err
			}
			fields[key] = converted
		}
		return fields, nil
	default:
		if _, err := conformance.Encode(value); err != nil {
			return nil, fmt.Errorf("module observation cannot encode as TLA+: %w", err)
		}
		return value, nil
	}
}

func pythonApplicationTraces(x PythonApplicationExecutionResult) ([]byte, error) {
	if !x.Success || !x.Complete || x.ObservationMode != "sampled" || len(x.Observations) == 0 {
		return nil, fmt.Errorf("no complete authenticated sampled observations")
	}
	t := conformance.Traces{}
	for _, row := range x.Observations {
		state := row.After
		if row.Kind == "initial" {
			state = row.State
		}
		if row.Form == "module" {
			converted, err := pythonApplicationModelValue(state)
			if err != nil {
				return nil, err
			}
			state = converted.(map[string]any)
		}
		if row.Kind == "initial" {
			if row.Run != len(t.Traces)+1 {
				return nil, fmt.Errorf("invalid observation run")
			}
			t.Traces = append(t.Traces, []map[string]any{state})
		} else {
			if row.Run < 1 || row.Run > len(t.Traces) {
				return nil, fmt.Errorf("invalid observation run")
			}
			t.Traces[row.Run-1] = append(t.Traces[row.Run-1], state)
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
	s, err := PreparePythonApplicationSession(ctx, p, filepath.Join(work, "src"), pythonApplicationGateOptions)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	// Every selected proof's verifier input is built, and the application
	// shown to import the contracts its module reads, before anything runs:
	// a selection the input can't preserve is refused, as preflight refuses
	// configuration (#359).
	proofTargets := pythonApplicationProofTargets(s.inventory)
	if err := s.preparePythonApplicationProofs(ctx, proofTargets); err != nil {
		return nil, err
	}
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
	contracts := pythonApplicationContracts(s.inventory)
	var contractSteps [][2]int
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
			name, contract := pythonApplicationContractStep(contracts, v.Key)
			if contract {
				contractSteps = append(contractSteps, [2]int{len(r.Build.Steps), len(r.Application.Executions) - 1})
			}
			r.Build.Steps = append(r.Build.Steps, BuildStep{Name: name, Passed: x.Success})
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
		var traces []byte
		var traceErr error
		var model *conformance.Model
		if !v.Explored {
			traces, traceErr = pythonApplicationTraces(*x)
		} else {
			// An explored driver's attempts are checked as every exhaustive
			// driver's are: against the model's named steps, with the
			// environment's bounds one larger (D-0082, D-0085, #342).
			c.Mode = "bounded-exhaustive"
			c.Result.Exhaustive = true
			traces, traceErr = pythonApplicationAttempts(*x)
			model = &conformance.Model{Larger: environmentLarger(p.Lock.Bounds, p.Manifest.Parameters)}
			if steps, err := tla.Steps(string(raw)); err != nil {
				model.Problem = err.Error()
			} else {
				model.Steps = steps
			}
		}
		if traceErr == nil {
			d, e := stage(work, fmt.Sprintf("conformance-%d", i), p, string(raw), "")
			if e != nil {
				return nil, e
			}
			checks := &pythonApplicationModelRunner{runner: runner, root: d}
			c.Result, e = conformance.Check(ctx, checks, d, p.ModuleName(), p.Lock.Bounds, tla.Variables(string(raw)), r.Design.DistinctStates, traces, model)
			if v.Explored {
				// Attempts carry no runs; the exploration's are its replays.
				c.Result.Exhaustive, c.Result.Runs = true, x.Exploration.Runs
				// It claims every state the code can reach, so it must reach
				// every state the model does; its checked steps still stand.
				if c.Result.Passed && int64(c.Result.States) != c.Result.ModelStates {
					c.Result.Message = fmt.Sprintf("the driver explores every state the code can reach, and it reached %d of the model's %d", c.Result.States, c.Result.ModelStates)
				}
			}
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
		all = all && c.passed()
		r.Application.Conformance = append(r.Application.Conformance, c)
	}
	if p.Manifest.Exhaustive {
		if err := runPythonApplicationLarger(ctx, s, r, p, work, string(raw), runner); err != nil {
			return nil, err
		}
	}
	// Each selected function is proved in its module, by the pinned Nagini,
	// apart from the application's image (#359).
	var proofs []ComponentProofResult
	for _, target := range proofTargets {
		proof, err := s.Prove(ctx, target)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, err
		}
		r.Application.Proofs = append(r.Application.Proofs, *proof)
		proofs = append(proofs, proof.Result)
	}
	if len(proofTargets) > 0 {
		r.Toolchain.NaginiRecipe = toolchain.NaginiRecipe()
	}
	if s.inventory.Mode == "explicit" {
		if err := r.attributePythonApplication(s.inventory); err != nil {
			return nil, err
		}
		// Conformance-only applications keep their reports exactly as before.
		if len(proofTargets) > 0 {
			if err := r.AttributeComponentProofs(s.inventory, proofs); err != nil {
				return nil, err
			}
		}
		r.Build.Passed = r.pythonApplicationContractSteps(contractSteps) && r.Build.Passed
	}
	// Code that agrees one size larger only when TLC can say what that is
	// carries no bound; code that disagrees there fails (D-0068, D-0076).
	// Every declared trusted contract's backing tests are required, whether
	// or not a proof uses it yet, and every selected proof must qualify.
	larger := r.Larger == nil || !r.Larger.Required || r.Larger.Passed
	r.Passed = r.Passed && all && larger && r.Build.Passed && len(r.Application.Conformance) > 0 && r.pythonApplicationContractsTested() &&
		len(r.pythonApplicationProofProblems()) == 0
	r.Assurance = r.CodeAssurance()
	r.Fingerprint = Fingerprint(*r)
	r.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	return r, nil
}

// pythonApplicationLargerTimeouts are the allowances a larger run has: TLC's
// is every language's, and an exploration's is a driver count's, twice it.
// Tests shorten them.
var pythonApplicationLargerTimeouts = struct{ model, explore time.Duration }{largerTimeout, countTimeout}

// runPythonApplicationLarger checks an exhaustive application one size past
// the ratified bounds when its driver names every bound with a next size, as
// every exhaustive driver is checked (D-0068, D-0076). TLC runs first; only
// once it finishes there does each explored invocation run again, with the
// same snapshot and image, its driver copy supplying the larger bounds.
func runPythonApplicationLarger(ctx context.Context, s *PythonApplicationSession, r *Report, p *project.Project, work, src string, runner tlc.Runner) error {
	larger, sizes := largerBounds(p.Lock.Bounds)
	driver, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(s.driver)))
	if err != nil {
		return err
	}
	if !declaresBounds(driver, sizes) {
		return nil
	}
	d, err := stage(work, "larger", p, src, "")
	if err != nil {
		return err
	}
	timeouts := pythonApplicationLargerTimeouts
	tctx, cancel := context.WithTimeout(ctx, timeouts.model)
	res, err := runner.Check(tctx, d, p.ModuleName(), tlc.Config{Specification: p.SpecName(), Constants: larger, Invariants: p.Invariants()})
	if err == nil && tctx.Err() != nil {
		err = tctx.Err()
	}
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		r.Larger = compareLarger(larger, nil, nil, fmt.Sprintf("TLC didn't finish one size larger within %s: %v", timeouts.model, err))
		return nil
	}
	if res.Outcome != tlc.Passed {
		r.Larger = compareLarger(larger, &res, nil, "")
		return nil
	}
	original := project.ComponentFile{Path: s.driver, SHA256: pythonApplicationHash(driver)}
	for _, v := range s.Invocations() {
		if !v.Explored {
			continue
		}
		lctx, cancel := context.WithTimeout(ctx, timeouts.explore)
		x, file, err := s.RunLarger(lctx, v.Key, sizes)
		unfinished := lctx.Err() != nil
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var e Exploration
		switch {
		case unfinished:
			// As a driver's count does, an exploration that runs out claims
			// nothing and fails nothing.
			e = Exploration{Unfinished: true, Message: fmt.Sprintf("the driver didn't finish exploring one size larger within %s", timeouts.explore)}
		case x == nil:
			return err
		case !x.Success || x.Exploration == nil:
			e = Exploration{Message: lastLines(strings.Join(x.Diagnostics, "\n"), 20)}
		default:
			e = Exploration{OK: true, States: int64(x.Exploration.States), Depth: x.Exploration.Depth}
		}
		l := compareLarger(larger, &res, &e, "")
		r.Application.Larger = append(r.Application.Larger, PythonApplicationLarger{Key: v.Key, Bounds: maps.Clone(larger), Driver: file, Original: original, Execution: x, Result: *l})
	}
	// The report claims the weakest result: any disagreement, else any run
	// that couldn't finish, else agreement.
	weakness := func(l *Larger) int {
		switch {
		case l.Required && !l.Passed:
			return 2
		case !l.Required:
			return 1
		}
		return 0
	}
	for i := range r.Application.Larger {
		l := r.Application.Larger[i].Result
		if r.Larger == nil || weakness(&l) > weakness(r.Larger) {
			r.Larger = &l
		}
	}
	return nil
}

// Batch completion order is not evidence order. Retain each actual TLC
// invocation, then sort by its stable relative batch path for the receipt.
type pythonApplicationModelRunner struct {
	mu      sync.Mutex
	runner  conformance.Runner
	root    string
	results []PythonApplicationModelCheck
	phase   int
	next    int
	changed chan struct{}
	failed  bool
}

func (r *pythonApplicationModelRunner) Check(ctx context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
	// The shared checker can schedule batches concurrently. Execute application
	// batches in order so a failure has a deterministic actual invocation prefix,
	// with no schedule-dependent canceled TLC runs in its provenance. Starting
	// states come first, then the steps' batches, then the attempts'.
	batch, _ := filepath.Rel(r.root, dir)
	phase, order := 0, 0
	for i, prefix := range []string{"steps-", "tried-"} {
		if module == []string{"Invariant_Conformance", "Invariant_Tried"}[i] {
			var err error
			phase = i + 1
			order, err = strconv.Atoi(strings.TrimPrefix(filepath.Base(dir), prefix))
			if err != nil {
				return tlc.Result{}, err
			}
		}
	}
	r.mu.Lock()
	if phase < r.phase {
		r.mu.Unlock()
		return tlc.Result{}, fmt.Errorf("an application model batch ran out of order")
	}
	if phase > r.phase {
		r.phase, r.next = phase, 1
	}
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
	inv, err := pythonApplicationAdmit(p)
	if err != nil {
		return Build{}, Evidence{}, err
	}
	// The direct route returns a build and traces, never a proof: an in-place
	// proof qualifies only in verify.Run, beside the conformance and the
	// backing tests it rests on (#359).
	if targets := pythonApplicationProofTargets(inv); len(targets) > 0 {
		return Build{}, Evidence{}, fmt.Errorf("%s selects a proof, which the direct Python check doesn't run: an in-place proof qualifies only in the gate, verify.Run, beside the conformance and backing tests it rests on", targets[0])
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
	var explored [][]byte
	contracts := pythonApplicationContracts(s.inventory)
	var contractSteps [][2]int
	var executions []PythonApplicationExecutionResult
	for _, v := range s.Invocations() {
		x, e := s.Run(ctx, v.Key)
		if ctx.Err() != nil {
			return b, evidence, ctx.Err()
		}
		if x == nil {
			return b, evidence, e
		}
		executions = append(executions, *x)
		name, contract := pythonApplicationContractStep(contracts, v.Key)
		if contract {
			contractSteps = append(contractSteps, [2]int{len(b.Steps), len(executions) - 1})
		}
		b.Passed = b.Passed && x.Success
		b.Steps = append(b.Steps, BuildStep{Name: name, Passed: x.Success})
		if v.Key.Kind == "conformance" && v.Explored {
			raw, e := pythonApplicationAttempts(*x)
			if e != nil {
				evidence.Message = e.Error()
				continue
			}
			explored = append(explored, raw)
			continue
		}
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
	// Backing-test runs pass here as they pass the gate: each run's own row
	// tested, against the binding its session records, and every declared
	// contract tested.
	if len(contracts) > 0 {
		r, e := pythonApplicationContractRows(s.inventory, s.Provenance(), executions)
		if e != nil {
			return b, evidence, e
		}
		r.Build.Steps = b.Steps
		b.Passed = r.pythonApplicationContractSteps(contractSteps) && b.Passed && r.pythonApplicationContractsTested()
		b.Steps = r.Build.Steps
	}
	if len(combined.Traces) > 0 {
		evidence.Traces, err = json.Marshal(combined)
	}
	if len(explored) > 0 {
		evidence.Traces, err = mergePythonApplicationAttempts(explored)
	}
	return b, evidence, err
}
