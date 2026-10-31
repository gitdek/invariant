package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// A trusted contract's backing tests run in the application's own session
// (#294): each of the contract's declared invocations, keyed by its contract
// declaration, offline in the admitted dependency image like every other test.
// Before its tests load, the runner resolves the contract's operation to its
// real implementation, then counts each test's calls into it and records the
// test's unittest assertions. The gate attributes what ran as backing-test
// results, and passes only when every declared contract is tested.

var pythonApplicationPythonVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// boundary validates the runner's account of the implementation it resolved.
// An application declaration must be found in its staged source as the
// session holds it; a distribution's must carry its installed version.
func (s *PythonApplicationSession) boundary(c PythonApplicationCommand, spec *pythonApplicationContractRequest, line []byte) (*PythonApplicationBoundary, error) {
	fields, err := pythonApplicationJSON(line, "type", "contract", "kind", "resolved", "callable", "file", "distribution", "version", "recorded", "python", "diagnostics")
	if err != nil {
		return nil, err
	}
	if _, err := pythonApplicationJSON(fields["file"], "path", "sha256"); err != nil {
		return nil, err
	}
	var b PythonApplicationBoundary
	if err := json.Unmarshal(line, &b); err != nil {
		return nil, err
	}
	f := b.File
	switch {
	case b.Contract != spec.Contract || b.Kind != spec.Kind:
		return nil, fmt.Errorf("foreign contract boundary record")
	case b.Callable != "" && b.Callable != "python" && b.Callable != "c", b.Resolved == (b.Callable == ""):
		return nil, fmt.Errorf("contradictory contract boundary record")
	case (f.Path == "") != (f.SHA256 == ""), f.SHA256 != "" && !pythonApplicationDigest.MatchString(f.SHA256),
		f.Path != "" && (path.Clean(f.Path) != f.Path || path.IsAbs(f.Path) || f.Path == ".." || strings.HasPrefix(f.Path, "../")):
		return nil, fmt.Errorf("invalid contract boundary file")
	case !b.Resolved && (f.Path != "" || b.Distribution != "" || b.Version != "" || b.Recorded):
		return nil, fmt.Errorf("an unresolved contract boundary claims an implementation")
	case b.Python != "" && !pythonApplicationPythonVersion.MatchString(b.Python):
		return nil, fmt.Errorf("invalid contract boundary interpreter version")
	}
	switch spec.Kind {
	case project.OperationApplication:
		if b.Distribution != "" || b.Version != "" || b.Recorded ||
			b.Resolved && (b.Callable != "python" || spec.Declaration == nil || f.Path != spec.Declaration.Source || s.snapshot[f.Path].hash != f.SHA256) {
			return nil, fmt.Errorf("foreign application contract boundary")
		}
	case project.OperationStandardLibrary:
		if b.Distribution != "" || b.Version != "" || b.Recorded {
			return nil, fmt.Errorf("a standard-library contract boundary names a distribution")
		}
	case project.OperationDependency:
		if b.Resolved && (b.Distribution != spec.Distribution || strings.TrimSpace(b.Version) == "" || f.Path == "") {
			return nil, fmt.Errorf("foreign dependency contract boundary")
		}
	default:
		return nil, fmt.Errorf("unknown contract boundary kind")
	}
	for i, diagnostic := range b.Diagnostics {
		b.Diagnostics[i] = strings.NewReplacer(c.Root, "/src", c.RuntimeDir, "/runtime", c.OutputDir, "/output").Replace(diagnostic)
	}
	if b.Diagnostics == nil {
		b.Diagnostics = []string{}
	}
	return &b, nil
}

