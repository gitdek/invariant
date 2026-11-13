package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
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
// session with. The gate's own are the zero value: every eligible proof runs
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
	// The collector binds either the selected function or declared calls
	// inside explored entries to the exact staged declaration (#359, #793).
	for _, check := range inv.Checks {
		if check.Proof != nil && len(check.Conformance) == 0 && !pythonApplicationObservedTarget(inv, check.Target) {
			return nil, fmt.Errorf("%s selects a proof and no conformance: select conformance on the same function or observe it in another check's exhaustive conformance, to show the application runs the declaration Nagini proved", check.Target)
		}
	}
	app, err := project.ResolvePythonApplication(p)
	if err != nil {
		return nil, err
	}
	if _, err := pythonApplicationInvocations(p, app.Root, inv); err != nil {
		return nil, err
	}
	for _, check := range inv.Checks {
		if check.Proof == nil {
			continue
		}
		i := slices.IndexFunc(inv.Components, func(c project.Component) bool { return c.ID == check.Target })
		if i < 0 {
			return nil, fmt.Errorf("%s selects a proof and isn't in the inventory", check.Target)
		}
		if err := pythonApplicationProofShape(app.Root, inv.Components[i]); err != nil {
			return nil, err
		}
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
	if x.ObservationMode != "sampled" {
		return nil, fmt.Errorf("no complete authenticated sampled observations")
	}
	var out bytes.Buffer
	if err := writePythonApplicationRecording(context.Background(), &out, x); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// The session's sorted snapshot already selects every staged regular file.
// Hash its original bytes without applying the TypeScript package copier's
// exclusions again: Python applications can use files under bin/ and out/.
func pythonApplicationHashExisting(root string, existing []string, files []project.ComponentFile) ([]ExistingCode, error) {
	var out []ExistingCode
	for _, selection := range existing {
		h := sha256.New()
		count := 0
		for _, file := range files {
			if file.Path != selection && !strings.HasPrefix(file.Path, selection+"/") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path)))
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(h, "%s\n%d\n", file.Path, len(b))
			h.Write(b)
			count++
		}
		out = append(out, ExistingCode{Path: selection, Files: count, SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil))})
	}
	return out, nil
}

