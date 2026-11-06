package plumbing

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func TestInvocationPartialStartupRetainsEvidence621(t *testing.T) {
	for _, base := range []bool{true, false} {
		t.Run(fmt.Sprintf("base=%v", base), func(t *testing.T) {
			f := newFixture515(t, "api", "web")
			noCommands621(t, f)
			want := "passed"
			if base {
				want = "failed"
			}
			f.runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
				// The next environment returns a handle alongside its startup
				// error. Both handles and both staging copies must be closed.
				f.runner.partialOpen = true
				return evidence515(in, "test", want, "completed before partial startup"), nil, nil
			}
			var result Result
			if base {
				checked := base515(t, f, t.Context())
				if checked.Problem == "" {
					t.Fatal("partial startup qualified the base")
				}
				result = checked.Run
			} else {
				var err error
				result, err = f.sandbox.Run(t.Context(), f.candidate, f.plan)
				if err == nil || result.Passed() {
					t.Fatal("partial startup passed the candidate")
				}
			}
			if len(f.runner.opens) != 2 || len(f.runner.invocations) != 1 || len(f.runner.closed) != 2 {
				t.Fatal("partial startup lost cleanup or admitted a later selector")
			}
			for i, test := range f.plan.Tests {
				status := "not_run"
				if i == 0 {
					status = want
				}
				if got := result515(t, result, test); got.Status != status || got.Reason == "" {
					t.Fatalf("startup lost completed or unfinished evidence: %+v", got)
				}
			}
			closed484(t, f.runner.runner484)
			for _, q := range f.runner.opens {
				if _, err := os.Stat(q.SourceDir); !os.IsNotExist(err) {
					t.Fatal("partial startup left private staging")
				}
			}
		})
	}
}
