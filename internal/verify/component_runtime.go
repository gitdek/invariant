package verify

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/project"
)

// ComponentRuntimeResult records supplied outcomes for an exact selected test
// invocation. Tests and assertions describe actual reported exercise, never
// inferred coverage. Attribution preserves incomplete and unsuccessful results
// and neither runs tools nor discovers omitted inputs, tests or assertions.
type ComponentRuntimeResult struct {
	Target      string                               `json:"target"`
	Check       string                               `json:"check"`
	Invocation  *project.ResolvedComponentInvocation `json:"invocation,omitempty"`
	Runner      *ComponentProofTool                  `json:"runner,omitempty"`
	Inputs      []project.Component                  `json:"inputs"`
	FileInputs  []project.ComponentFile              `json:"file_inputs"`
	Model       *ComponentConformanceModel           `json:"model,omitempty"`
	Bounds      *ComponentConformanceBounds          `json:"bounds,omitempty"`
	Outcome     string                               `json:"outcome"`
	Tests       []ComponentRuntimeTestResult         `json:"tests,omitempty"`
	Diagnostics []string                             `json:"diagnostics,omitempty"`
}

// ComponentRuntimeTestResult records one selected test's actual exercise and
// outcome independently of the process outcome and of its supplied assertions.
type ComponentRuntimeTestResult struct {
	Check       string                            `json:"check"`
	Exercised   bool                              `json:"exercised"`
	Outcome     string                            `json:"outcome"`
	Assertions  []ComponentRuntimeAssertionResult `json:"assertions,omitempty"`
	Diagnostics []string                          `json:"diagnostics,omitempty"`
}

// ComponentRuntimeAssertionResult identifies an assertion within its test and
// invocation. ID is opaque, not a discovered component identity. A missing
// actual source leaves the assertion unchecked even when its outcome passed.
type ComponentRuntimeAssertionResult struct {
	ID          string                 `json:"id"`
	Source      *project.ComponentFile `json:"source,omitempty"`
	Exercised   bool                   `json:"exercised"`
	Outcome     string                 `json:"outcome"`
	Diagnostics []string               `json:"diagnostics,omitempty"`
}

// ComponentRuntimeEvidence qualifies only supplied tests and assertions. A
// tested row grants no target code coverage, proof or conformance claim.
type ComponentRuntimeEvidence struct {
	Origin string                         `json:"origin,omitempty"`
	Target string                         `json:"target"`
	Check  string                         `json:"check"`
	Status string                         `json:"status"`
	Tests  []ComponentRuntimeTestEvidence `json:"tests,omitempty"`
	Result ComponentRuntimeResult         `json:"result"`
}

type ComponentRuntimeTestEvidence struct {
	Check      string                              `json:"check"`
	Status     string                              `json:"status"`
	Assertions []ComponentRuntimeAssertionEvidence `json:"assertions,omitempty"`
}

type ComponentRuntimeAssertionEvidence struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// AttributeComponentRuntime replaces or clears only supplied runtime evidence.
// It validates a detached inventory, incoming results and every retained kind
// before committing once. Missing actual provenance leaves records unchecked;
// mismatched references reject the update without changing the report. It runs
// no tools and changes no gate outcome, legacy check or ratified data.
func (r *Report) AttributeComponentRuntime(inv *project.ComponentInventory, results []ComponentRuntimeResult) error {
	evidence, index, err := r.componentReplacement(inv)
	if err != nil {
		return err
	}
	if evidence.Runtime, err = index.runtime(results, r.Bounds); err != nil {
		return err
	}
	if evidence.Proofs, err = index.retainedProofs(evidence.Inventory, r.Components); err != nil {
		return err
	}
	if evidence.Assumptions, err = index.retainedAssumptions(r.Components); err != nil {
		return err
	}
	if evidence.Conformance, err = index.retainedConformance(r.Components, r.Bounds); err != nil {
		return err
	}
	if evidence.ContractTests, err = index.retainedContractTests(r.Components); err != nil {
		return err
	}
	r.commitComponentEvidence(index, evidence)
	return nil
}

