package verify

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// ErrStaleComponentEvidence marks an attribution rejected because supplied or
// retained evidence names a declaration, file or operation identity the report
// no longer holds. The report is left as it was.
var ErrStaleComponentEvidence = errors.New("stale component evidence")

// ComponentContractBinding is the identity a trusted contract's operation has
// in a report: the pinned Python runtime it runs on, by digest, and for an
// application operation its declaration's snapshot, or for a dependency the
// resolved wheel by name, version, filename and hash. A package name or a
// mutable image tag alone never binds one.
type ComponentContractBinding struct {
	Kind        string                    `json:"kind"`
	Runtime     string                    `json:"runtime"`
	Declaration *project.Component        `json:"declaration,omitempty"`
	Wheel       *toolchain.PythonArtifact `json:"wheel,omitempty"`
}

// ComponentContractTestResult records a supplied run of a trusted contract's
// backing tests through one of its declared invocations, with the snapshots
// and the operation identity they ran against (#294). Attribution runs
// nothing: an executor supplies these from actual runs, and a passing process
// alone supports no contract.
type ComponentContractTestResult struct {
	Contract    string                               `json:"contract"`
	Check       string                               `json:"check"`
	Invocation  *project.ResolvedComponentInvocation `json:"invocation,omitempty"`
	Runner      *ComponentProofTool                  `json:"runner,omitempty"`
	Binding     *ComponentContractBinding            `json:"binding,omitempty"`
	Inputs      []project.Component                  `json:"inputs"`
	FileInputs  []project.ComponentFile              `json:"file_inputs"`
	Outcome     string                               `json:"outcome"`
	Tests       []ComponentRuntimeTestResult         `json:"tests,omitempty"`
	Diagnostics []string                             `json:"diagnostics,omitempty"`
}

// ComponentContractTestEvidence qualifies one supplied backing-test result.
// A test supports its contract only when it was exercised and passed in a
// process that passed, with the contract's snapshots and identity present.
type ComponentContractTestEvidence struct {
	Origin   string                         `json:"origin,omitempty"`
	Contract string                         `json:"contract"`
	Check    string                         `json:"check"`
	Status   string                         `json:"status"`
	Tests    []ComponentRuntimeTestEvidence `json:"tests,omitempty"`
	Result   ComponentContractTestResult    `json:"result"`
}

// ComponentContractUse is one trusted contract a proof attempt's verifier
// inputs actually assumed, with the contract declaration they held, as the
// prover's adapter reports it.
type ComponentContractUse struct {
	Contract    string            `json:"contract"`
	Declaration project.Component `json:"declaration"`
}

// ComponentUnaccounted is a boundary a proof attempt reached with no declared
// trusted contract (missing_contract), or an assumption its verifier made that
// no declaration accounts for (unaccounted_assumption), in the adapter's words.
type ComponentUnaccounted struct {
	Kind      string `json:"kind"`
	Operation string `json:"operation"`
	Detail    string `json:"detail,omitempty"`
}

// Unaccounted kinds.
const (
	MissingContract       = "missing_contract"
	UnaccountedAssumption = "unaccounted_assumption"
)

// ComponentContractEvidence is a declared trusted contract's standing, derived
// on every attribution: the identity its operation has in this report,
// whether every backing test supports it, and which proof attempts reported
// assuming it. A trusted contract is never proved, and one that no proof used
// stays a declaration.
type ComponentContractEvidence struct {
	Contract string                    `json:"contract"`
	Binding  *ComponentContractBinding `json:"binding,omitempty"`
	Support  string                    `json:"support"`
	Use      string                    `json:"use"`
	Relying  []string                  `json:"relying,omitempty"`
}

