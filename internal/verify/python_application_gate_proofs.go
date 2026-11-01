package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/project"
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

// pythonApplicationProofShape refuses, at admission, what the selected
// function's own source shows the gate can't prove in place: what the
// verifier input can't preserve of the function itself, a decorator, since
// conformance, which the gate needs beside the proof, binds only undecorated
// functions, Assume, and a contract that couldn't run as Nagini reads it
// (#359). What depends on the rest of the application,
// its bindings, its trusted contracts and its production sources, is refused
// once the session has staged it, before any test, conformance, backing test
// or Nagini runs.
func pythonApplicationProofShape(root string, target project.Component) error {
	source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(target.Source)))
	if err != nil {
		return err
	}
	d, err := analyzePythonProof(source, target.Symbol)
	if err != nil {
		return fmt.Errorf("in-place proof of %s: %w", target.ID, err)
	}
	switch {
	case d.Async:
		return fmt.Errorf("in-place proof of %s: unsupported: %s is an async function", target.ID, target.Symbol)
	case len(d.Unsupported) > 0:
		return fmt.Errorf("in-place proof of %s: unsupported: %s holds %s", target.ID, target.Symbol, strings.Join(d.Unsupported, ", "))
	case len(d.Decorators) > 0:
		return fmt.Errorf("in-place proof of %s: unsupported: %s is decorated with @%s, and conformance, which the gate needs beside the proof, binds only undecorated functions", target.ID, target.Symbol, d.Decorators[0])
	}
	if err := pythonProofContracts(d, target.Symbol); err != nil {
		return fmt.Errorf("in-place proof of %s: %w", target.ID, err)
	}
	return nil
}

// naginiStandInSource is Invariant's runtime stand-in for
// nagini_contracts.contracts, whose functions the analyzer reads to check
// that a contract's calls bind as Python runs them.
var naginiStandInSource = func() []byte {
	b, err := naginiRuntime.ReadFile("naginiruntime/nagini_contracts/contracts.py")
	if err != nil {
		panic(err)
	}
	return b
}()

// naginiStandIn is what Invariant's runtime stand-in for nagini_contracts
// exports, by its __all__, a tuple so nothing can add to it: the only Nagini
// names a selected function may read, since only these are bound where the
// application runs (#359).
var naginiStandIn = func() []string {
	all := regexp.MustCompile(`(?s)__all__ = \((.*?)\)`).FindSubmatch(naginiStandInSource)
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
// byte for byte, nothing else in that package, and no other
// nagini_contracts anywhere a change to the import path could load instead.
// Any other could bind names a proof relies on, as a min shadowing the
// builtin does, or run its contract calls with effects Nagini assumes away.
// The stand-in's bytes alone don't settle what it binds when the module
// runs: the scan for writes refuses production code that changes it, as it
// does the module (#359).
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
	paths := make([]string, 0, len(s.snapshot))
	for path := range s.snapshot {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if !strings.HasPrefix(path, "nagini_contracts/") {
			if parts := strings.Split(path, "/"); slices.Contains(parts[:len(parts)-1], "nagini_contracts") || parts[len(parts)-1] == "nagini_contracts.py" {
				return fmt.Errorf("the application ships another nagini_contracts at %s, which a change to the import path could load in place of Invariant's runtime stand-in: %s, and no other", path, remedy)
			}
			continue
		}
		hash, ok := want[path]
		switch {
		case !ok:
			return fmt.Errorf("the application's nagini_contracts holds %s, which Invariant's runtime stand-in doesn't: %s", path, remedy)
		case s.snapshot[path].hash != hash:
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
	var out []string
	for _, target := range pythonApplicationProofTargets(&e.Inventory) {
		out = append(out, PythonApplicationTargetProblems(e, target)...)
	}
	return out
}

// Unqualified is a selected in-place proof Nagini verified that still falls
// short of an effective guarantee, as an assumed contract that isn't tested,
// or no passing conformance on the same function, leaves it (#359). The
// gate marks its row so, and receipts don't count it as proved.
const Unqualified = "unqualified"

// qualifyPythonApplicationProofs marks each proved row whose proof falls
// short of an effective guarantee as Unqualified.
func (r *Report) qualifyPythonApplicationProofs() {
	if r.Components == nil || r.Application == nil {
		return
	}
	for i, row := range r.Components.Proofs {
		if row.Status == "proved" && len(PythonApplicationTargetProblems(r.Components, row.Target)) > 0 &&
			slices.Contains(pythonApplicationProofTargets(&r.Components.Inventory), row.Target) {
			r.Components.Proofs[i].Status = Unqualified
		}
	}
}

// PythonApplicationTargetProblems says why the proof selected on target falls
// short of an effective guarantee.
func PythonApplicationTargetProblems(e *ComponentEvidence, target string) []string {
	standing := map[string]ComponentContractEvidence{}
	for _, c := range e.Contracts {
		standing[c.Contract] = c
	}
	i := slices.IndexFunc(e.Proofs, func(row ComponentProof) bool { return row.Target == target })
	if i < 0 || e.Proofs[i].Result == nil {
		return []string{fmt.Sprintf("%s has no proof attempt", target)}
	}
	row := e.Proofs[i]
	var out []string
	if row.Status != "proved" && row.Status != Unqualified {
		why := slices.Clone(row.Result.Errors)
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
