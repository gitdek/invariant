package plumbing

import (
	"fmt"
	"strings"
	"testing"
)

// A passing run of a large package reaches thousands of tests, and its
// summary named every one: 689,793 characters for #399's build, more than a
// pull request or an agent's tool result holds (#408). A passing run counts
// its tests. One that didn't names every test that didn't pass first, then
// at most summaryNamed of the rest.
func TestSummaryNamesWhatDidntPassAndCountsTheRest(t *testing.T) {
	var results []TestResult
	for i := range 5000 {
		results = append(results, TestResult{Package: "example.com/big", Name: fmt.Sprintf("Test%04d", i), Phase: "test", Status: "passed", Shard: i%8 + 1})
	}
	results = append(results, TestResult{Package: "example.com/big", Name: "TestSkipped", Phase: "test", Status: "skipped", Shard: 1},
		TestResult{Package: "example.com/big", Name: "TestRatified", Phase: "accept", Status: "passed"})
	passing := Result{Vet: true, Tests: true, Accepted: map[string]bool{"big_accept_test.go:TestRatified": true}, TestResults: results}
	if !passing.Passed() {
		t.Fatal("the passing fixture didn't pass")
	}
	summary := passing.Summary()
	if !strings.HasPrefix(summary, "Everything passed") || strings.Contains(summary, "Test0001") || strings.Contains(summary, "TestRatified") {
		t.Errorf("a passing run's summary names its tests:\n%.2000s", summary)
	}
	for _, count := range []string{"test: 5000 passed, 1 skipped", "accept: 1 passed, 0 skipped"} {
		if !strings.Contains(summary, count) {
			t.Errorf("a passing run's summary lacks %q:\n%s", count, summary)
		}
	}
	if len(summary) > 1000 {
		t.Errorf("a passing run's summary has %d bytes", len(summary))
	}

	failing := passing
	failing.Tests = false
	failing.TestResults = append(append([]TestResult(nil), results[:2500]...),
		TestResult{Package: "example.com/big", Name: "TestBroken", Phase: "test", Status: "failed", Shard: 3},
		TestResult{Package: "example.com/big", Name: "TestHung", Phase: "test", Status: "running", Shard: 4})
	failing.TestResults = append(failing.TestResults, results[2500:]...)
	failing.TestResults = append(failing.TestResults, TestResult{Package: "example.com/big", Name: "TestQueued", Phase: "test", Status: "not_run"})
	summary = failing.Summary()
	first := strings.Index(summary, ": passed")
	for _, line := range []string{"test example.com/big/TestBroken: failed (shard 3)", "test example.com/big/TestHung: running (shard 4)", "test example.com/big/TestQueued: unstarted (not_run)"} {
		if at := strings.Index(summary, line); at < 0 || at > first {
			t.Errorf("a failing run's summary doesn't name %q before any passed test:\n%.3000s", line, summary)
		}
	}
	if named := strings.Count(summary, ": passed") + strings.Count(summary, ": skipped"); named != summaryNamed {
		t.Errorf("a failing run's summary names %d passed or skipped tests, want %d", named, summaryNamed)
	}
	if more := fmt.Sprintf("…and %d more passed or skipped", len(results)-summaryNamed); !strings.Contains(summary, more) {
		t.Errorf("a failing run's summary lacks %q:\n%s", more, summary[max(len(summary)-500, 0):])
	}
}