// ComponentContractDiagnostic is one problem at a trusted boundary, in the
// same shape whichever adapter found it: an operation the report can't bind
// to a pinned runtime or one resolved wheel, a proof's assumption whose
// contract lacks tested support, and a proof's missing contract or
// unaccounted assumption. Stale evidence never gets this far: attribution
// rejects it with ErrStaleComponentEvidence.
type ComponentContractDiagnostic struct {
	Kind     string `json:"kind"`
	Contract string `json:"contract,omitempty"`
	Target   string `json:"target,omitempty"`
	Detail   string `json:"detail"`
}

// Diagnostic kinds beyond the unaccounted ones.
const (
	UnresolvedOperation = "unresolved_operation"
	UntestedAssumption  = "untested_assumption"
)

// AttributeComponentContractTests replaces or clears only supplied backing-test
// results for trusted contracts, revalidating every retained kind against inv,
// the report's bounds and its recorded Python runtime and dependencies before
// committing once. It runs no tools and changes no gate outcome. A result
// naming a declaration, file or operation identity the report doesn't hold
// is stale, and rejects the whole batch.
func (r *Report) AttributeComponentContractTests(inv *project.ComponentInventory, results []ComponentContractTestResult) error {
	evidence, index, err := r.componentReplacement(inv)
	if err != nil {
		return err
	}
	if evidence.ContractTests, err = index.contractTests(results); err != nil {
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
	if evidence.Runtime, err = index.retainedRuntime(r.Components, r.Bounds); err != nil {
		return err
	}
	r.commitComponentEvidence(index, evidence)
	return nil
}

// indexTrustedContracts validates a detached inventory's trusted contracts as
// the resolver would have: canonical IDs, operations and selections, each
// contract's declaration a Python abstraction with an inline contract that
// isn't production code in scope, and no operation or declaration bound twice.
func (index *componentIndex) indexTrustedContracts(inv project.ComponentInventory, provenance map[string]project.Component, files map[string]project.ComponentFile) error {
	operations, declarations := map[string]bool{}, map[string]bool{}
	for _, c := range inv.TrustedContracts {
		fail := func(why string) error { return fmt.Errorf("component inventory: trusted contract %q %s", c.ID, why) }
		if !project.ValidComponentContractID(c.ID) {
			return fail("has an invalid id")
		}
		if _, ok := index.contracts[c.ID]; ok {
			return fail("appears twice")
		}
		if !project.ValidComponentOperation(c.Operation) || operations[c.Operation.Key()] {
			return fail("has an invalid operation, or one another contract binds")
		}
		operations[c.Operation.Key()] = true
		if c.Operation.Kind == project.OperationApplication {
			if d, ok := provenance[c.Operation.Declaration]; !ok || d.Language != "python" {
				return fail("names an operation declaration missing from provenance")
			}
		}
		d, ok := provenance[c.Contract]
		if _, production := index.components[c.Contract]; !ok || production || d.Language != "python" || d.ContractSHA256 == "" ||
			c.Contract == c.Operation.Declaration || declarations[c.Contract] {
			return fail("needs its own Python contract declaration, with an inline contract, outside scope")
		}
		declarations[c.Contract] = true
		if len(c.Tests) == 0 {
			return fail("selects no backing tests")
		}
		if err := componentProofReferences(provenance, c.Tests, "backing test"); err != nil {
			return err
		}
		invocations := map[[3]string]bool{}
		for _, v := range c.Invocations {
			key := [3]string{v.Kind, v.Check, v.ID}
			if v.Kind != "test" || !slices.Contains(c.Tests, v.Check) || strings.TrimSpace(v.ID) == "" || strings.TrimSpace(v.Command) == "" || invocations[key] {
				return fail(fmt.Sprintf("has an invalid or duplicate invocation %q", v.ID))
			}
			if err := componentFileReferences(files, v.Inputs); err != nil {
				return err
			}
			invocations[key] = true
		}
		index.contracts[c.ID] = c
	}
	return nil
}

type contractBinding struct {
	binding *ComponentContractBinding
	problem string
}

var (
	contractPinnedImage = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)
	contractWheelHash   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// contractBindings binds each declared contract to what the report records:
// the dependency image's base, or else the Python image, and the resolved
// wheels. Only a digest-pinned runtime and one complete wheel bind.
func (index *componentIndex) contractBindings(tc Toolchain) map[string]contractBinding {
	runtime := tc.PythonImage
	if tc.PythonDependencies != nil {
		runtime = tc.PythonDependencies.Base
	}
	out := map[string]contractBinding{}
	for id, c := range index.contracts {
		if !contractPinnedImage.MatchString(runtime) {
			out[id] = contractBinding{problem: "the report records no Python runtime pinned by digest"}
			continue
		}
		b := &ComponentContractBinding{Kind: c.Operation.Kind, Runtime: runtime}
		switch c.Operation.Kind {
		case project.OperationApplication:
			d := index.declarations[c.Operation.Declaration]
			b.Declaration = &d
		case project.OperationDependency:
			var found []toolchain.PythonArtifact
			if tc.PythonDependencies != nil {
				for _, a := range tc.PythonDependencies.Artifacts {
					if a.Name == c.Operation.Distribution {
						found = append(found, a)
					}
				}
			}
			switch {
			case len(found) == 0:
				out[id] = contractBinding{problem: fmt.Sprintf("no resolved wheel provides distribution %q", c.Operation.Distribution)}
				continue
			case len(found) > 1:
				out[id] = contractBinding{problem: fmt.Sprintf("%d resolved wheels claim distribution %q", len(found), c.Operation.Distribution)}
				continue
			case strings.TrimSpace(found[0].Version) == "" || !strings.HasSuffix(found[0].Filename, ".whl") || !contractWheelHash.MatchString(found[0].SHA256):
				out[id] = contractBinding{problem: fmt.Sprintf("the resolved wheel for distribution %q lacks a version, filename or SHA-256", c.Operation.Distribution)}
				continue
			}
			w := found[0]
			b.Wheel = &w
		}
		out[id] = contractBinding{binding: b}
	}
	return out
}

func sameContractBinding(a, b *ComponentContractBinding) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Kind == b.Kind && a.Runtime == b.Runtime &&
		(a.Declaration == nil) == (b.Declaration == nil) && (a.Declaration == nil || *a.Declaration == *b.Declaration) &&
		(a.Wheel == nil) == (b.Wheel == nil) && (a.Wheel == nil || *a.Wheel == *b.Wheel)
}