func (index *componentIndex) runtime(results []ComponentRuntimeResult, bounds map[string]string) ([]ComponentRuntimeEvidence, error) {
	var rows []ComponentRuntimeEvidence
	seen := map[[3]string]bool{}
	for _, supplied := range results {
		result := copyComponentRuntimeResult(supplied)
		check, ok := index.checks[result.Target]
		if !ok || !slices.Contains(check.Tests, result.Check) {
			return nil, fmt.Errorf("component runtime: target %q has no selected test %q", result.Target, result.Check)
		}
		v := result.Invocation
		if v == nil || v.Kind != "test" || v.Check != result.Check || !slices.ContainsFunc(check.Invocations, func(selected project.ResolvedComponentInvocation) bool {
			return sameComponentInvocation(*v, selected)
		}) {
			return nil, fmt.Errorf("component runtime: invocation does not match a selected test for %q", result.Target)
		}
		key := [3]string{result.Target, result.Check, v.ID}
		if seen[key] {
			return nil, fmt.Errorf("component runtime: duplicate result for %q, %q, %q", key[0], key[1], key[2])
		}
		seen[key] = true
		inputs := map[string]bool{}
		for _, input := range result.Inputs {
			c, ok := index.declarations[input.ID]
			if !ok || input != c || inputs[input.ID] {
				return nil, fmt.Errorf("component runtime: invalid, stale or duplicate input %q", input.ID)
			}
			inputs[input.ID] = true
		}
		if err := componentFileReferences(index.files, result.FileInputs); err != nil {
			return nil, err
		}
		declared, err := componentFileSet(v.Inputs)
		if err != nil {
			return nil, err
		}
		if result.Model != nil {
			if result.Model.Module != "" {
				if _, ok := declared[result.Model.Module]; !ok {
					return nil, fmt.Errorf("component runtime: model module %q is not a declared invocation input", result.Model.Module)
				}
			}
			if err := componentFileReferences(declared, result.Model.Inputs); err != nil {
				return nil, err
			}
		}
		if result.Bounds != nil {
			if !maps.Equal(result.Bounds.Values, bounds) {
				return nil, fmt.Errorf("component runtime: supplied bounds do not match report bounds for %q", result.Target)
			}
			if err := componentFileReferences(declared, result.Bounds.Sources); err != nil {
				return nil, err
			}
		}
		if !componentConformanceOutcome(result.Outcome) {
			return nil, fmt.Errorf("component runtime: invalid outcome for %q", result.Target)
		}
		tests := map[string]bool{}
		for _, test := range result.Tests {
			if !slices.Contains(check.Tests, test.Check) || tests[test.Check] || !componentConformanceOutcome(test.Outcome) {
				return nil, fmt.Errorf("component runtime: unselected, duplicate or invalid test %q for %q", test.Check, result.Target)
			}
			tests[test.Check] = true
			assertions := map[string]bool{}
			for _, assertion := range test.Assertions {
				if strings.TrimSpace(assertion.ID) == "" || assertions[assertion.ID] || !componentConformanceOutcome(assertion.Outcome) {
					return nil, fmt.Errorf("component runtime: invalid or duplicate assertion %q for %q", assertion.ID, test.Check)
				}
				assertions[assertion.ID] = true
				if assertion.Source != nil {
					if err := componentFileReferences(index.files, []project.ComponentFile{*assertion.Source}); err != nil {
						return nil, err
					}
				}
			}
		}
		rows = append(rows, result.qualifyRuntime(check))
	}
	return copyComponentRuntime(rows), nil
}

