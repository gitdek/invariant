package verify

import (
	"encoding/json"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestLogicalConformanceFingerprintBindings(t *testing.T) {
	r := batchReport("steps-1", 5, tlc.Deadlock)
	r.Application.Conformance[0].LogicalChecks = []PythonApplicationLogicalCheck{{
		Module: "Invariant_Conformance", Batch: "steps-1", InputSHA256: "sha256:00",
		Config:  tlc.Config{Specification: "Invariant_CSpec", Constants: map[string]string{"N": "5"}},
		Outcome: tlc.Deadlock, Trace: []tlc.State{{Vars: []tlc.Var{{Name: "n", Value: "4"}}}},
	}}
	fingerprint := Fingerprint(r)
	wire, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	copyReport := func() Report {
		t.Helper()
		var copy Report
		if err := json.Unmarshal(wire, &copy); err != nil {
			t.Fatal(err)
		}
		return copy
	}
	for name, change := range map[string]func(*PythonApplicationLogicalCheck){
		"module":         func(c *PythonApplicationLogicalCheck) { c.Module = "Invariant_StrictAttempts" },
		"input":          func(c *PythonApplicationLogicalCheck) { c.InputSHA256 = "sha256:01" },
		"config":         func(c *PythonApplicationLogicalCheck) { c.Config.Constants["N"] = "6" },
		"outcome":        func(c *PythonApplicationLogicalCheck) { c.Outcome = tlc.Passed },
		"counterexample": func(c *PythonApplicationLogicalCheck) { c.Trace[0].Vars[0].Value = "3" },
		"error":          func(c *PythonApplicationLogicalCheck) { c.Error = "storage failed" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := copyReport()
			change(&copy.Application.Conformance[0].LogicalChecks[0])
			if Fingerprint(copy) == fingerprint {
				t.Fatal("logical evidence change did not affect fingerprint")
			}
		})
	}
	copy := copyReport()
	copy.Application.Conformance[0].Checks = nil
	copy.Application.Conformance[0].LogicalChecks[0].Batch = "another-scheduling-path"
	if Fingerprint(copy) != fingerprint {
		t.Fatal("physical windows or scheduling paths changed logical evidence")
	}
	if Fingerprint(r) != fingerprint || r.Application.Conformance[0].LogicalChecks[0].Batch != "steps-1" {
		t.Fatal("fingerprinting changed retained receipt evidence")
	}
}