// contractFiles accepts only known file snapshots: an unknown path is an error,
// and a known one with other bytes is stale.
func contractFiles(files map[string]project.ComponentFile, inputs []project.ComponentFile) error {
	set, err := componentFileSet(inputs)
	if err != nil {
		return err
	}
	for name, input := range set {
		file, ok := files[name]
		if !ok {
			return fmt.Errorf("component contract tests: unknown file %q", name)
		}
		if file != input {
			return fmt.Errorf("%w: file %q does not match its inventory snapshot", ErrStaleComponentEvidence, name)
		}
	}
	return nil
}

func (index *componentIndex) contractTests(results []ComponentContractTestResult) ([]ComponentContractTestEvidence, error) {
	var rows []ComponentContractTestEvidence
	seen := map[[3]string]bool{}
	for _, supplied := range results {
		result := copyComponentContractTestResult(supplied)
		c, ok := index.contracts[result.Contract]
		if !ok || !slices.Contains(c.Tests, result.Check) {
			return nil, fmt.Errorf("component contract tests: trusted contract %q has no backing test %q", result.Contract, result.Check)
		}
		v := result.Invocation
		if v == nil || v.Kind != "test" || v.Check != result.Check || !slices.ContainsFunc(c.Invocations, func(declared project.ResolvedComponentInvocation) bool {
			return sameComponentInvocation(*v, declared)
		}) {
			return nil, fmt.Errorf("component contract tests: invocation does not match a declared backing test of %q", result.Contract)
		}
		key := [3]string{result.Contract, result.Check, v.ID}
		if seen[key] {
			return nil, fmt.Errorf("component contract tests: duplicate result for %q, %q, %q", key[0], key[1], key[2])
		}
		seen[key] = true
		inputs := map[string]bool{}
		for _, input := range result.Inputs {
			d, ok := index.declarations[input.ID]
			if !ok || inputs[input.ID] {
				return nil, fmt.Errorf("component contract tests: unknown or duplicate input %q", input.ID)
			}
			if input != d {
				return nil, fmt.Errorf("%w: input %q does not match its inventory snapshot", ErrStaleComponentEvidence, input.ID)
			}
			inputs[input.ID] = true
		}
		if err := contractFiles(index.files, result.FileInputs); err != nil {
			return nil, err
		}
		if result.Binding != nil && !sameContractBinding(result.Binding, index.bindings[c.ID].binding) {
			return nil, fmt.Errorf("%w: trusted contract %q's backing tests ran against an operation identity this report doesn't record", ErrStaleComponentEvidence, c.ID)
		}
		if !componentConformanceOutcome(result.Outcome) {
			return nil, fmt.Errorf("component contract tests: invalid outcome for %q", result.Contract)
		}
		tests := map[string]bool{}
		for _, test := range result.Tests {
			if !slices.Contains(c.Tests, test.Check) || tests[test.Check] || !componentConformanceOutcome(test.Outcome) {
				return nil, fmt.Errorf("component contract tests: unselected, duplicate or invalid test %q for %q", test.Check, result.Contract)
			}
			tests[test.Check] = true
			assertions := map[string]bool{}
			for _, assertion := range test.Assertions {
				if strings.TrimSpace(assertion.ID) == "" || assertions[assertion.ID] || !componentConformanceOutcome(assertion.Outcome) {
					return nil, fmt.Errorf("component contract tests: invalid or duplicate assertion %q for %q", assertion.ID, test.Check)
				}
				assertions[assertion.ID] = true
				if assertion.Source != nil {
					if err := contractFiles(index.files, []project.ComponentFile{*assertion.Source}); err != nil {
						return nil, err
					}
				}
			}
		}
		rows = append(rows, result.qualifyContractTest(c))
	}
	return copyComponentContractTests(rows), nil
}