func (r *ComponentRuntimeResult) qualifyRuntime(check project.ResolvedComponentCheck) ComponentRuntimeEvidence {
	inputs := map[string]project.Component{}
	for _, input := range r.Inputs {
		inputs[input.ID] = input
	}
	files := map[string]bool{}
	for _, file := range r.FileInputs {
		files[file.Path] = true
	}
	declarationPresent := func(id string) bool {
		input, ok := inputs[id]
		return ok && files[input.Source]
	}
	filesPresent := func(snapshots []project.ComponentFile) bool {
		for _, file := range snapshots {
			if !files[file.Path] {
				return false
			}
		}
		return true
	}
	ready := r.Runner != nil && strings.TrimSpace(r.Runner.Name) != "" && strings.TrimSpace(r.Runner.Version) != "" &&
		declarationPresent(r.Target) && filesPresent(r.Invocation.Inputs)
	if check.Proof != nil {
		for _, id := range check.Proof.Contracts {
			ready = ready && declarationPresent(id)
		}
	}
	if r.Model != nil {
		ready = ready && r.Model.Module != "" && slices.ContainsFunc(r.Model.Inputs, func(file project.ComponentFile) bool {
			return file.Path == r.Model.Module
		}) && filesPresent(r.Model.Inputs)
	}
	if r.Bounds != nil {
		ready = ready && len(r.Bounds.Sources) > 0 && filesPresent(r.Bounds.Sources)
	}
	row := ComponentRuntimeEvidence{Origin: "supplied", Target: r.Target, Check: r.Check, Status: "unchecked", Result: *r}
	for _, test := range r.Tests {
		testReady := ready && test.Exercised && declarationPresent(test.Check)
		evidence := ComponentRuntimeTestEvidence{Check: test.Check, Status: "unchecked"}
		if testReady && test.Outcome == "passed" {
			evidence.Status = "tested"
			if r.Outcome == "passed" && test.Check == r.Check {
				row.Status = "tested"
			}
		}
		for _, assertion := range test.Assertions {
			item := ComponentRuntimeAssertionEvidence{ID: assertion.ID, Status: "unchecked"}
			// A passing assertion survives a failed parent outcome, but requires
			// its own exercise and source and an actually exercised parent test.
			if testReady && assertion.Exercised && assertion.Outcome == "passed" && assertion.Source != nil && files[assertion.Source.Path] {
				item.Status = "passed"
			}
			evidence.Assertions = append(evidence.Assertions, item)
		}
		row.Tests = append(row.Tests, evidence)
	}
	return row
}

func (index *componentIndex) retainedRuntime(old *ComponentEvidence, bounds map[string]string) ([]ComponentRuntimeEvidence, error) {
	if old == nil {
		return nil, nil
	}
	prior := copyComponentEvidence(*old)
	var results []ComponentRuntimeResult
	for _, row := range prior.Runtime {
		if err := index.retainRuntime(prior.Inventory, row); err != nil {
			return nil, err
		}
		results = append(results, row.Result)
	}
	rows, err := index.runtime(results, bounds)
	if err != nil {
		return nil, err
	}
	for i, row := range rows {
		was := prior.Runtime[i]
		if was.Origin != "" && was.Origin != "gate" && was.Origin != "supplied" {
			return nil, fmt.Errorf("invalid runtime origin")
		}
		rows[i].Origin = was.Origin
		if row.Target != was.Target || row.Check != was.Check || row.Status != was.Status ||
			!slices.EqualFunc(row.Tests, was.Tests, func(a, b ComponentRuntimeTestEvidence) bool {
				return a.Check == b.Check && a.Status == b.Status && slices.Equal(a.Assertions, b.Assertions)
			}) {
			return nil, fmt.Errorf("component runtime: retained qualification changed for %q/%q", was.Target, was.Check)
		}
	}
	return rows, nil
}