func runPythonApplication(ctx context.Context, p *project.Project, outDir string, tc toolchain.Toolchain, options Options) (*Report, error) {
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
	dir, lock := p.Dir, p.Lock
	p = s.project
	budget, err := tlcBudget(ctx, options.TLCParallel)
	if err != nil {
		return nil, err
	}
	r, err := gateSelected(ctx, p.Dir, outDir, tc, true, nil, false, options, budget)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if r == nil {
		return nil, err
	}
	defer func() { r.TLCAttempts = budget.Attempts() }()
	// The gate ran on the staged copy, outside the repository, so the lock's
	// ratification is read where the project is.
	ratification(ctx, r, dir, lock)
	r.Passed = r.Passed && r.RatificationMatches()
	r.ModelOnly = false
	replaysOK := activateBugReplays(r)
	r.Passed = r.Passed && replaysOK
	r.Application = &PythonApplicationReport{}
	if err != nil {
		r.Application.ModelError = strings.ReplaceAll(err.Error(), work, "/application")
		r.Passed = false
	}
	r.Toolchain.PythonImage = toolchain.PythonImage
	r.Toolchain.PythonDependencies = s.Provenance().Dependencies
	r.Existing, err = pythonApplicationHashExisting(s.root, p.Manifest.Existing, s.Provenance().Files)
	if err != nil {
		return nil, err
	}
	r.Build.Passed = true
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar, Budget: budget, CheckRoot: work}
	raw, err := os.ReadFile(p.ModulePath())
	if err != nil {
		return nil, err
	}
	includes, err := tla.Siblings(filepath.Dir(p.ModulePath()), filepath.Base(p.ModulePath()))
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
		c, e := checkPythonApplicationConformance(ctx, s, x, v.Explored, work, fmt.Sprintf("conformance-%d", i), string(raw), includes, p.Lock.Bounds, r.Design.DistinctStates, runner)
		if e != nil {
			return nil, e
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
	if s.inventory.Mode == "explicit" {
		if err := r.attributePythonApplication(s.inventory); err != nil {
			return nil, err
		}
	}
	// Qualify each prepared input against the evidence already collected,
	// before acquiring or running Nagini (#849).
	var proofs []ComponentProofResult
	for _, target := range proofTargets {
		proof, err := s.prove(ctx, target, func(proof *PythonApplicationProof) []string {
			e := *r.Components
			e.Proofs = []ComponentProof{{Target: target, Result: &proof.Result}}
			a := *r.Application
			a.Proofs = append(slices.Clone(a.Proofs), *proof)
			return pythonApplicationProofPrerequisites(&e, target, &proof.Result, &a)
		})
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
		// Conformance-only applications keep their reports exactly as before.
		if len(proofTargets) > 0 {
			if err := r.AttributeComponentProofs(s.inventory, proofs); err != nil {
				return nil, err
			}
			r.qualifyPythonApplicationProofs()
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

// Both sizes check the same authenticated recording, including declared outcomes
// and omissions evaluated at the next environment bounds from this size.
func checkPythonApplicationConformance(ctx context.Context, s *PythonApplicationSession, x *PythonApplicationExecutionResult, explored bool, work, name, src string, includes map[string]string, bounds map[string]string, modelStates int64, runner tlc.Runner) (PythonApplicationConformance, error) {
	p := s.project
	c := PythonApplicationConformance{Key: x.Key, Invocation: x.Declared, Bounds: maps.Clone(bounds),
		Result: conformance.Result{ModelStates: modelStates}}
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
	recording, traceErr := pythonApplicationRecording(ctx, *x, explored)
	var model *conformance.Model
	if explored {
		// An explored driver's attempts are checked as every exhaustive
		// driver's are: against the model's named steps, with the
		// environment's bounds one larger (D-0082, D-0085, #342).
		c.Mode = "bounded-exhaustive"
		c.Result.Exhaustive = true
		model = &conformance.Model{Larger: environmentLarger(bounds, p.Manifest.Parameters)}
		if steps, err := modelSteps(src, includes); err != nil {
			model.Problem = err.Error()
		} else {
			model.Steps = steps
		}
	}
	if traceErr == nil {
		d, e := stage(work, name, p, src, "")
		if e != nil {
			return c, errors.Join(e, recording.close())
		}
		defer os.RemoveAll(d)
		checks := &pythonApplicationModelRunner{runner: runner, root: d}
		c.Result, e = recording.check(ctx, checks, d, p.ModuleName(), bounds, modelVariables(src, includes), modelStates, model)
		if explored {
			// Attempts carry no runs; the exploration's are its replays.
			c.Result.Exhaustive, c.Result.Runs = true, x.Exploration.Runs
			// It claims every state the code can reach, so it must reach
			// every state the model does; its checked steps still stand.
			if c.Result.Passed && int64(c.Result.States) != c.Result.ModelStates {
				c.Result.Message = fmt.Sprintf("the driver explores every state the code can reach, and it reached %d of the model's %d", c.Result.States, c.Result.ModelStates)
			}
		}
		c.Checks = checks.results
		c.LogicalChecks = checks.logical
		slices.SortFunc(c.Checks, func(a, b PythonApplicationModelCheck) int {
			return strings.Compare(a.Batch+"/"+a.Module, b.Batch+"/"+b.Module)
		})
		if len(c.Checks) > 0 {
			c.Tool = &ComponentProofTool{Name: "TLC", Version: c.Checks[0].Result.Version, Pins: map[string]string{"release": toolchain.TLCRelease, "jar_sha256": toolchain.TLCJarSHA256, "java_image": runner.Image}}
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
		var pathError *os.PathError
		c.OperationalError = ctx.Err() != nil || errors.As(traceErr, &pathError)
		if x.HarnessError != "" {
			c.Result.Message += ": " + x.HarnessError
		}
	}
	return c, nil
}

// pythonApplicationLargerTimeouts are the allowances a larger run has: TLC's
// is every language's; exploration, recording conversion and conformance
// share a driver count's allowance, twice it (#881).
// Tests shorten them.
var pythonApplicationLargerTimeouts = struct{ model, explore time.Duration }{largerTimeout, countTimeout}

// runPythonApplicationLarger checks an exhaustive application one size past
// the ratified bounds when an invocation's driver names every bound with a
// next size, as every exhaustive driver is checked (D-0068, D-0076). It needs
// passing base execution and conformance first (#849). TLC runs only when
// one qualifies; once it finishes there, eligible invocations run again with
// the same snapshot and image, their driver copy supplying the larger bounds.
func runPythonApplicationLarger(ctx context.Context, s *PythonApplicationSession, r *Report, p *project.Project, work, src string, runner tlc.Runner) error {
	larger, sizes := largerBounds(p.Lock.Bounds)
	var eligible []PythonApplicationInvocation
	originals := map[PythonApplicationInvocationKey]project.ComponentFile{}
	order := map[PythonApplicationInvocationKey]int{}
	for i, v := range s.Invocations() {
		if !v.Explored {
			continue
		}
		name := s.driverPath(v.Key)
		driver, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if !declaresBounds(driver, sizes) {
			continue
		}
		original := project.ComponentFile{Path: name, SHA256: pythonApplicationHash(driver)}
		originals[v.Key] = original
		order[v.Key] = i
		if why := pythonApplicationLargerPrerequisites(r.Application, v.Key); len(why) > 0 {
			r.Application.Larger = append(r.Application.Larger, PythonApplicationLarger{
				Key: v.Key, Bounds: maps.Clone(larger), Original: original, Skipped: why,
				Result: Larger{Bounds: maps.Clone(larger)}})
		} else {
			eligible = append(eligible, v)
		}
	}
	if len(eligible) == 0 {
		return ctx.Err()
	}
	d, err := stage(work, "larger", p, src, "")
	if err != nil {
		return err
	}
	timeouts := pythonApplicationLargerTimeouts
	res, err := runner.CheckWithin(ctx, d, p.ModuleName(), tlc.Config{Specification: p.SpecName(), Constants: larger, Invariants: p.Invariants()}, timeouts.model)
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
	includes, err := tla.Siblings(filepath.Dir(p.ModulePath()), filepath.Base(p.ModulePath()))
	if err != nil {
		return err
	}
	for _, v := range eligible {
		lctx, cancel := context.WithTimeout(ctx, timeouts.explore)
		x, file, err := s.RunLarger(lctx, v.Key, sizes)
		unfinished := lctx.Err() != nil
		if ctx.Err() != nil {
			cancel()
			return ctx.Err()
		}
		var e Exploration
		switch {
		case unfinished:
			// As a driver's count does, an exploration that runs out claims
			// nothing and fails nothing.
			e = Exploration{Unfinished: true, Message: fmt.Sprintf("the driver didn't finish exploring one size larger within %s", timeouts.explore)}
		case x == nil:
			cancel()
			return err
		case !x.Success || !x.Complete || x.Exploration == nil:
			e = Exploration{Message: x.HarnessError}
			if e.Message == "" && err != nil && len(x.Diagnostics) > 0 {
				// The session appends its normalized execution/protocol error
				// after process output. It is not a slice of the traceback.
				e.Message = x.Diagnostics[len(x.Diagnostics)-1]
			}
			if e.Message == "" {
				e.Message = "Python application driver did not complete successfully; see execution diagnostics"
			}
		default:
			e = Exploration{OK: true, States: int64(x.Exploration.States), Depth: x.Exploration.Depth}
		}
		l := compareLarger(larger, &res, &e, "")
		record := PythonApplicationLarger{Key: v.Key, Bounds: maps.Clone(larger), Driver: file, Original: originals[v.Key], Execution: x}
		if e.OK {
			c, checkErr := checkPythonApplicationConformance(lctx, s, x, true, work, fmt.Sprintf("larger-conformance-%d", order[v.Key]), src, includes, larger, res.DistinctStates, runner)
			if checkErr != nil {
				c.OperationalError = true
				c.Result.Message = strings.ReplaceAll(checkErr.Error(), work, "/application")
			}
			record.Conformance = &c
			reason, unreadable := pythonApplicationUnreadableEvidence(c)
			switch {
			case lctx.Err() != nil || (c.OperationalError && !unreadable):
				// A completed count disagreement still stands. Otherwise an
				// unavailable check claims nothing, including no agreement.
				if l.Passed {
					l.Required, l.Passed = false, false
					l.Message = "conformance one size larger was unavailable: " + pythonApplicationConformanceReason(c)
					if lctx.Err() != nil {
						l.Message = fmt.Sprintf("conformance one size larger didn't finish within the shared %s allowance", timeouts.explore)
					}
				}
			case unreadable:
				// TLC ran and couldn't read what the run recorded. That's
				// invalid execution evidence, which fails here as it does at
				// the ratified bounds, and nothing operational.
				c.OperationalError = false
				l.Required, l.Passed = true, false
				l.Message = "conformance one size larger: TLC can't check what the run recorded: " + reason
			case !c.passed():
				l.Required, l.Passed = true, false
				l.Message = pythonApplicationLargerFailure(c, *x, modelVariables(src, includes))
			}
		}
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if l.Message != "" {
			l.Message = fmt.Sprintf("target %s, check %s, invocation %s: %s", v.Key.Target, v.Key.Check, v.Key.ID, l.Message)
		}
		record.Result = *l
		r.Application.Larger = append(r.Application.Larger, record)
	}
	slices.SortFunc(r.Application.Larger, func(a, b PythonApplicationLarger) int { return order[a.Key] - order[b.Key] })
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
		if len(r.Application.Larger[i].Skipped) > 0 {
			continue
		}
		l := r.Application.Larger[i].Result
		if r.Larger == nil || weakness(&l) > weakness(r.Larger) {
			r.Larger = &l
		}
	}
	return nil
}

// TLC's own exit statuses, tlc2.output.EC.ExitStatus in the pinned release.
// It ends with the first three only for a specification it couldn't evaluate
// or parse: a step, an invariant, a module. Its general status is shared: its
// system, memory and command-line errors end with it, as a number it can't
// hold does.
const (
	tlcExitSpecEval     = 75
	tlcExitSafetyEval   = 76
	tlcExitSpecParse    = 150
	tlcExitGeneralError = 255
)

// tlcIntegerTooBig is how TLC's message for a number past its 32 bits starts.
// It exits with its general status, as its system errors do, so only these
// words tell the two apart.
const tlcIntegerTooBig = "TLC can't handle a number this big."

// pythonApplicationUnreadableEvidence says whether a conformance check ended
// because TLC couldn't read what the run recorded, with TLC's reason in one
// line. The model and its constants have just passed TLC at this size, and
// these checking modules at the ratified bounds, so what TLC can't parse or
// evaluate is the recorded states: a number the model's modules can't write,
// a value of another type, a number past its range. The every-step module
// also holds the attempts' arguments and the model at the next environment
// bounds, and TLC ending it this way fails too, as at the ratified bounds. A
// check that never ran or was cut off carries the runner's own error. One
// that stalled, ran out of memory or was killed has the runner's result,
// which holds no TLC version. Neither is evidence, and neither takes anything
// from a check TLC did end this way: as a completed count disagreement does,
// the first such check stands whatever became of the others. No words of
// TLC's make an end operational, since a recorded value can put any words in
// its message.
func pythonApplicationUnreadableEvidence(c PythonApplicationConformance) (reason string, unreadable bool) {
	for _, check := range c.Checks {
		r := check.Result
		if check.Error != "" || r.Version == "" {
			continue
		}
		switch {
		case r.ExitCode == tlcExitSpecParse, r.ExitCode == tlcExitSpecEval, r.ExitCode == tlcExitSafetyEval,
			r.ExitCode == tlcExitGeneralError && strings.HasPrefix(r.Message, tlcIntegerTooBig):
			return pythonApplicationOneLine(r.Message), true
		}
	}
	return "", false
}

// pythonApplicationConformanceReason is why a conformance check didn't pass,
// in one line. An every-step check's reason is its own (#881).
func pythonApplicationConformanceReason(c PythonApplicationConformance) string {
	reason := c.Result.Message
	if reason == "" && c.Result.Tried != nil {
		reason = c.Result.Tried.Message
	}
	if reason = pythonApplicationOneLine(reason); reason == "" {
		return "no reason was recorded"
	}
	return reason
}

// pythonApplicationOneLine is a tool's message on one line, short enough for
// a receipt's row. The check's evidence keeps the whole of it.
func pythonApplicationOneLine(message string) string {
	const limit = 400
	line := strings.Join(strings.Fields(message), " ")
	if len(line) <= limit {
		return line
	}
	cut := strings.LastIndexByte(line[:limit], ' ')
	if cut <= 0 {
		cut = limit
	}
	return strings.ToValidUTF8(line[:cut], "") + " ..."
}

// Next counterexamples name state pairs. Recover the action from the retained
// authenticated attempts, with the same encoding the recording used.
func pythonApplicationLargerFailure(c PythonApplicationConformance, x PythonApplicationExecutionResult, vars []string) string {
	r := c.Result
	message := "conformance one size larger: " + r.Message
	if r.BadStart != "" {
		return message + ": " + r.BadStart
	}
	if bad := r.BadStep; bad != nil {
		// The declared-outcome checker already names the exact action.
		// Another attempt can share its endpoints without being that action.
		for _, check := range c.LogicalChecks {
			if check.Module == "Invariant_StrictAttempts" && check.Outcome == tlc.Deadlock {
				return message
			}
		}
		state := func(row PythonApplicationObservation, values map[string]any) string {
			fields, err := pythonApplicationComponentState(values, row.Form == "module")
			if err != nil {
				return ""
			}
			parts := make([]string, len(vars))
			for i, name := range vars {
				parts[i] = name + " = " + fields[name]
			}
			return strings.Join(parts, ", ")
		}
		for _, row := range x.Observations {
			if row.Kind != "transition" || row.Replay == nil || *row.Replay || state(row, row.Before) != bad.From || state(row, row.After) != bad.To {
				continue
			}
			action := row.Step
			var args []string
			for _, arg := range row.Args {
				value, _ := conformance.Encode(arg)
				args = append(args, value)
			}
			if len(args) > 0 {
				action += "(" + strings.Join(args, ", ") + ")"
			}
			return fmt.Sprintf("%s; %s from %s to %s", message, action, bad.From, bad.To)
		}
		return fmt.Sprintf("%s; from %s to %s", message, bad.From, bad.To)
	}
	if tried := r.Tried; tried != nil && !tried.Passed {
		return fmt.Sprintf("conformance one size larger: %s; %s in %s", tried.Message, tried.Untried, tried.In)
	}
	return message
}

// A larger exploration depends on its own completed execution and all three
// conformance obligations at the ratified bounds, never a sibling's verdict.
func pythonApplicationLargerPrerequisites(a *PythonApplicationReport, key PythonApplicationInvocationKey) []string {
	var out []string
	x := slices.IndexFunc(a.Executions, func(x PythonApplicationExecutionResult) bool { return x.Key == key })
	if x < 0 || !a.Executions[x].Success || !a.Executions[x].Complete || a.Executions[x].Exploration == nil {
		out = append(out, "base exploration execution did not complete successfully")
	}
	i := slices.IndexFunc(a.Conformance, func(c PythonApplicationConformance) bool { return c.Key == key })
	if i < 0 {
		return append(out, "base exploration has no transition, exhaustive-state or attempted-step conformance")
	}
	c := a.Conformance[i]
	if !c.Result.Passed || c.OperationalError {
		out = append(out, "base exploration transition conformance did not pass")
	}
	if c.Mode != "bounded-exhaustive" || !c.Result.Exhaustive || c.Result.ModelStates <= 0 || int64(c.Result.States) != c.Result.ModelStates {
		out = append(out, "base exploration exhaustive-state conformance did not pass")
	}
	if c.Result.Tried == nil || !c.Result.Tried.Passed {
		out = append(out, "base exploration attempted-step conformance did not pass")
	}
	return out
}

// Actual completed windows and original logical obligations are separate
// evidence. The scheduler uses one worker here to retain the deterministic
// invocation prefix on failure, including through a recovery tree.
type pythonApplicationModelRunner struct {
	mu           sync.Mutex
	runner       conformance.Runner
	root         string
	results      []PythonApplicationModelCheck
	logical      []PythonApplicationLogicalCheck
	logicalIndex map[string]int
}

func (r *pythonApplicationModelRunner) Check(ctx context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
	c := tlc.ConformanceCheck{Batch: filepath.Base(dir), Recovery: &tlc.ConformanceRecovery{}}
	result, err := r.CheckConformance(ctx, dir, module, cfg, c)
	r.CompleteConformance(c, result, err)
	return result, err
}

func (r *pythonApplicationModelRunner) ConformanceConcurrency() int { return 1 }

func (r *pythonApplicationModelRunner) CheckConformance(ctx context.Context, dir, module string, cfg tlc.Config, c tlc.ConformanceCheck) (tlc.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return tlc.Result{}, err
	}
	inputHash, err := pythonApplicationModelHash(ctx, filepath.Join(dir, module+".tla"))
	if c.ParentAttempt == 0 {
		if r.logicalIndex == nil {
			r.logicalIndex = map[string]int{}
		}
		r.logicalIndex[c.Batch] = len(r.logical)
		r.logical = append(r.logical, PythonApplicationLogicalCheck{Module: module, Batch: c.Batch, InputSHA256: inputHash, Config: cfg})
	}
	if err != nil {
		return tlc.Result{}, err
	}
	var result tlc.Result
	if capable, ok := r.runner.(tlc.ConformanceRunner); ok && c.Range.Kind != "" {
		result, err = capable.CheckConformance(ctx, dir, module, cfg, c)
	} else {
		result, err = r.runner.Check(ctx, dir, module, cfg)
	}
	var exhausted *tlc.MemoryExhaustion
	if errors.As(err, &exhausted) {
		return result, err
	}
	record := PythonApplicationModelCheck{Module: module, Batch: c.Batch, InputSHA256: inputHash, Config: cfg, Result: result}
	// TLC's unstructured progress includes temporary extraction paths. Retain
	// its parsed verdict, diagnostics and counterexample, not progress output.
	record.Result.Printed = nil
	if err != nil {
		record.Error = strings.ReplaceAll(err.Error(), r.root, "/model")
	}
	r.results = append(r.results, record)
	return result, err
}

func (r *pythonApplicationModelRunner) CompleteConformance(c tlc.ConformanceCheck, result tlc.Result, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i, ok := r.logicalIndex[c.Batch]; ok {
		l := &r.logical[i]
		l.Outcome = result.Outcome
		if result.Outcome != tlc.Passed {
			l.Invariant, l.Property, l.Message = result.Invariant, result.Property, result.Message
			l.Trace, l.Loop, l.Stutters = result.Trace, result.Loop, result.Stutters
		}
		if err != nil {
			l.Outcome = tlc.Failed
			l.Error = strings.ReplaceAll(err.Error(), r.root, "/model")
		}
		delete(r.logicalIndex, c.Batch)
	}
	if capable, ok := r.runner.(tlc.ConformanceRunner); ok {
		capable.CompleteConformance(c, result, err)
	}
}

// A tried-state window can contain arbitrarily many action instances. Hash
// its generated module without retaining those recording contents in memory.
func pythonApplicationModelHash(ctx context.Context, path string) (sum string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	h := sha256.New()
	var buffer [32 << 10]byte
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buffer[:])
		h.Write(buffer[:n])
		if err == io.EOF {
			return "sha256:" + hex.EncodeToString(h.Sum(nil)), ctx.Err()
		}
		if err != nil {
			return "", err
		}
	}
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