func (r *ComponentContractTestResult) qualifyContractTest(c project.ResolvedComponentContract) ComponentContractTestEvidence {
	inputs := map[string]project.Component{}
	for _, input := range r.Inputs {
		inputs[input.ID] = input
	}
	files := map[string]bool{}
	for _, file := range r.FileInputs {
		files[file.Path] = true
	}
	declared := func(id string) bool {
		input, ok := inputs[id]
		return ok && files[input.Source]
	}
	ready := r.Runner != nil && strings.TrimSpace(r.Runner.Name) != "" && strings.TrimSpace(r.Runner.Version) != "" &&
		r.Binding != nil && declared(c.Contract)
	if c.Operation.Kind == project.OperationApplication {
		ready = ready && declared(c.Operation.Declaration)
	}
	for _, file := range r.Invocation.Inputs {
		ready = ready && files[file.Path]
	}
	row := ComponentContractTestEvidence{Origin: "supplied", Contract: r.Contract, Check: r.Check, Status: "unchecked", Result: *r}
	for _, test := range r.Tests {
		exercised := ready && test.Exercised && declared(test.Check)
		evidence := ComponentRuntimeTestEvidence{Check: test.Check, Status: "unchecked"}
		// A contract rests on its tests, so an interrupted or failing
		// process supports nothing, even a test that passed inside it.
		if exercised && test.Outcome == "passed" && r.Outcome == "passed" {
			evidence.Status = "tested"
			if test.Check == r.Check {
				row.Status = "tested"
			}
		}
		for _, assertion := range test.Assertions {
			item := ComponentRuntimeAssertionEvidence{ID: assertion.ID, Status: "unchecked"}
			if exercised && assertion.Exercised && assertion.Outcome == "passed" && assertion.Source != nil && files[assertion.Source.Path] {
				item.Status = "passed"
			}
			evidence.Assertions = append(evidence.Assertions, item)
		}
		row.Tests = append(row.Tests, evidence)
	}
	return row
}

