package verify

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/project"
)

// ComponentProofResult records a supplied proof attempt for an exact target.
// Inputs are declarations; optional Invocation, FileInputs and Tool record
// actual provenance. Execution adapters must establish these facts from runs;
// attribution neither executes tools nor infers results from configuration.
type ComponentProofResult struct {
	Target     string                               `json:"target"`
	Verifier   string                               `json:"verifier"`
	Passed     bool                                 `json:"passed"`
	Inputs     []project.Component                  `json:"inputs"`
	Verified   []string                             `json:"verified"`
	Contracts  []string                             `json:"contracts"`
	Errors     []string                             `json:"errors,omitempty"`
	Invocation *project.ResolvedComponentInvocation `json:"invocation,omitempty"`
	FileInputs []project.ComponentFile              `json:"file_inputs,omitempty"`
	Tool       *ComponentProofTool                  `json:"tool,omitempty"`
	// Assumed and Unaccounted are the attempt's trusted boundary as its
	// prover's adapter reports it from the verifier's actual inputs (#294):
	// each trusted contract they assumed, and each boundary reached or
	// assumption made that no declared contract accounts for. Neither proves
	// a contract, and an unaccounted boundary leaves the target unproved.
	Assumed     []ComponentContractUse `json:"assumed,omitempty"`
	Unaccounted []ComponentUnaccounted `json:"unaccounted,omitempty"`
}

// ComponentProofTool retains supplied tool identity and pins as opaque data.
type ComponentProofTool struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	Pins    map[string]string `json:"pins,omitempty"`
}

// ComponentAssumptionRecord binds a supplied premise to its selected declaration
// and target snapshot. It records an assumption, never a verified guarantee.
type ComponentAssumptionRecord struct {
	Target     string                              `json:"target"`
	Input      project.Component                   `json:"input"`
	Assumption project.ResolvedComponentAssumption `json:"assumption"`
}

type ComponentAssumptionEvidence struct {
	Target   string                    `json:"target"`
	Status   string                    `json:"status"`
	Verified bool                      `json:"verified"`
	Record   ComponentAssumptionRecord `json:"record"`
}

// ComponentEvidence keeps complete proof coverage, supplied assumptions,
// conformance, runtime and trusted contracts' backing tests independent.
// Declarations alone create no records. Contracts and ContractDiagnostics are
// derived from the rest on every attribution, never supplied.
type ComponentEvidence struct {
	Inventory           project.ComponentInventory      `json:"inventory"`
	Proofs              []ComponentProof                `json:"proofs"`
	Assumptions         []ComponentAssumptionEvidence   `json:"assumptions,omitempty"`
	Conformance         []ComponentConformanceEvidence  `json:"conformance,omitempty"`
	Runtime             []ComponentRuntimeEvidence      `json:"runtime,omitempty"`
	ContractTests       []ComponentContractTestEvidence `json:"contract_tests,omitempty"`
	Contracts           []ComponentContractEvidence     `json:"contracts,omitempty"`
	ContractDiagnostics []ComponentContractDiagnostic   `json:"contract_diagnostics,omitempty"`
}

// ComponentProof is one component's proof status, including an unsuccessful
// attempt when there was one. A missing result leaves the component unchecked.
type ComponentProof struct {
	Target string                `json:"target"`
	Status string                `json:"status"`
	Result *ComponentProofResult `json:"result,omitempty"`
}