// pythonApplicationBoundaryTest validates a backing test's calls into the
// resolved implementation and its assertion records.
func pythonApplicationBoundaryTest(line []byte, b *PythonApplicationBoundary, row *PythonApplicationTest) error {
	var raw struct {
		Calls      json.Number       `json:"boundary_calls"`
		Assertions []json.RawMessage `json:"assertions"`
	}
	d := json.NewDecoder(bytes.NewReader(line))
	d.UseNumber()
	if err := d.Decode(&raw); err != nil {
		return err
	}
	calls, err := raw.Calls.Int64()
	if err != nil || calls < 0 || row.BoundaryCalls == nil || calls != *row.BoundaryCalls || raw.Assertions == nil {
		return fmt.Errorf("invalid backing test calls or assertions")
	}
	if calls > 0 && !b.Resolved {
		return fmt.Errorf("calls reached an implementation that didn't resolve")
	}
	seen := map[string]bool{}
	for i, assertion := range raw.Assertions {
		if _, err := pythonApplicationJSON(assertion, "id", "outcome"); err != nil {
			return err
		}
		a := row.Assertions[i]
		if strings.TrimSpace(a.ID) == "" || seen[a.ID] || a.Outcome != "passed" && a.Outcome != "failed" && a.Outcome != "error" {
			return fmt.Errorf("invalid or duplicate backing test assertion")
		}
		seen[a.ID] = true
	}
	if row.Assertions == nil {
		row.Assertions = []PythonApplicationAssertion{}
	}
	return nil
}

// pythonApplicationContractTests turns each backing-test execution into the
// result the evidence model qualifies: the run's own tests, calls and
// assertions, the implementation it resolved, and the identity the session
// bound, nothing an invocation merely intended.
func (r *Report) pythonApplicationContractTests(inv *project.ComponentInventory, declarations map[string]project.Component) ([]ComponentContractTestResult, error) {
	contracts := pythonApplicationContracts(inv)
	index, err := indexComponentInventory(copyComponentInventory(*inv))
	if err != nil {
		return nil, err
	}
	bindings := index.contractBindings(r.Toolchain)
	var out []ComponentContractTestResult
	for _, x := range r.Application.Executions {
		c, ok := contracts[x.Key.Target]
		if !ok {
			continue
		}
		v := x.Declared
		outcome := "failed"
		if x.Success {
			outcome = "passed"
		}
		result := ComponentContractTestResult{Contract: c.ID, Check: x.Key.Check, Invocation: &v,
			Runner:  &ComponentProofTool{Name: "Python unittest", Version: r.Toolchain.PythonImage, Pins: map[string]string{"runner_sha256": x.Provenance.RunnerSHA256, "observer_sha256": x.Provenance.ObserverSHA256}},
			Binding: copyContractBinding(bindings[c.ID].binding),
			Inputs:  []project.Component{declarations[c.Contract], declarations[x.Key.Check]}, FileInputs: slices.Clone(x.Provenance.Files),
			Outcome: outcome, Diagnostics: slices.Clone(x.Diagnostics)}
		if c.Operation.Kind == project.OperationApplication {
			result.Inputs = append(result.Inputs, declarations[c.Operation.Declaration])
		}
		test := declarations[x.Key.Check]
		source := project.ComponentFile{Path: test.Source, SHA256: test.SourceSHA256}
		var runs []PythonApplicationTest
		for _, t := range x.Tests {
			if t.Check == x.Key.Check {
				runs = append(runs, t)
			}
		}
		// A backing test supports its contract from one run of it. One that
		// ran twice in the same invocation, through a subclass say, fails
		// its evidence rather than the attribution.
		if len(runs) > 1 {
			why := fmt.Sprintf("the backing test ran %d times in one invocation", len(runs))
			result.Tests = []ComponentRuntimeTestResult{{Check: x.Key.Check, Outcome: "failed", Diagnostics: []string{why}}}
			result.Exercise = []ComponentContractExercise{{Check: x.Key.Check}}
			result.Diagnostics = append(result.Diagnostics, why)
			runs = nil
		}
		for _, t := range runs {
			row := ComponentRuntimeTestResult{Check: t.Check, Exercised: t.Exercised, Outcome: "failed", Diagnostics: slices.Clone(t.Diagnostics)}
			switch t.Outcome {
			case "passed":
				row.Outcome = "passed"
			case "skipped":
				row.Outcome = "skipped"
			case "not_run":
				row.Outcome = "empty"
			}
			for _, a := range t.Assertions {
				s := source
				assertion := ComponentRuntimeAssertionResult{ID: a.ID, Source: &s, Exercised: true, Outcome: "failed"}
				if a.Outcome == "passed" {
					assertion.Outcome = "passed"
				}
				row.Assertions = append(row.Assertions, assertion)
			}
			result.Tests = append(result.Tests, row)
			result.Exercise = append(result.Exercise, ComponentContractExercise{Check: t.Check, Calls: t.Calls()})
		}
		if b := x.Boundary; b != nil {
			result.Diagnostics = append(result.Diagnostics, b.Diagnostics...)
			if b.Resolved {
				result.Implementation = &ComponentContractImplementation{Kind: b.Kind, Callable: b.Callable,
					Distribution: b.Distribution, Version: b.Version, Recorded: b.Recorded, Python: b.Python}
				if b.File.Path != "" {
					file := b.File
					result.Implementation.File = &file
				}
			}
		}
		out = append(out, result)
	}
	return out, nil
}

