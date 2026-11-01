package verify

import (
	"context"
	"fmt"
	"regexp"
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

// preparePythonApplicationProofs checks that the application ships
// Invariant's runtime stand-in for nagini_contracts, which every module that
// holds a selected function imports to state its contract, then builds every
// selected proof's verifier input, which refuses a selection the input can't
// preserve. The gate supplies no stand-in of its own, so it runs the
// application as the application runs.
func (s *PythonApplicationSession) preparePythonApplicationProofs(ctx context.Context, targets []string) error {
	if len(targets) == 0 {
		return nil
	}
	if err := s.shipsNaginiStandIn(); err != nil {
		return fmt.Errorf("%s selects an in-place proof, whose module imports nagini_contracts to state its contract: %w", targets[0], err)
	}
	for _, target := range targets {
		if _, err := s.ProofInput(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

// naginiStandIn is what Invariant's runtime stand-in for nagini_contracts
// exports, by its __all__: the only Nagini names a selected function may
// read, since only these are bound where the application runs (#359).
var naginiStandIn = func() []string {
	b, err := naginiRuntime.ReadFile("naginiruntime/nagini_contracts/contracts.py")
	if err != nil {
		panic(err)
	}
	all := regexp.MustCompile(`(?s)__all__ = \[(.*?)\]`).FindSubmatch(b)
	if all == nil {
		panic("the runtime stand-in for nagini_contracts has no __all__")
	}
	var names []string
	for _, m := range regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"`).FindAllSubmatch(all[1], -1) {
		names = append(names, string(m[1]))
	}
	return names
}()

// shipsNaginiStandIn checks that the application ships Invariant's runtime
// stand-in at its root, which the runner puts first on the import path,
// byte for byte, and nothing else in that package. Any other
// nagini_contracts could bind names a proof relies on, as a min shadowing
// the builtin does, or run its contract calls with effects Nagini assumes
// away.
func (s *PythonApplicationSession) shipsNaginiStandIn() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("Python application session is closed")
	}
	want := map[string]string{}
	for _, name := range []string{"__init__.py", "contracts.py"} {
		b, err := naginiRuntime.ReadFile("naginiruntime/nagini_contracts/" + name)
		if err != nil {
			return err
		}
		want["nagini_contracts/"+name] = pythonApplicationHash(b)
	}
	const remedy = "ship Invariant's runtime stand-in, the nagini_contracts package in internal/verify/naginiruntime, at the application's root, exactly as it is"
	for path, f := range s.snapshot {
		if !strings.HasPrefix(path, "nagini_contracts/") {
			continue
		}
		hash, ok := want[path]
		switch {
		case !ok:
			return fmt.Errorf("the application's nagini_contracts holds %s, which Invariant's runtime stand-in doesn't: %s", path, remedy)
		case f.hash != hash:
			return fmt.Errorf("the application's %s isn't Invariant's runtime stand-in: %s", path, remedy)
		}
	}
	for path := range want {
		if _, ok := s.snapshot[path]; !ok {
			return fmt.Errorf("the application doesn't ship %s: %s", path, remedy)
		}
	}
	return nil
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