// AttributeComponentProofs replaces or clears only proof evidence, revalidating
// retained assumptions, conformance and runtime against inv and the report's
// bounds. It runs no tools and changes no gate outcome or legacy check. Invalid
// attribution leaves the report untouched; incomplete evidence proves nothing.
func (r *Report) AttributeComponentProofs(inv *project.ComponentInventory, results []ComponentProofResult) error {
	evidence, index, err := r.componentReplacement(inv)
	if err != nil {
		return err
	}
	if evidence.Proofs, err = index.proofs(evidence.Inventory, results); err != nil {
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
	if evidence.ContractTests, err = index.retainedContractTests(r.Components); err != nil {
		return err
	}
	r.commitComponentEvidence(index, evidence)
	return nil
}

// AttributeComponentAssumptions replaces or clears only supplied assumptions,
// revalidating retained proof, conformance and runtime attempts against inv and
// the report's bounds, including their original targets and selected contracts
// even when the attempt supplied no snapshots. Records remain assumed and
// unverified and never change proof or gate outcomes.
func (r *Report) AttributeComponentAssumptions(inv *project.ComponentInventory, records []ComponentAssumptionRecord) error {
	evidence, index, err := r.componentReplacement(inv)
	if err != nil {
		return err
	}
	if evidence.Assumptions, err = index.assumptions(records, false); err != nil {
		return err
	}
	if evidence.Proofs, err = index.retainedProofs(evidence.Inventory, r.Components); err != nil {
		return err
	}
	if evidence.Conformance, err = index.retainedConformance(r.Components, r.Bounds); err != nil {
		return err
	}
	if evidence.Runtime, err = index.retainedRuntime(r.Components, r.Bounds); err != nil {
		return err
	}
	if evidence.ContractTests, err = index.retainedContractTests(r.Components); err != nil {
		return err
	}
	r.commitComponentEvidence(index, evidence)
	return nil
}

func (r *Report) componentReplacement(inv *project.ComponentInventory) (*ComponentEvidence, *componentIndex, error) {
	if inv == nil || inv.Mode != "explicit" {
		return nil, nil, fmt.Errorf("component evidence requires an explicit inventory")
	}
	if r.ModelOnly {
		return nil, nil, fmt.Errorf("component evidence cannot be attributed to a model-only report")
	}
	evidence := &ComponentEvidence{Inventory: copyComponentInventory(*inv)}
	index, err := indexComponentInventory(evidence.Inventory)
	if err != nil {
		return nil, nil, err
	}
	index.bindings = index.contractBindings(r.Toolchain)
	return evidence, index, nil
}

func (r *Report) commitComponentEvidence(index *componentIndex, evidence *ComponentEvidence) {
	index.contractStanding(evidence)
	next := *r
	next.Components, next.Assurance = evidence, "component evidence"
	next.Fingerprint = Fingerprint(next)
	*r = next
}

type componentIndex struct {
	components   map[string]project.Component
	declarations map[string]project.Component
	files        map[string]project.ComponentFile
	checks       map[string]project.ResolvedComponentCheck
	contracts    map[string]project.ResolvedComponentContract
	bindings     map[string]contractBinding
}

// Each collection is unique within itself. A production declaration or module
// may also be selected as provenance, but the overlapping snapshots must agree.
func indexComponentInventory(inv project.ComponentInventory) (*componentIndex, error) {
	index := &componentIndex{declarations: map[string]project.Component{}, files: map[string]project.ComponentFile{}, checks: map[string]project.ResolvedComponentCheck{},
		contracts: map[string]project.ResolvedComponentContract{}}
	for i, scope := range inv.Scope {
		if i > 0 && scope == inv.Scope[i-1] {
			return nil, fmt.Errorf("component inventory: duplicate scope %q", scope)
		}
	}
	provenance := project.ComponentProvenance{}
	if inv.Provenance != nil {
		provenance = *inv.Provenance
	}
	fileSets := make([]map[string]project.ComponentFile, 0, 3)
	for _, files := range [][]project.ComponentFile{inv.Files, provenance.Files, provenance.Modules} {
		set, err := componentFileSet(files)
		if err != nil {
			return nil, err
		}
		for name, file := range set {
			if old, ok := index.files[name]; ok && old != file {
				return nil, fmt.Errorf("component inventory: inconsistent file %q", name)
			}
			index.files[name] = file
		}
		fileSets = append(fileSets, set)
	}
	declarationSets := make([]map[string]project.Component, 0, 2)
	for n, declarations := range [][]project.Component{inv.Components, provenance.Declarations} {
		set := map[string]project.Component{}
		for _, c := range declarations {
			if c.Language == "" || c.Package == "" || c.Symbol == "" || c.Source == "" || c.ID != c.Language+":"+c.Package+"#"+c.Symbol+"@"+c.Source {
				return nil, fmt.Errorf("component inventory: noncanonical declaration %q", c.ID)
			}
			if _, ok := set[c.ID]; ok {
				return nil, fmt.Errorf("component inventory: duplicate declaration %q", c.ID)
			}
			file, ok := fileSets[n][c.Source]
			if !ok || file.SHA256 != c.SourceSHA256 {
				return nil, fmt.Errorf("component inventory: declaration %q does not match its source snapshot", c.ID)
			}
			if old, ok := index.declarations[c.ID]; ok && old != c {
				return nil, fmt.Errorf("component inventory: inconsistent declaration %q", c.ID)
			}
			set[c.ID], index.declarations[c.ID] = c, c
		}
		declarationSets = append(declarationSets, set)
	}
	index.components = declarationSets[0]
	for _, check := range inv.Checks {
		if _, ok := index.components[check.Target]; !ok {
			return nil, fmt.Errorf("component inventory: unknown production target %q", check.Target)
		}
		if _, ok := index.checks[check.Target]; ok {
			return nil, fmt.Errorf("component inventory: duplicate check target %q", check.Target)
		}
		if check.Proof != nil {
			if len(check.Proof.Contracts) == 0 {
				return nil, fmt.Errorf("component inventory: target %q selects no contracts", check.Target)
			}
			if err := componentProofReferences(index.components, check.Proof.Contracts, "selected contract"); err != nil {
				return nil, err
			}
			for _, id := range check.Proof.Contracts {
				if index.components[id].ContractSHA256 == "" {
					return nil, fmt.Errorf("component inventory: selected declaration %q has no contract", id)
				}
			}
		}
		for _, selection := range []struct {
			kind string
			ids  []string
		}{{"test", check.Tests}, {"conformance", check.Conformance}} {
			if err := componentProofReferences(declarationSets[1], selection.ids, selection.kind); err != nil {
				return nil, err
			}
		}
		assumptions := map[string]bool{}
		for _, a := range check.Assumptions {
			if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.Statement) == "" || strings.TrimSpace(a.Reason) == "" || assumptions[a.ID] {
				return nil, fmt.Errorf("component inventory: invalid or duplicate assumption %q for %q", a.ID, check.Target)
			}
			if err := componentFileReferences(fileSets[1], []project.ComponentFile{a.Source}); err != nil {
				return nil, err
			}
			assumptions[a.ID] = true
		}
		invocations := map[[3]string]bool{}
		for _, v := range check.Invocations {
			key := [3]string{v.Kind, v.Check, v.ID}
			if strings.TrimSpace(v.ID) == "" || strings.TrimSpace(v.Command) == "" || invocations[key] {
				return nil, fmt.Errorf("component inventory: invalid or duplicate invocation %q for %q", v.ID, check.Target)
			}
			selected := false
			switch v.Kind {
			case "proof":
				selected = check.Proof != nil && v.Check == check.Target
			case "test":
				selected = slices.Contains(check.Tests, v.Check)
			case "conformance":
				selected = slices.Contains(check.Conformance, v.Check)
			}
			if !selected {
				return nil, fmt.Errorf("component inventory: invocation %q names an unselected %s check %q", v.ID, v.Kind, v.Check)
			}
			if err := componentFileReferences(fileSets[1], v.Inputs); err != nil {
				return nil, err
			}
			invocations[key] = true
		}
		index.checks[check.Target] = check
	}
	if err := index.indexTrustedContracts(inv, declarationSets[1], fileSets[1]); err != nil {
		return nil, err
	}
	return index, nil
}

