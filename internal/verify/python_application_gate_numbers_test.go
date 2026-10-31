package verify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/tlc"
)

func TestPythonApplicationGateNumberRoundTrip(t *testing.T) {
	r := Report{Application: &PythonApplicationReport{Executions: []PythonApplicationExecutionResult{{
		Observations: []PythonApplicationObservation{{State: map[string]any{"n": json.Number("9007199254740993")}}},
	}}}}
	r.Fingerprint = Fingerprint(r)
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if Fingerprint(decoded) != r.Fingerprint {
		t.Fatal("report rounded an observed integer")
	}
	decoded.Application.Executions[0].Observations[0].State["n"] = json.Number("9007199254740992")
	if Fingerprint(decoded) == r.Fingerprint {
		t.Fatal("distinct observations share a fingerprint")
	}
}

type applicationBatchFailure struct{}

func (applicationBatchFailure) Check(_ context.Context, _, module string, _ tlc.Config) (tlc.Result, error) {
	if module == "Invariant_ConformanceInit" {
		return tlc.Result{Outcome: tlc.Passed}, nil
	}
	return tlc.Result{Outcome: tlc.Deadlock, Trace: []tlc.State{{Vars: []tlc.Var{{Name: "n", Value: "0"}}}}}, nil
}

func TestPythonApplicationGateFailedBatchesDeterministic(t *testing.T) {
	var trace []map[string]any
	for i := 0; i < 4002; i++ {
		trace = append(trace, map[string]any{"n": i})
	}
	raw, err := json.Marshal(conformance.Traces{Traces: [][]map[string]any{trace}})
	if err != nil {
		t.Fatal(err)
	}
	var first string
	for repeat := 0; repeat < 4; repeat++ {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "Counter.tla"), []byte("---- MODULE Counter ----\nVARIABLE n\nvars == <<n>>\nInit == n = 0\nNext == n' = n+1\n====\n"), 0600); err != nil {
			t.Fatal(err)
		}
		runner := &pythonApplicationModelRunner{root: dir, runner: applicationBatchFailure{}}
		result, err := conformance.Check(context.Background(), runner, dir, "Counter", nil, []string{"n"}, 4002, raw, nil)
		if err != nil || result.Passed {
			t.Fatalf("failed batch: %+v %v", result, err)
		}
		if len(runner.results) != 2 {
			t.Fatalf("schedule-dependent TLC invocations: %+v", runner.results)
		}
		fingerprint := pythonApplicationFingerprint(runner.results)
		if repeat > 0 && first != fingerprint {
			t.Fatal("failed batches changed portable evidence")
		}
		first = fingerprint
	}
}