// Retained backing-test results replay through the same validation against
// the current inventory and operation identities, so a changed contract,
// test, operation, runtime or wheel rejects the replacement rather than
// keeping support that no longer applies.
func (index *componentIndex) retainedContractTests(old *ComponentEvidence) ([]ComponentContractTestEvidence, error) {
	if old == nil {
		return nil, nil
	}
	prior := copyComponentEvidence(*old)
	var results []ComponentContractTestResult
	for _, row := range prior.ContractTests {
		if err := index.retainContractTest(prior.Inventory, row); err != nil {
			return nil, err
		}
		results = append(results, row.Result)
	}
	rows, err := index.contractTests(results)
	if err != nil {
		return nil, err
	}
	for i, row := range rows {
		was := prior.ContractTests[i]
		if was.Origin != "" && was.Origin != "gate" && was.Origin != "supplied" {
			return nil, fmt.Errorf("invalid contract test origin")
		}
		rows[i].Origin = was.Origin
		if row.Contract != was.Contract || row.Check != was.Check || row.Status != was.Status ||
			!slices.EqualFunc(row.Tests, was.Tests, func(a, b ComponentRuntimeTestEvidence) bool {
				return a.Check == b.Check && a.Status == b.Status && slices.Equal(a.Assertions, b.Assertions)
			}) {
			return nil, fmt.Errorf("%w: component contract tests: retained qualification changed for %q/%q", ErrStaleComponentEvidence, was.Contract, was.Check)
		}
	}
	return rows, nil
}

// A retained result also keeps its original anchors: the contract's operation
// and declaration, and every test it names, as the earlier inventory held
// them, even where the result itself supplied no snapshot.
func (index *componentIndex) retainContractTest(old project.ComponentInventory, row ComponentContractTestEvidence) error {
	c, ok := index.contracts[row.Contract]
	prior := slices.IndexFunc(old.TrustedContracts, func(o project.ResolvedComponentContract) bool { return o.ID == row.Contract })
	if row.Contract != row.Result.Contract || row.Check != row.Result.Check || !ok || prior < 0 ||
		old.TrustedContracts[prior].Operation != c.Operation || old.TrustedContracts[prior].Contract != c.Contract {
		return fmt.Errorf("%w: component contract tests: retained trusted contract %q changed", ErrStaleComponentEvidence, row.Contract)
	}
	ids := []string{c.Contract, row.Check}
	if c.Operation.Kind == project.OperationApplication {
		ids = append(ids, c.Operation.Declaration)
	}
	for _, test := range row.Result.Tests {
		ids = append(ids, test.Check)
	}
	return index.sameAnchors(old, ids, "component contract tests")
}

// sameAnchors requires each declaration to be identical in the earlier
// inventory and this one.
func (index *componentIndex) sameAnchors(old project.ComponentInventory, ids []string, kind string) error {
	declarations := slices.Clone(old.Components)
	if old.Provenance != nil {
		declarations = append(declarations, old.Provenance.Declarations...)
	}
	for _, id := range ids {
		pos := slices.IndexFunc(declarations, func(c project.Component) bool { return c.ID == id })
		c, ok := index.declarations[id]
		if pos < 0 || !ok || declarations[pos] != c {
			return fmt.Errorf("%w: %s: retained declaration %q changed", ErrStaleComponentEvidence, kind, id)
		}
	}
	return nil
}