func componentFileSet(files []project.ComponentFile) (map[string]project.ComponentFile, error) {
	set := map[string]project.ComponentFile{}
	for _, file := range files {
		if file.Path == "" || file.SHA256 == "" {
			return nil, fmt.Errorf("component evidence: missing file identity for %q", file.Path)
		}
		if _, ok := set[file.Path]; ok {
			return nil, fmt.Errorf("component evidence: duplicate file %q", file.Path)
		}
		set[file.Path] = file
	}
	return set, nil
}

func componentFileReferences(files map[string]project.ComponentFile, inputs []project.ComponentFile) error {
	set, err := componentFileSet(inputs)
	if err != nil {
		return err
	}
	for name, input := range set {
		file, ok := files[name]
		if !ok {
			return fmt.Errorf("component evidence: unknown file %q", name)
		}
		if file != input {
			return fmt.Errorf("%w: file %q does not match its inventory snapshot", ErrStaleComponentEvidence, name)
		}
	}
	return nil
}

// matchComponentInvocation checks a result's actual invocation against the
// declarations it must match exactly. A missing or unselected one is an
// error, and a declared (kind, check, id) whose other values differ is stale.
func matchComponentInvocation(v *project.ResolvedComponentInvocation, kind, check string, declared []project.ResolvedComponentInvocation) error {
	if v == nil || v.Kind != kind || v.Check != check {
		return fmt.Errorf("invocation does not match a selected %s check", kind)
	}
	for _, d := range declared {
		if d.Kind == v.Kind && d.Check == v.Check && d.ID == v.ID {
			if sameComponentInvocation(*v, d) {
				return nil
			}
			return fmt.Errorf("%w: invocation %q no longer matches its declaration", ErrStaleComponentEvidence, v.ID)
		}
	}
	return fmt.Errorf("invocation %q is not declared", v.ID)
}

