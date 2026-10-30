package verify

import (
	"fmt"
	"slices"
	"sort"

	"github.com/gitdek/invariant/internal/project"
)

// ComponentProofResult records what a tool checked, using canonical component
// IDs and snapshots of the declarations supplied to it. Execution adapters
// must establish these facts from tool output, never from configured intent.
type ComponentProofResult struct {
	Target    string              `json:"target"`
	Verifier  string              `json:"verifier"`
	Passed    bool                `json:"passed"`
	Inputs    []project.Component `json:"inputs"`
	Verified  []string            `json:"verified"`
	Contracts []string            `json:"contracts"`
	Errors    []string            `json:"errors,omitempty"`
}

// ComponentEvidence keeps proof coverage separate from other evidence kinds.
// The inventory includes every scoped declaration, checked or not.
type ComponentEvidence struct {
	Inventory project.ComponentInventory `json:"inventory"`
	Proofs    []ComponentProof           `json:"proofs"`
}

type ComponentProof struct {
	Target string                `json:"target"`
	Status string                `json:"status"`
	Result *ComponentProofResult `json:"result,omitempty"`
}

// AttributeComponentProofs replaces the report's component evidence without
// running tools or changing any gate result. Invalid references or snapshots
// leave the report untouched; valid but incomplete attempts remain unchecked.
func (r *Report) AttributeComponentProofs(inv *project.ComponentInventory, results []ComponentProofResult) error {
	if r.ModelOnly || inv == nil || inv.Mode != "explicit" {
		return fmt.Errorf("component proofs require an explicit inventory and a code report")
	}
	components := make(map[string]project.Component, len(inv.Components))
	for _, c := range inv.Components {
		if c.ID == "" || c.Language != "go" {
			return fmt.Errorf("component proofs: invalid Go component %q", c.ID)
		}
		if _, ok := components[c.ID]; ok {
			return fmt.Errorf("component proofs: duplicate component %s", c.ID)
		}
		components[c.ID] = c
	}
	checks := make(map[string]project.ResolvedComponentCheck, len(inv.Checks))
	for _, check := range inv.Checks {
		if _, ok := components[check.Target]; !ok {
			return fmt.Errorf("component proofs: unknown check target %s", check.Target)
		}
		if _, ok := checks[check.Target]; ok {
			return fmt.Errorf("component proofs: duplicate check target %s", check.Target)
		}
		if p := check.Proof; p != nil {
			if len(p.Contracts) == 0 {
				return fmt.Errorf("component proofs: target %s selects no contracts", check.Target)
			}
			if err := componentReferences(components, p.Contracts, "selected contract"); err != nil {
				return err
			}
		}
		checks[check.Target] = check
	}
	proofs := make(map[string]ComponentProof, len(results))
	for _, result := range results {
		check, ok := checks[result.Target]
		if !ok || check.Proof == nil {
			return fmt.Errorf("component proofs: target %s has no selected proof", result.Target)
		}
		if _, ok := proofs[result.Target]; ok {
			return fmt.Errorf("component proofs: duplicate result for %s", result.Target)
		}
		if err := componentReferences(components, result.Verified, "verified"); err != nil {
			return err
		}
		if err := componentReferences(components, result.Contracts, "contract"); err != nil {
			return err
		}
		inputs := make(map[string]bool, len(result.Inputs))
		for _, input := range result.Inputs {
			c, ok := components[input.ID]
			if !ok || c != input {
				return fmt.Errorf("component proofs: input %s does not match the inventory", input.ID)
			}
			if inputs[input.ID] {
				return fmt.Errorf("component proofs: duplicate input %s", input.ID)
			}
			inputs[input.ID] = true
		}
		proved := result.Passed && result.Verifier != "" && len(result.Errors) == 0 && slices.Contains(result.Verified, result.Target)
		for _, id := range check.Proof.Contracts {
			proved = proved && slices.Contains(result.Contracts, id)
		}
		for _, ids := range [][]string{result.Verified, result.Contracts} {
			for _, id := range ids {
				proved = proved && inputs[id]
			}
		}
		status := "unchecked"
		if proved {
			status = "proved"
		}
		copy := canonicalComponentResult(result)
		proofs[result.Target] = ComponentProof{Target: result.Target, Status: status, Result: &copy}
	}

	evidence := &ComponentEvidence{Inventory: canonicalComponentInventory(*inv), Proofs: make([]ComponentProof, 0, len(components))}
	for _, c := range evidence.Inventory.Components {
		p, ok := proofs[c.ID]
		if !ok {
			p = ComponentProof{Target: c.ID, Status: "unchecked"}
		}
		evidence.Proofs = append(evidence.Proofs, p)
	}
	r.Components = evidence
	r.Assurance = r.CodeAssurance()
	r.Fingerprint = Fingerprint(*r)
	return nil
}

func componentReferences(components map[string]project.Component, ids []string, kind string) error {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if _, ok := components[id]; !ok {
			return fmt.Errorf("component proofs: unknown %s ID %s", kind, id)
		}
		if seen[id] {
			return fmt.Errorf("component proofs: duplicate %s ID %s", kind, id)
		}
		seen[id] = true
	}
	return nil
}

// Canonical copies own their nested slices. Ordering reference sets never
// changes the caller's evidence, including when computing a fingerprint.
func canonicalComponentInventory(inv project.ComponentInventory) project.ComponentInventory {
	inv.Scope = componentIDs(inv.Scope)
	inv.Files = slices.Clone(inv.Files)
	sort.Slice(inv.Files, func(i, j int) bool { return inv.Files[i].Path < inv.Files[j].Path })
	inv.Components = slices.Clone(inv.Components)
	sort.Slice(inv.Components, func(i, j int) bool { return inv.Components[i].ID < inv.Components[j].ID })
	inv.Checks = slices.Clone(inv.Checks)
	for i := range inv.Checks {
		c := &inv.Checks[i]
		if c.Proof != nil {
			c.Proof = &project.ResolvedComponentProof{Contracts: componentIDs(c.Proof.Contracts)}
		}
		c.Tests = componentIDs(c.Tests)
		c.Conformance = componentIDs(c.Conformance)
	}
	sort.Slice(inv.Checks, func(i, j int) bool { return inv.Checks[i].Target < inv.Checks[j].Target })
	return inv
}

func canonicalComponentResult(result ComponentProofResult) ComponentProofResult {
	result.Inputs = slices.Clone(result.Inputs)
	sort.Slice(result.Inputs, func(i, j int) bool { return result.Inputs[i].ID < result.Inputs[j].ID })
	result.Verified = componentIDs(result.Verified)
	result.Contracts = componentIDs(result.Contracts)
	result.Errors = slices.Clone(result.Errors)
	return result
}

func canonicalComponentEvidence(e ComponentEvidence) ComponentEvidence {
	e.Inventory = canonicalComponentInventory(e.Inventory)
	e.Proofs = slices.Clone(e.Proofs)
	for i := range e.Proofs {
		if e.Proofs[i].Result != nil {
			result := canonicalComponentResult(*e.Proofs[i].Result)
			e.Proofs[i].Result = &result
		}
	}
	sort.Slice(e.Proofs, func(i, j int) bool { return e.Proofs[i].Target < e.Proofs[j].Target })
	return e
}

func componentIDs(ids []string) []string {
	ids = slices.Clone(ids)
	sort.Strings(ids)
	return ids
}
