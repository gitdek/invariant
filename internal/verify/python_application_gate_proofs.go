package verify

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// The gate proves selected Python functions in place (#359, #294's step 6).
// It builds each selected function's verifier input before anything runs,
// runs the pinned Nagini on it after the application's tests, conformance
// and backing tests, and attributes the attempt as component proof
// evidence. A proof qualifies only as an effective guarantee: proved from
// the function's original bytes with nothing it reached unaccounted for,
// every trusted contract it assumed tested, and conformance on the same
// function, in the same session, observing calls into that very
// declaration. The proof is conditional on those trusted contracts, which
// tests support and nothing proves.

// preparePythonApplicationProofs builds every selected proof's verifier
// input, which refuses a selection the input can't preserve, and checks
// that the application provides the contracts module its selected functions'
// modules import. The gate supplies no stand-in for nagini_contracts, so it
// runs the application as the application runs.
func (s *PythonApplicationSession) preparePythonApplicationProofs(ctx context.Context, targets []string) error {
	contracts := ""
	for _, target := range targets {
		in, err := s.ProofInput(ctx, target)
		if err != nil {
			return err
		}
		if contracts == "" && slices.ContainsFunc(in.Boundaries, func(b PythonApplicationProofBoundary) bool { return b.Kind == "nagini" }) {
			contracts = target
		}
	}
	if contracts == "" {
		return nil
	}
	ok, err := s.providesNaginiContracts(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s selects an in-place proof, and its module imports nagini_contracts to state its contract, which the application doesn't provide: put Invariant's runtime stand-in, a nagini_contracts package with contracts.py, at the application's root, or install a distribution that provides nagini_contracts, so the module imports wherever the application runs", contracts)
	}
	return nil
}

// providesNaginiContracts says whether the application provides
// nagini_contracts.contracts: as a package at its root, which the runner
// puts first on the import path, or installed in its dependency image.
func (s *PythonApplicationSession) providesNaginiContracts(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, fmt.Errorf("Python application session is closed")
	}
	if _, ok := s.snapshot["nagini_contracts/contracts.py"]; ok {
		return true, nil
	}
	modules, err := s.moduleDistributions(ctx)
	if err != nil {
		return false, err
	}
	_, ok := modules["nagini_contracts"]
	return ok, nil
}

// pythonApplicationProofProblems says why each selected proof falls short of
// an effective guarantee; none means every one qualifies.
func (r *Report) pythonApplicationProofProblems() []string {
	if r.Components == nil || r.Application == nil {
		return nil
	}
	return PythonApplicationProofProblems(r.Components)
}

// PythonApplicationProofProblems says why each proof e's inventory selects
// falls short of an effective guarantee, in the inventory's order, from e's
// statuses as they stand; none means every one qualifies. A receipt passes
// evidence it has qualified again, so an edited status can't pass here.
func PythonApplicationProofProblems(e *ComponentEvidence) []string {
	standing := map[string]ComponentContractEvidence{}
	for _, c := range e.Contracts {
		standing[c.Contract] = c
	}
	rows := map[string]ComponentProof{}
	for _, row := range e.Proofs {
		rows[row.Target] = row
	}
	var out []string
	for _, target := range pythonApplicationProofTargets(&e.Inventory) {
		row, ok := rows[target]
		if !ok || row.Result == nil {
			out = append(out, fmt.Sprintf("%s has no proof attempt", target))
			continue
		}
		if row.Status != "proved" {
			var why []string
			why = append(why, row.Result.Errors...)
			for _, u := range row.Result.Unaccounted {
				if u.Kind != MissingContract || u.Operation == nil {
					why = append(why, unaccountedText(u))
				}
			}
			if len(why) == 0 {
				why = []string{"Nagini didn't verify it"}
			}
			out = append(out, fmt.Sprintf("%s isn't proved: %s", target, strings.Join(why, "; ")))
		}
		for _, use := range row.Result.Assumed {
			if standing[use.Contract].Support != "tested" {
				out = append(out, fmt.Sprintf("%s assumes trusted contract %s, which isn't tested, so it can't support the proof", target, use.Contract))
			}
		}
		if !pythonApplicationProofExercised(e, target, row.Result) {
			out = append(out, fmt.Sprintf("%s has no passing conformance on the same declaration, so nothing shows the application runs the function Nagini proved", target))
		}
	}
	return out
}

// pythonApplicationProofExercised says whether conformance the gate ran on
// target passed against the same source bytes the proof's input copies: the
// collector binds conformance's observed calls to the selected staged
// declaration, so the implementation proved is the one exercised.
func pythonApplicationProofExercised(e *ComponentEvidence, target string, proof *ComponentProofResult) bool {
	source := ""
	for _, c := range proof.Inputs {
		if c.ID == target {
			source = c.Source
		}
	}
	hash := ""
	for _, f := range proof.FileInputs {
		if f.Path == source {
			hash = f.SHA256
		}
	}
	if source == "" || hash == "" {
		return false
	}
	for _, row := range e.Conformance {
		if row.Target != target || row.Status != "tested" || row.Origin != "gate" {
			continue
		}
		for _, f := range row.Result.FileInputs {
			if f.Path == source && f.SHA256 == hash {
				return true
			}
		}
	}
	return false
}