// componentInput checks one supplied declaration snapshot: unknown is an
// error, and a known one with other contents is stale.
func (index *componentIndex) componentInput(input project.Component) error {
	c, ok := index.declarations[input.ID]
	if !ok {
		return fmt.Errorf("unknown input %q", input.ID)
	}
	if input != c {
		return fmt.Errorf("%w: input %q does not match its inventory snapshot", ErrStaleComponentEvidence, input.ID)
	}
	return nil
}

func (index *componentIndex) proofs(inv project.ComponentInventory, results []ComponentProofResult) ([]ComponentProof, error) {
	byTarget := map[string]*ComponentProofResult{}
	for _, result := range results {
		check, ok := index.checks[result.Target]
		if !ok || check.Proof == nil {
			return nil, fmt.Errorf("component proofs: target %q has no selected proof", result.Target)
		}
		if _, ok := byTarget[result.Target]; ok {
			return nil, fmt.Errorf("component proofs: duplicate result for %q", result.Target)
		}
		if err := componentProofReferences(index.declarations, result.Verified, "verified"); err != nil {
			return nil, err
		}
		if err := componentProofReferences(index.declarations, result.Contracts, "contract"); err != nil {
			return nil, err
		}
		inputs := map[string]bool{}
		for _, input := range result.Inputs {
			if inputs[input.ID] {
				return nil, fmt.Errorf("component proofs: duplicate input %q", input.ID)
			}
			if err := index.componentInput(input); err != nil {
				return nil, fmt.Errorf("component proofs: %w", err)
			}
			inputs[input.ID] = true
		}
		if err := index.contractUses(result); err != nil {
			return nil, err
		}
		copy := copyComponentProofResult(result)
		if v := copy.Invocation; v != nil {
			if err := matchComponentInvocation(v, "proof", result.Target, check.Invocations); err != nil {
				return nil, fmt.Errorf("component proofs: %q: %w", result.Target, err)
			}
		}
		if err := componentFileReferences(index.files, copy.FileInputs); err != nil {
			return nil, err
		}
		if len(copy.FileInputs) > 0 && !slices.ContainsFunc(copy.FileInputs, func(file project.ComponentFile) bool {
			return file.Path == index.components[result.Target].Source
		}) {
			return nil, fmt.Errorf("component proofs: actual files omit the source of %q", result.Target)
		}
		byTarget[result.Target] = &copy
	}
	proofs := make([]ComponentProof, 0, len(inv.Components))
	for _, c := range inv.Components {
		row := ComponentProof{Target: c.ID, Status: "unchecked", Result: byTarget[c.ID]}
		if row.Result != nil && row.Result.covers(index.checks[c.ID].Proof.Contracts) {
			row.Status = "proved"
		}
		proofs = append(proofs, row)
	}
	return proofs, nil
}

func sameComponentInvocation(a, b project.ResolvedComponentInvocation) bool {
	return a.ID == b.ID && a.Kind == b.Kind && a.Check == b.Check && a.Command == b.Command &&
		a.WorkingDir == b.WorkingDir && slices.Equal(a.Args, b.Args) && maps.Equal(a.Env, b.Env) &&
		maps.Equal(a.Sampling, b.Sampling) && slices.Equal(a.Inputs, b.Inputs)
}