func (index *componentIndex) retainRuntime(old project.ComponentInventory, row ComponentRuntimeEvidence) error {
	check, ok := index.checks[row.Target]
	prior := slices.IndexFunc(old.Checks, func(c project.ResolvedComponentCheck) bool { return c.Target == row.Target })
	if row.Target != row.Result.Target || row.Check != row.Result.Check || !ok || prior < 0 ||
		(check.Proof == nil) != (old.Checks[prior].Proof == nil) {
		return fmt.Errorf("component runtime: retained selection changed for %q", row.Target)
	}
	ids := []string{row.Target, row.Check}
	for _, test := range row.Result.Tests {
		ids = append(ids, test.Check)
	}
	if check.Proof != nil {
		if !slices.Equal(check.Proof.Contracts, old.Checks[prior].Proof.Contracts) {
			return fmt.Errorf("component runtime: retained contracts changed for %q", row.Target)
		}
		ids = append(ids, check.Proof.Contracts...)
	}
	// Incomplete results still depend on the original target, selected contracts
	// and tests. Their invocation and every actual snapshot are replayed above.
	declarations := slices.Clone(old.Components)
	if old.Provenance != nil {
		declarations = append(declarations, old.Provenance.Declarations...)
	}
	for _, id := range ids {
		pos := slices.IndexFunc(declarations, func(c project.Component) bool { return c.ID == id })
		c, ok := index.declarations[id]
		if pos < 0 || !ok || declarations[pos] != c {
			return fmt.Errorf("component runtime: retained declaration %q changed", id)
		}
	}
	return nil
}

func copyComponentRuntimeResult(result ComponentRuntimeResult) ComponentRuntimeResult {
	result.Inputs = slices.Clone(result.Inputs)
	sort.Slice(result.Inputs, func(i, j int) bool { return result.Inputs[i].ID < result.Inputs[j].ID })
	result.FileInputs = copyComponentFiles(result.FileInputs)
	if result.Invocation != nil {
		invocation := copyComponentInvocation(*result.Invocation)
		result.Invocation = &invocation
	}
	if result.Runner != nil {
		runner := *result.Runner
		runner.Pins = maps.Clone(runner.Pins)
		result.Runner = &runner
	}
	if result.Model != nil {
		model := *result.Model
		model.Inputs = copyComponentFiles(model.Inputs)
		result.Model = &model
	}
	if result.Bounds != nil {
		bounds := *result.Bounds
		bounds.Values = maps.Clone(bounds.Values)
		bounds.Sources = copyComponentFiles(bounds.Sources)
		result.Bounds = &bounds
	}
	result.Tests = copyComponentRuntimeTests(result.Tests)
	result.Diagnostics = slices.Clone(result.Diagnostics)
	return result
}

func copyComponentRuntimeTests(tests []ComponentRuntimeTestResult) []ComponentRuntimeTestResult {
	tests = slices.Clone(tests)
	for i := range tests {
		test := &tests[i]
		test.Assertions = slices.Clone(test.Assertions)
		for j := range test.Assertions {
			assertion := &test.Assertions[j]
			if assertion.Source != nil {
				source := *assertion.Source
				assertion.Source = &source
			}
			assertion.Diagnostics = slices.Clone(assertion.Diagnostics)
		}
		sort.Slice(test.Assertions, func(i, j int) bool { return test.Assertions[i].ID < test.Assertions[j].ID })
		test.Diagnostics = slices.Clone(test.Diagnostics)
	}
	sort.Slice(tests, func(i, j int) bool { return tests[i].Check < tests[j].Check })
	return tests
}

func copyComponentRuntime(rows []ComponentRuntimeEvidence) []ComponentRuntimeEvidence {
	rows = slices.Clone(rows)
	for i := range rows {
		rows[i].Result = copyComponentRuntimeResult(rows[i].Result)
		rows[i].Tests = slices.Clone(rows[i].Tests)
		for j := range rows[i].Tests {
			test := &rows[i].Tests[j]
			test.Assertions = slices.Clone(test.Assertions)
			sort.Slice(test.Assertions, func(i, j int) bool { return test.Assertions[i].ID < test.Assertions[j].ID })
		}
		sort.Slice(rows[i].Tests, func(a, b int) bool { return rows[i].Tests[a].Check < rows[i].Tests[b].Check })
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		var aid, bid string
		if a.Result.Invocation != nil {
			aid = a.Result.Invocation.ID
		}
		if b.Result.Invocation != nil {
			bid = b.Result.Invocation.ID
		}
		return aid < bid
	})
	return rows
}
