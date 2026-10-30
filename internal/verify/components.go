package verify

import (
	"fmt"
	"slices"
	"sort"

	"github.com/gitdek/invariant/internal/project"
)

// ComponentProofResult records a tool's proof of an exact target, with the
// declarations supplied to it and the contracts it checked. Execution adapters
// must establish these facts from tool output; configuration is not evidence.
type ComponentProofResult struct {
	Target    string              `json:"target"`
	Verifier  string              `json:"verifier"`
	Passed    bool                `json:"passed"`
	Inputs    []project.Component `json:"inputs"`
	Verified  []string            `json:"verified"`
	Contracts []string            `json:"contracts"`
	Errors    []string            `json:"errors,omitempty"`
}

// ComponentEvidence keeps the complete inventory and proof coverage separate
// from other kinds of evidence, which are not attributed to components yet.
type ComponentEvidence struct {
	Inventory project.ComponentInventory `json:"inventory"`
	Proofs    []ComponentProof           `json:"proofs"`
}

// ComponentProof is one component's proof status, including an unsuccessful
// attempt when there was one. A missing result leaves the component unchecked.
type ComponentProof struct {
	Target string                `json:"target"`
	Status string                `json:"status"`
	Result *ComponentProofResult `json:"result,omitempty"`
}

// AttributeComponentProofs replaces a report's component proof evidence. It
// runs no tools and changes no gate outcome or legacy check. Invalid evidence
// leaves the report untouched; valid but incomplete evidence proves nothing.
func (r *Report) AttributeComponentProofs(inv *project.ComponentInventory, results []ComponentProofResult) error {
	if inv == nil || inv.Mode != "explicit" {
		return fmt.Errorf("component proofs require an explicit inventory")
	}
	if r.ModelOnly {
		return fmt.Errorf("component proofs cannot be attributed to a model-only report")
	}
	evidence := &ComponentEvidence{Inventory: copyComponentInventory(*inv)}
	components := map[string]project.Component{}
	for _, c := range evidence.Inventory.Components {
		if _, ok := components[c.ID]; ok {
			return fmt.Errorf("component proofs: duplicate component %q", c.ID)
		}
		components[c.ID] = c
	}
	checks := map[string]project.ResolvedComponentCheck{}
	for _, check := range evidence.Inventory.Checks {
		if _, ok := components[check.Target]; !ok {
			return fmt.Errorf("component proofs: unknown check target %q", check.Target)
		}
		if _, ok := checks[check.Target]; ok {
			return fmt.Errorf("component proofs: duplicate check target %q", check.Target)
		}
		if check.Proof != nil {
			if len(check.Proof.Contracts) == 0 {
				return fmt.Errorf("component proofs: target %q selects no contracts", check.Target)
			}
			if err := componentProofReferences(components, check.Proof.Contracts, "selected contract"); err != nil {
				return err
			}
		}
		checks[check.Target] = check
	}
	byTarget := map[string]*ComponentProofResult{}
	for _, result := range results {
		check, ok := checks[result.Target]
		if !ok || check.Proof == nil {
			return fmt.Errorf("component proofs: target %q has no selected proof", result.Target)
		}
		if _, ok := byTarget[result.Target]; ok {
			return fmt.Errorf("component proofs: duplicate result for %q", result.Target)
		}
		if err := componentProofReferences(components, result.Verified, "verified"); err != nil {
			return err
		}
		if err := componentProofReferences(components, result.Contracts, "contract"); err != nil {
			return err
		}
		inputs := map[string]bool{}
		for _, input := range result.Inputs {
			c, ok := components[input.ID]
			if !ok {
				return fmt.Errorf("component proofs: unknown input %q", input.ID)
			}
			if inputs[input.ID] {
				return fmt.Errorf("component proofs: duplicate input %q", input.ID)
			}
			if input != c {
				return fmt.Errorf("component proofs: input %q does not match its inventory snapshot", input.ID)
			}
			inputs[input.ID] = true
		}
		copy := copyComponentProofResult(result)
		byTarget[result.Target] = &copy
	}
	evidence.Proofs = make([]ComponentProof, 0, len(components))
	for _, c := range evidence.Inventory.Components {
		row := ComponentProof{Target: c.ID, Status: "unchecked", Result: byTarget[c.ID]}
		if row.Result != nil && row.Result.covers(checks[c.ID].Proof.Contracts) {
			row.Status = "proved"
		}
		evidence.Proofs = append(evidence.Proofs, row)
	}
	next := *r
	next.Components, next.Assurance = evidence, "component evidence"
	next.Fingerprint = Fingerprint(next)
	*r = next
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
	if !r.Passed || r.Verifier == "" || len(r.Errors) > 0 || !slices.Contains(r.Verified, r.Target) {
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

// Copies own every nested slice and sort reference sets without changing the
// caller's inventory or results. Diagnostics retain the tool's ordering.
func copyComponentInventory(inv project.ComponentInventory) project.ComponentInventory {
	inv.Scope = slices.Clone(inv.Scope)
	sort.Strings(inv.Scope)
	inv.Files = slices.Clone(inv.Files)
	sort.Slice(inv.Files, func(i, j int) bool { return inv.Files[i].Path < inv.Files[j].Path })
	inv.Components = slices.Clone(inv.Components)
	sort.Slice(inv.Components, func(i, j int) bool { return inv.Components[i].ID < inv.Components[j].ID })
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
	}
	sort.Slice(inv.Checks, func(i, j int) bool { return inv.Checks[i].Target < inv.Checks[j].Target })
	return inv
}

func copyComponentProofResult(result ComponentProofResult) ComponentProofResult {
	result.Inputs = slices.Clone(result.Inputs)
	sort.Slice(result.Inputs, func(i, j int) bool { return result.Inputs[i].ID < result.Inputs[j].ID })
	result.Verified = slices.Clone(result.Verified)
	sort.Strings(result.Verified)
	result.Contracts = slices.Clone(result.Contracts)
	sort.Strings(result.Contracts)
	result.Errors = slices.Clone(result.Errors)
	return result
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
	return &e
}
