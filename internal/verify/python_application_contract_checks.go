package verify

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// PythonContractChecks is what checking a proved function's contract on the
// calls the gate recorded found (#838). It says nothing about a call no
// exploration made.
type PythonContractChecks struct {
	Target string `json:"target"`
	// Calls is how many calls of the function the gate recorded, replays
	// apart, Own how many of them the application's own code made, and
	// Tests how many were made while the application's tests ran (#841);
	// the rest were made inside an exploration's attempts.
	Calls int `json:"calls"`
	Own   int `json:"own"`
	Tests int `json:"tests"`
	// Clauses is how many checks settled, each a parameter's type, a clause
	// or the result's type on one call, and Findings each one a call didn't
	// meet or the gate couldn't evaluate on it.
	Clauses  int                         `json:"clauses"`
	Findings []PythonContractCallFinding `json:"findings,omitempty"`
}

// PythonContractCallFinding is one finding with the call it was found on,
// and how many recorded calls gave the same one.
type PythonContractCallFinding struct {
	PythonContractFinding
	Caller    string                   `json:"caller,omitempty"`
	Own       bool                     `json:"own"`
	Arguments map[string]any           `json:"arguments"`
	Result    any                      `json:"result"`
	After     map[string]any           `json:"after,omitempty"`
	Raised    *PythonApplicationRaised `json:"raised,omitempty"`
	// Test is the test the call was made during, for one a test run
	// recorded inside a test.
	Test  string `json:"test,omitempty"`
	Count int    `json:"count"`
}

// PythonApplicationContractChecks evaluates the contract of the function
// proved in place on target on every call of it the report's explorations
// recorded outside a replay, whoever made the call. It returns nil where
// the report holds no attempt with the contract the result names: nothing
// here was checked. A receipt calls it on the report it decoded, so what it
// says is derived again from the recorded calls and never read from a
// status.
func PythonApplicationContractChecks(e *ComponentEvidence, a *PythonApplicationReport, target string) *PythonContractChecks {
	if e == nil || a == nil {
		return nil
	}
	i := slices.IndexFunc(e.Proofs, func(row ComponentProof) bool { return row.Target == target })
	if i < 0 || e.Proofs[i].Result == nil || e.Proofs[i].Result.Tool == nil {
		return nil
	}
	pin := e.Proofs[i].Result.Tool.Pins[InPlaceInputPin]
	j := slices.IndexFunc(a.Proofs, func(p PythonApplicationProof) bool {
		return pin != "" && p.Result.Target == target && p.Input.Fingerprint == pin && p.Input.Contract != nil
	})
	if j < 0 {
		return nil
	}
	in := a.Proofs[j].Input
	proved := ""
	for _, f := range in.Sources {
		if f.Path == in.Target.Source {
			proved = f.SHA256
		}
	}
	index, err := indexComponentInventory(e.Inventory)
	if err != nil {
		return nil
	}
	out := &PythonContractChecks{Target: target}
	seen := map[string]int{}
	executions := slices.Clone(a.Executions)
	for _, larger := range a.Larger {
		if larger.Execution != nil {
			executions = append(executions, *larger.Execution)
		}
	}
	check := func(call PythonApplicationCall, own bool, test string) {
		if call.Target != target {
			return
		}
		out.Calls++
		if own {
			out.Own++
		}
		evaluated, findings := in.Contract.Check(call)
		if call.SourceSHA256 != proved || proved == "" {
			// The call ran other bytes than Nagini read.
			evaluated, findings = 0, []PythonContractFinding{{Clause: "the function Nagini proved", Verdict: "unresolved",
				Why: "the call was recorded against other source bytes than the proof read"}}
		}
		out.Clauses += evaluated
		for _, f := range findings {
			found := PythonContractCallFinding{PythonContractFinding: f, Caller: call.Caller, Own: own, Arguments: call.Arguments,
				Result: call.Result, After: call.After, Raised: call.Raised, Test: test, Count: 1}
			key := pythonApplicationKey(found)
			if at, ok := seen[key]; ok {
				out.Findings[at].Count++
				continue
			}
			seen[key] = len(out.Findings)
			out.Findings = append(out.Findings, found)
		}
	}
	for _, x := range executions {
		// A call counts as the application's own when one of its existing
		// sources made it, and never the driver or the test's own file.
		existing, checker := index.checks[x.Key.Target].Existing, index.declarations[x.Key.Check].Source
		for _, row := range x.Observations {
			if row.Replay == nil || *row.Replay {
				continue
			}
			for _, call := range row.Calls {
				check(call, pythonApplicationOwnCall(call, existing, checker), "")
			}
		}
		before := out.Calls
		for _, t := range x.Tests {
			for _, call := range t.ContractCalls {
				check(call, pythonApplicationOwnCall(call, existing, checker), t.ID)
			}
		}
		for _, call := range x.Calls {
			check(call, pythonApplicationOwnCall(call, existing, checker), "")
		}
		out.Tests += out.Calls - before
	}
	return out
}

// Evidence says whether the checks show the application's callers meet the
// contract on the calls exploration exercised: its own code made at least
// one call, and every clause evaluated on every recorded call held.
func (c *PythonContractChecks) Evidence() bool {
	return c != nil && c.Own > 0 && len(c.Findings) == 0
}

// problems says each finding as a reason the proof falls short, the first
// few in full and the rest counted.
func (c *PythonContractChecks) problems() []string {
	if c == nil {
		return nil
	}
	var out []string
	for i, f := range c.Findings {
		if i == 3 {
			out = append(out, fmt.Sprintf("%s: %d more recorded calls didn't meet its contract, or couldn't be checked against it", c.Target, len(c.Findings)-i))
			break
		}
		out = append(out, c.Target+": "+f.String())
	}
	return out
}

// String says what the call was and what it didn't meet.
func (f PythonContractCallFinding) String() string {
	who := "a call nothing in the application made"
	switch {
	case f.Own:
		who = "a call from " + f.Caller
	case f.Caller != "":
		who = "a call from " + f.Caller + ", which isn't the application's own code,"
	}
	names := make([]string, 0, len(f.Arguments))
	for name := range f.Arguments {
		names = append(names, name)
	}
	sort.Strings(names)
	args := make([]string, len(names))
	for i, name := range names {
		args[i] = name + "=" + pythonContractShow(pythonContractValue(f.Arguments[name]))
	}
	call := "(" + strings.Join(args, ", ") + ")"
	switch {
	case f.Raised != nil:
		call += " raising " + f.Raised.Type
	default:
		call += " returning " + pythonContractShow(pythonContractValue(f.Result))
	}
	what := "didn't meet " + f.Clause
	if f.Verdict == "unresolved" {
		what = "couldn't be checked against " + f.Clause
	}
	if f.Why != "" {
		what += ": " + f.Why
	}
	if f.Test != "" {
		who += " during " + f.Test
	}
	out := fmt.Sprintf("%s %s, with %s", who, what, call)
	if f.Count > 1 {
		out += fmt.Sprintf(" (%d such calls)", f.Count)
	}
	return out
}