// contractUses validates a proof attempt's account of its trusted boundary.
func (index *componentIndex) contractUses(result ComponentProofResult) error {
	assumed := map[string]bool{}
	for _, use := range result.Assumed {
		c, ok := index.contracts[use.Contract]
		if !ok || assumed[use.Contract] {
			return fmt.Errorf("component proofs: unknown or duplicate trusted contract %q assumed by %q", use.Contract, result.Target)
		}
		if use.Declaration != index.declarations[c.Contract] {
			return fmt.Errorf("%w: %q assumed trusted contract %q from a declaration this inventory doesn't hold", ErrStaleComponentEvidence, result.Target, use.Contract)
		}
		assumed[use.Contract] = true
	}
	seen := map[ComponentUnaccounted]bool{}
	for _, u := range result.Unaccounted {
		if (u.Kind != MissingContract && u.Kind != UnaccountedAssumption) || strings.TrimSpace(u.Operation) == "" || seen[u] {
			return fmt.Errorf("component proofs: invalid or duplicate unaccounted boundary %q for %q", u.Operation, result.Target)
		}
		seen[u] = true
	}
	return nil
}

// retainContractUses keeps a retained attempt's assumed contracts anchored to
// the operation and declaration the earlier inventory gave them.
func (index *componentIndex) retainContractUses(old project.ComponentInventory, result ComponentProofResult) error {
	for _, use := range result.Assumed {
		c, ok := index.contracts[use.Contract]
		prior := slices.IndexFunc(old.TrustedContracts, func(o project.ResolvedComponentContract) bool { return o.ID == use.Contract })
		if !ok || prior < 0 || old.TrustedContracts[prior].Operation != c.Operation || old.TrustedContracts[prior].Contract != c.Contract {
			return fmt.Errorf("%w: component proofs: retained trusted contract %q changed", ErrStaleComponentEvidence, use.Contract)
		}
	}
	return nil
}

// contractStanding derives every declared contract's binding, support and use,
// and the trusted-boundary diagnostics, from the evidence being committed.
func (index *componentIndex) contractStanding(e *ComponentEvidence) {
	supported := map[string]map[string]bool{}
	for _, row := range e.ContractTests {
		for _, test := range row.Tests {
			if test.Status == "tested" {
				if supported[row.Contract] == nil {
					supported[row.Contract] = map[string]bool{}
				}
				supported[row.Contract][test.Check] = true
			}
		}
	}
	relying := map[string][]string{}
	var diagnostics []ComponentContractDiagnostic
	for _, p := range e.Proofs {
		if p.Result == nil {
			continue
		}
		for _, use := range p.Result.Assumed {
			relying[use.Contract] = append(relying[use.Contract], p.Target)
		}
		for _, u := range p.Result.Unaccounted {
			detail := u.Operation
			if u.Detail != "" {
				detail += ": " + u.Detail
			}
			diagnostics = append(diagnostics, ComponentContractDiagnostic{Kind: u.Kind, Target: p.Target, Detail: detail})
		}
	}
	e.Contracts = nil
	for _, c := range e.Inventory.TrustedContracts {
		row := ComponentContractEvidence{Contract: c.ID, Support: "untested", Use: "declared"}
		b := index.bindings[c.ID]
		if b.binding == nil {
			diagnostics = append(diagnostics, ComponentContractDiagnostic{Kind: UnresolvedOperation, Contract: c.ID, Detail: b.problem})
		} else {
			row.Binding = copyContractBinding(b.binding)
			tested := true
			for _, test := range c.Tests {
				tested = tested && supported[c.ID][test]
			}
			if tested {
				row.Support = "tested"
			}
		}
		if targets := relying[c.ID]; len(targets) > 0 {
			row.Use, row.Relying = "assumed", slices.Compact(slices.Sorted(slices.Values(targets)))
			if row.Support != "tested" {
				for _, target := range row.Relying {
					diagnostics = append(diagnostics, ComponentContractDiagnostic{Kind: UntestedAssumption, Contract: c.ID, Target: target,
						Detail: "the proof assumes this contract, and not every backing test supports it"})
				}
			}
		}
		e.Contracts = append(e.Contracts, row)
	}
	sortContractDiagnostics(diagnostics)
	e.ContractDiagnostics = diagnostics
}

