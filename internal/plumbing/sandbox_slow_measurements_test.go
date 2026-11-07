package plumbing_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
)

func TestSandboxBudgetSlowMeasurements(t *testing.T) {
	var result plumbing.Result
	for i := range 25 {
		elapsed := float64(i)
		result.TestResults = append(result.TestResults, plumbing.TestResult{
			Package: "fixture/work", Name: fmt.Sprintf("Test%d", i), Phase: "test",
			Status: "passed", ElapsedSeconds: &elapsed,
		})
	}
	for _, row := range []plumbing.TestResult{
		{Package: "fixture/work", Name: "TestSlow/child", Phase: "test", Status: "passed"},
		{Package: "fixture/work", Name: "TestRunning", Phase: "test", Status: "running"},
		{Package: "fixture/work", Name: "TestPending", Phase: "test", Status: "not_run"},
	} {
		elapsed := 1000.0
		row.ElapsedSeconds = &elapsed
		result.TestResults = append(result.TestResults, row)
	}
	result.TestResults = append(result.TestResults, plumbing.TestResult{Name: "TestAbsent", Status: "passed"})
	for _, elapsed := range []float64{-1, math.NaN(), math.Inf(1)} {
		result.TestResults = append(result.TestResults, plumbing.TestResult{Name: "TestInvalid", Status: "passed", ElapsedSeconds: &elapsed})
	}
	elapsed := 24.0
	result.TestResults = append(result.TestResults, plumbing.TestResult{
		Package: "fixture/contract", Name: "Test24", Phase: "accept", Status: "failed", ElapsedSeconds: &elapsed,
	})
	tests := sandboxBudgetSlowTests(result)
	if len(tests) != 20 || tests[0].Package != "fixture/contract" || tests[0].Phase != "accept" || tests[1].Package != "fixture/work" || tests[1].Name != "Test24" {
		t.Fatalf("lost bound, measured order or package/phase identities: %+v", tests)
	}
	for i, test := range tests {
		want := float64(25 - i)
		if i == 0 {
			want = 24
		}
		if *test.ElapsedSeconds != want {
			t.Errorf("rank %d: %+v elapsed %v, want %v", i, test, *test.ElapsedSeconds, want)
		}
	}
	if result.TestResults[0].Name != "Test0" {
		t.Fatal("logging reordered the original results")
	}
}