// assumptions validates supplied or retained records against the inventory.
// A record that names no configured target, another declaration's snapshot or
// an assumption its target doesn't declare, or repeats a target's assumption,
// is invalid. One whose target or assumption the inventory holds with other
// contents is stale, and so is a retained one whose target or assumption the
// inventory no longer holds, as every kind's stale evidence is (D-0205,
// D-0253).
func (index *componentIndex) assumptions(records []ComponentAssumptionRecord, retained bool) ([]ComponentAssumptionEvidence, error) {
	var rows []ComponentAssumptionEvidence
	seen := map[[2]string]bool{}
	for _, record := range records {
		key := [2]string{record.Target, record.Assumption.ID}
		check, ok := index.checks[record.Target]
		known := ok && record.Input.ID == record.Target && slices.ContainsFunc(check.Assumptions, func(a project.ResolvedComponentAssumption) bool {
			return a.ID == record.Assumption.ID
		})
		switch {
		case seen[key], !known && !retained:
			return nil, fmt.Errorf("component assumptions: invalid, stale or duplicate record %q for %q", record.Assumption.ID, record.Target)
		case !known, record.Input != index.components[record.Target], !slices.Contains(check.Assumptions, record.Assumption):
			return nil, fmt.Errorf("%w: component assumptions: invalid, stale or duplicate record %q for %q", ErrStaleComponentEvidence, record.Assumption.ID, record.Target)
		}
		seen[key] = true
		rows = append(rows, ComponentAssumptionEvidence{Target: record.Target, Status: "assumed", Record: record})
	}
	return copyComponentAssumptions(rows), nil
}

// Retained evidence is copied and replayed through the same validation as new
// evidence. The prior inventory additionally anchors incomplete attempts, and
// requalification must preserve their existing claims before a commit is allowed.
func (index *componentIndex) retainedProofs(inv project.ComponentInventory, old *ComponentEvidence) ([]ComponentProof, error) {
	var results []ComponentProofResult
	if old != nil {
		prior := copyComponentEvidence(*old)
		for _, row := range prior.Proofs {
			if row.Result == nil {
				if row.Status != "unchecked" {
					return nil, fmt.Errorf("component proofs: invalid retained coverage for %q", row.Target)
				}
				continue
			}
			if err := index.retainProof(prior.Inventory, row); err != nil {
				return nil, err
			}
			results = append(results, *row.Result)
		}
	}
	return index.proofs(inv, results)
}

func (index *componentIndex) retainedAssumptions(old *ComponentEvidence) ([]ComponentAssumptionEvidence, error) {
	var records []ComponentAssumptionRecord
	if old != nil {
		for _, row := range old.Assumptions {
			if row.Target != row.Record.Target || row.Status != "assumed" || row.Verified {
				return nil, fmt.Errorf("component assumptions: invalid retained record for %q", row.Target)
			}
			records = append(records, row.Record)
		}
	}
	return index.assumptions(records, true)
}

func (index *componentIndex) retainedConformance(old *ComponentEvidence, bounds map[string]string) ([]ComponentConformanceEvidence, error) {
	if old == nil {
		return nil, nil
	}
	prior := copyComponentEvidence(*old)
	var results []ComponentConformanceResult
	for _, row := range prior.Conformance {
		if err := index.retainConformance(prior.Inventory, row); err != nil {
			return nil, err
		}
		results = append(results, row.Result)
	}
	rows, err := index.conformance(results, bounds)
	if err != nil {
		return nil, err
	}
	for i, row := range rows {
		was := prior.Conformance[i]
		if was.Origin != "" && was.Origin != "gate" && was.Origin != "supplied" {
			return nil, fmt.Errorf("invalid conformance origin")
		}
		rows[i].Origin = was.Origin
		if row.Target != was.Target || row.Check != was.Check || row.Status != was.Status ||
			row.TransitionsChecked != was.TransitionsChecked || row.ExhaustiveStates != was.ExhaustiveStates || row.EveryStep != was.EveryStep {
			return nil, fmt.Errorf("%w: component conformance: retained qualification changed for %q/%q", ErrStaleComponentEvidence, was.Target, was.Check)
		}
	}
	return rows, nil
}

