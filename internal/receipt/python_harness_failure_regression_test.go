package receipt

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

func TestPythonLargerUnfinishedExecutionIsNotAFailedClaim(t *testing.T) {
	r := &verify.Report{Passed: true, Application: &verify.PythonApplicationReport{
		Larger: []verify.PythonApplicationLarger{{
			Execution: &verify.PythonApplicationExecutionResult{ExitCode: -1, Diagnostics: []string{"context deadline exceeded"}},
			Result:    verify.Larger{Message: "the driver didn't finish exploring one size larger"},
		}},
	}}
	md := Markdown(r)
	if !strings.Contains(md, "didn't finish exploring one size larger") || !strings.Contains(md, "context deadline exceeded") {
		t.Fatal("unfinished larger execution lost its diagnostic context")
	}
	if strings.Contains(md, "Python execution one size larger, target") {
		t.Fatal("a larger run that claims nothing was listed as a failed claim")
	}
}