// pythonApplicationContractsTested says whether every declared trusted
// contract has tested support, which the gate requires of each, used by a
// proof or not.
func (r *Report) pythonApplicationContractsTested() bool {
	if r.Components == nil {
		return true
	}
	for _, c := range r.Components.Contracts {
		if c.Support != "tested" {
			return false
		}
	}
	return true
}

// pythonApplicationContractSteps marks each backing-test run's build step, at
// (step, execution) indices, as passing only where its attributed row is
// tested, so a stub-only run fails its step as it fails its contract, on
// both routes. It says whether every such step passed.
func (r *Report) pythonApplicationContractSteps(steps [][2]int) bool {
	all := true
	for _, at := range steps {
		x := r.Application.Executions[at[1]]
		tested := false
		if r.Components != nil {
			for _, row := range r.Components.ContractTests {
				if row.Check == x.Key.Check && row.Result.Invocation != nil && row.Result.Invocation.ID == x.Key.ID &&
					slices.ContainsFunc(r.Components.Inventory.TrustedContracts, func(c project.ResolvedComponentContract) bool {
						return c.ID == row.Contract && c.Contract == x.Key.Target
					}) {
					tested = row.Status == "tested"
				}
			}
		}
		r.Build.Steps[at[0]].Passed = x.Success && tested
		all = all && r.Build.Steps[at[0]].Passed
	}
	return all
}

// pythonApplicationContractRows attributes a session's backing-test runs on
// a detached report, for the direct route, as the gate attributes them.
func pythonApplicationContractRows(inv *project.ComponentInventory, deps PythonApplicationProvenance, executions []PythonApplicationExecutionResult) (*Report, error) {
	r := &Report{Application: &PythonApplicationReport{Executions: executions},
		Toolchain: Toolchain{PythonImage: toolchain.PythonImage, PythonDependencies: deps.Dependencies}}
	declarations := map[string]project.Component{}
	for _, c := range append(slices.Clone(inv.Components), inv.Provenance.Declarations...) {
		declarations[c.ID] = c
	}
	tests, err := r.pythonApplicationContractTests(inv, declarations)
	if err != nil {
		return nil, err
	}
	return r, r.AttributeComponentContractTests(inv, tests)
}

// pythonApplicationContractStep names a backing-test invocation's build step
// apart from a check's, which can share its invocation ID.
func pythonApplicationContractStep(contracts map[string]project.ResolvedComponentContract, key PythonApplicationInvocationKey) (string, bool) {
	c, ok := contracts[key.Target]
	if !ok {
		return key.ID, false
	}
	return "trusted contract " + c.ID + " · " + key.ID, true
}

func pythonApplicationContracts(inv *project.ComponentInventory) map[string]project.ResolvedComponentContract {
	out := map[string]project.ResolvedComponentContract{}
	if inv != nil {
		for _, c := range inv.TrustedContracts {
			out[c.Contract] = c
		}
	}
	return out
}