func (index *componentIndex) retainProof(old project.ComponentInventory, row ComponentProof) error {
	check, ok := index.checks[row.Target]
	prior := slices.IndexFunc(old.Checks, func(c project.ResolvedComponentCheck) bool { return c.Target == row.Target })
	if row.Target != row.Result.Target || !ok || check.Proof == nil || prior < 0 || old.Checks[prior].Proof == nil ||
		!slices.Equal(check.Proof.Contracts, old.Checks[prior].Proof.Contracts) {
		return fmt.Errorf("%w: component proofs: retained selection changed for %q", ErrStaleComponentEvidence, row.Target)
	}
	// The inventory anchors incomplete attempts that supplied no input snapshot.
	declarations := slices.Clone(old.Components)
	if old.Provenance != nil {
		declarations = append(declarations, old.Provenance.Declarations...)
	}
	for _, ids := range [][]string{{row.Target}, check.Proof.Contracts, row.Result.Verified, row.Result.Contracts} {
		for _, id := range ids {
			pos := slices.IndexFunc(declarations, func(c project.Component) bool { return c.ID == id })
			c, ok := index.declarations[id]
			if pos < 0 || !ok || declarations[pos] != c {
				return fmt.Errorf("%w: component proofs: retained declaration %q changed", ErrStaleComponentEvidence, id)
			}
		}
	}
	if err := index.retainContractUses(old, *row.Result); err != nil {
		return err
	}
	status := "unchecked"
	if row.Result.covers(check.Proof.Contracts) {
		status = "proved"
	}
	if row.Status != status {
		return fmt.Errorf("component proofs: retained coverage changed for %q", row.Target)
	}
	return nil
}

func componentProofReferences(components map[string]project.Component, ids []string, kind string) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := components[id]; !ok {
			return fmt.Errorf("component proofs: unknown %s ID %q", kind, id)
		}
		if seen[id] {
			return fmt.Errorf("component proofs: duplicate %s ID %q", kind, id)
		}
		seen[id] = true
	}
	return nil
}

func (r *ComponentProofResult) covers(contracts []string) bool {
	if !r.Passed || r.Verifier == "" || len(r.Errors) > 0 || len(r.Unaccounted) > 0 || !slices.Contains(r.Verified, r.Target) {
		return false
	}
	for _, id := range contracts {
		if !slices.Contains(r.Contracts, id) {
			return false
		}
	}
	inputs := map[string]bool{}
	for _, c := range r.Inputs {
		inputs[c.ID] = true
	}
	for _, ids := range [][]string{r.Verified, r.Contracts} {
		for _, id := range ids {
			if !inputs[id] {
				return false
			}
		}
	}
	return true
}

// Copies own every nested slice and map and sort reference sets without
// changing the caller's inventory or results. Invocation arguments and tool
// diagnostics retain their ordering; declarations do not establish evidence.
func copyComponentInventory(inv project.ComponentInventory) project.ComponentInventory {
	inv.Scope = slices.Clone(inv.Scope)
	sort.Strings(inv.Scope)
	inv.Files = copyComponentFiles(inv.Files)
	inv.Components = slices.Clone(inv.Components)
	sort.Slice(inv.Components, func(i, j int) bool { return inv.Components[i].ID < inv.Components[j].ID })
	if inv.Provenance != nil {
		provenance := *inv.Provenance
		provenance.Declarations = slices.Clone(provenance.Declarations)
		sort.Slice(provenance.Declarations, func(i, j int) bool {
			return provenance.Declarations[i].ID < provenance.Declarations[j].ID
		})
		provenance.Files = copyComponentFiles(provenance.Files)
		provenance.Modules = copyComponentFiles(provenance.Modules)
		inv.Provenance = &provenance
	}
	inv.Checks = slices.Clone(inv.Checks)
	for i := range inv.Checks {
		check := &inv.Checks[i]
		if check.Proof != nil {
			check.Proof = &project.ResolvedComponentProof{Contracts: slices.Clone(check.Proof.Contracts)}
			sort.Strings(check.Proof.Contracts)
		}
		check.Tests = slices.Clone(check.Tests)
		sort.Strings(check.Tests)
		check.Conformance = slices.Clone(check.Conformance)
		sort.Strings(check.Conformance)
		check.Assumptions = slices.Clone(check.Assumptions)
		sort.Slice(check.Assumptions, func(i, j int) bool { return check.Assumptions[i].ID < check.Assumptions[j].ID })
		check.Invocations = slices.Clone(check.Invocations)
		for j := range check.Invocations {
			check.Invocations[j] = copyComponentInvocation(check.Invocations[j])
		}
		sortComponentInvocations(check.Invocations)
	}
	sort.Slice(inv.Checks, func(i, j int) bool { return inv.Checks[i].Target < inv.Checks[j].Target })
	if inv.TrustedContracts != nil {
		inv.TrustedContracts = copyTrustedContracts(inv.TrustedContracts)
	}
	return inv
}