func sortContractDiagnostics(diagnostics []ComponentContractDiagnostic) {
	sort.Slice(diagnostics, func(i, j int) bool {
		a, b := diagnostics[i], diagnostics[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.Contract != b.Contract {
			return a.Contract < b.Contract
		}
		return a.Detail < b.Detail
	})
}

func copyContractBinding(b *ComponentContractBinding) *ComponentContractBinding {
	if b == nil {
		return nil
	}
	out := *b
	if b.Declaration != nil {
		d := *b.Declaration
		out.Declaration = &d
	}
	if b.Wheel != nil {
		w := *b.Wheel
		out.Wheel = &w
	}
	return &out
}

func copyComponentContractTestResult(result ComponentContractTestResult) ComponentContractTestResult {
	if result.Invocation != nil {
		invocation := copyComponentInvocation(*result.Invocation)
		result.Invocation = &invocation
	}
	if result.Runner != nil {
		runner := *result.Runner
		runner.Pins = maps.Clone(runner.Pins)
		result.Runner = &runner
	}
	result.Binding = copyContractBinding(result.Binding)
	result.Inputs = slices.Clone(result.Inputs)
	sort.Slice(result.Inputs, func(i, j int) bool { return result.Inputs[i].ID < result.Inputs[j].ID })
	result.FileInputs = copyComponentFiles(result.FileInputs)
	result.Tests = copyComponentRuntimeTests(result.Tests)
	result.Diagnostics = slices.Clone(result.Diagnostics)
	return result
}

func copyComponentContractTests(rows []ComponentContractTestEvidence) []ComponentContractTestEvidence {
	rows = slices.Clone(rows)
	for i := range rows {
		rows[i].Result = copyComponentContractTestResult(rows[i].Result)
		rows[i].Tests = slices.Clone(rows[i].Tests)
		for j := range rows[i].Tests {
			test := &rows[i].Tests[j]
			test.Assertions = slices.Clone(test.Assertions)
			sort.Slice(test.Assertions, func(a, b int) bool { return test.Assertions[a].ID < test.Assertions[b].ID })
		}
		sort.Slice(rows[i].Tests, func(a, b int) bool { return rows[i].Tests[a].Check < rows[i].Tests[b].Check })
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Contract != b.Contract {
			return a.Contract < b.Contract
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

func copyComponentContracts(rows []ComponentContractEvidence) []ComponentContractEvidence {
	rows = slices.Clone(rows)
	for i := range rows {
		rows[i].Binding = copyContractBinding(rows[i].Binding)
		rows[i].Relying = slices.Clone(rows[i].Relying)
		sort.Strings(rows[i].Relying)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Contract < rows[j].Contract })
	return rows
}

func copyComponentContractUses(uses []ComponentContractUse) []ComponentContractUse {
	uses = slices.Clone(uses)
	sort.Slice(uses, func(i, j int) bool { return uses[i].Contract < uses[j].Contract })
	return uses
}

func copyComponentUnaccounted(rows []ComponentUnaccounted) []ComponentUnaccounted {
	rows = slices.Clone(rows)
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		return a.Detail < b.Detail
	})
	return rows
}

func copyTrustedContracts(contracts []project.ResolvedComponentContract) []project.ResolvedComponentContract {
	contracts = slices.Clone(contracts)
	for i := range contracts {
		c := &contracts[i]
		c.Tests = slices.Clone(c.Tests)
		sort.Strings(c.Tests)
		c.Invocations = slices.Clone(c.Invocations)
		for j := range c.Invocations {
			c.Invocations[j] = copyComponentInvocation(c.Invocations[j])
		}
		sortComponentInvocations(c.Invocations)
	}
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].ID < contracts[j].ID })
	return contracts
}