func sortComponentInvocations(invocations []project.ResolvedComponentInvocation) {
	sort.Slice(invocations, func(i, j int) bool {
		a, b := invocations[i], invocations[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		return a.ID < b.ID
	})
}

func copyComponentFiles(files []project.ComponentFile) []project.ComponentFile {
	files = slices.Clone(files)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

func copyComponentInvocation(v project.ResolvedComponentInvocation) project.ResolvedComponentInvocation {
	v.Args = slices.Clone(v.Args)
	v.Env = maps.Clone(v.Env)
	v.Sampling = maps.Clone(v.Sampling)
	v.Inputs = copyComponentFiles(v.Inputs)
	return v
}

func copyComponentProofResult(result ComponentProofResult) ComponentProofResult {
	result.Inputs = slices.Clone(result.Inputs)
	sort.Slice(result.Inputs, func(i, j int) bool { return result.Inputs[i].ID < result.Inputs[j].ID })
	result.Verified = slices.Clone(result.Verified)
	sort.Strings(result.Verified)
	result.Contracts = slices.Clone(result.Contracts)
	sort.Strings(result.Contracts)
	result.Errors = slices.Clone(result.Errors)
	if result.Invocation != nil {
		invocation := copyComponentInvocation(*result.Invocation)
		result.Invocation = &invocation
	}
	result.FileInputs = copyComponentFiles(result.FileInputs)
	if result.Tool != nil {
		tool := *result.Tool
		tool.Pins = maps.Clone(tool.Pins)
		result.Tool = &tool
	}
	if result.Assumed != nil {
		result.Assumed = copyComponentContractUses(result.Assumed)
	}
	if result.Unaccounted != nil {
		result.Unaccounted = copyComponentUnaccounted(result.Unaccounted)
	}
	return result
}

func copyComponentAssumptions(rows []ComponentAssumptionEvidence) []ComponentAssumptionEvidence {
	rows = slices.Clone(rows)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Target != rows[j].Target {
			return rows[i].Target < rows[j].Target
		}
		return rows[i].Record.Assumption.ID < rows[j].Record.Assumption.ID
	})
	return rows
}

func copyComponentEvidence(e ComponentEvidence) *ComponentEvidence {
	e.Inventory = copyComponentInventory(e.Inventory)
	e.Proofs = slices.Clone(e.Proofs)
	for i := range e.Proofs {
		if e.Proofs[i].Result != nil {
			result := copyComponentProofResult(*e.Proofs[i].Result)
			e.Proofs[i].Result = &result
		}
	}
	sort.Slice(e.Proofs, func(i, j int) bool { return e.Proofs[i].Target < e.Proofs[j].Target })
	e.Assumptions = copyComponentAssumptions(e.Assumptions)
	e.Conformance = copyComponentConformance(e.Conformance)
	e.Runtime = copyComponentRuntime(e.Runtime)
	if e.ContractTests != nil {
		e.ContractTests = copyComponentContractTests(e.ContractTests)
	}
	if e.Contracts != nil {
		e.Contracts = copyComponentContracts(e.Contracts)
	}
	if e.ContractDiagnostics != nil {
		e.ContractDiagnostics = slices.Clone(e.ContractDiagnostics)
		sortContractDiagnostics(e.ContractDiagnostics)
	}
	return &e
}
