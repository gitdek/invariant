package plumbing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

type acceptanceExitError515 int

func (e acceptanceExitError515) Error() string { return fmt.Sprintf("exit status %d", e) }
func (e acceptanceExitError515) ExitCode() int { return int(e) }

func TestAcceptanceBaseRequiresEveryCommandErrorCauseToBeAnExit(t *testing.T) {
	exit := acceptanceExitError515(1)
	for _, tc := range []struct {
		name      string
		err       error
		qualifies bool
	}{
		{"exit", exit, true},
		{"wrapped exit", fmt.Errorf("command: %w", exit), true},
		{"joined exits", errors.Join(exit, errors.New("exit status 2")), true},
		{"wrapped joined exits", fmt.Errorf("command: %w", errors.Join(exit, acceptanceExitError515(2))), true},
		{"exit and transport", errors.Join(exit, io.ErrUnexpectedEOF), false},
		{"transport and exit", errors.Join(io.ErrUnexpectedEOF, exit), false},
		{"wrapped joined transport", fmt.Errorf("command: %w", errors.Join(exit, fmt.Errorf("stream: %w", io.ErrUnexpectedEOF))), false},
		{"exit and startup", errors.Join(exit, acceptanceExitError515(125)), false},
	} {
		for _, phase := range []string{"build", "test"} {
			t.Run(tc.name+"/"+phase, func(t *testing.T) {
				f := newFixture515(t, "api", "web")
				allFail515(f.runner)
				f.runner.hook = func(_ context.Context, q DependencyRequest, argv []string) ([]byte, error) {
					if q.Directory == "web" && argv[1] == phase {
						return []byte("ordinary suite diagnostic"), tc.err
					}
					return nil, nil
				}
				checked := base515(t, f, context.Background())
				if (checked.Problem == "") != tc.qualifies || checked.Run.Passed() {
					t.Fatalf("wrong qualification for command error %v: %+v", tc.err, checked)
				}
				wantInvocations := len(f.plan.Tests)
				if !tc.qualifies {
					wantInvocations = 2
					if !strings.Contains(checked.Problem, tc.err.Error()) {
						t.Fatalf("infrastructure cause disappeared: %s", checked.Problem)
					}
				}
				if len(f.runner.invocations) != wantInvocations {
					t.Fatalf("ran %d selectors, want %d", len(f.runner.invocations), wantInvocations)
				}
				for _, test := range f.plan.Tests {
					r := result515(t, checked.Run, test)
					if test.Part == "api" || tc.qualifies {
						if r.Kind != "test" || r.Status != "failed" || r.Reason != "assertion failed" {
							t.Fatalf("lost independent completed failure: %+v", r)
						}
					} else if r.Status != "not_run" || !strings.Contains(r.Reason, tc.err.Error()) {
						t.Fatalf("lost unfinished work or infrastructure reason: %+v", r)
					}
				}
				closed484(t, f.runner.runner484)
			})
		}
	}
}

func TestAcceptanceNegativeExitRetainsUnfinishedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, kind, status, wantKind, wantStatus string
		edit                                     func(*RepositoryAcceptanceEvidence)
	}{
		{"running", "test", "running", "test", "running", nil},
		{"setup", "setup", "failed", "setup", "refused", nil},
		{"discovery", "setup", "failed", "setup", "refused", func(e *RepositoryAcceptanceEvidence) { e.Records = e.Records[1:] }},
		{"refused before selection", "test", "refused", "test", "refused", func(e *RepositoryAcceptanceEvidence) { e.Records = e.Records[1:] }},
		{"unstarted", "test", "running", "evidence", "not_run", func(e *RepositoryAcceptanceEvidence) { e.Records = nil }},
		{"selection only", "test", "running", "evidence", "not_run", func(e *RepositoryAcceptanceEvidence) { e.Records = e.Records[:1] }},
		{"test failure", "test", "failed", "evidence", "refused", nil},
		{"compiler failure", "compile", "failed", "evidence", "refused", nil},
		{"test pass", "test", "passed", "evidence", "refused", nil},
	} {
		for _, complete := range []bool{false, true} {
			for _, base := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/complete=%t/base=%t", tc.name, complete, base), func(t *testing.T) {
					f := newFixture515(t)
					reason := "specific interrupted " + tc.name + " explanation"
					firstStatus := "passed"
					if base {
						firstStatus = "failed"
					}
					f.runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
						if in.Binding.Selector == "TestFirst" {
							return evidence515(in, "test", firstStatus, "earlier completed assertion"), nil, nil
						}
						e := evidence515(in, tc.kind, tc.status, reason)
						e.ExitCode, e.Complete = -1, complete
						if tc.edit != nil {
							tc.edit(&e)
						}
						return e, []byte(strings.Repeat("clippable diagnostics\n", 2000)), nil
					}
					var result Result
					if base {
						checked := base515(t, f, context.Background())
						if checked.Problem == "" {
							t.Fatal("interrupted evidence qualified the base")
						}
						result = checked.Run
					} else {
						result, _ = f.sandbox.Run(context.Background(), f.candidate, f.plan)
					}
					if result.Passed() || len(result.Problems) == 0 || len(f.runner.invocations) != 2 {
						t.Fatalf("interrupted invocation did not veto the run: %s", result.Summary())
					}
					first := result515(t, result, f.plan.Tests[0])
					second := result515(t, result, f.plan.Tests[1])
					if first.Status != firstStatus || first.Kind != "test" || first.Reason != "earlier completed assertion" {
						t.Fatalf("interruption erased earlier completed evidence: %+v", first)
					}
					if second.Status != tc.wantStatus || second.Kind != tc.wantKind || second.Reason == "" {
						t.Fatalf("interruption lost the unfinished outcome: %+v", second)
					}
					if tc.name != "unstarted" && tc.name != "selection only" && !strings.Contains(second.Reason, reason) {
						t.Fatalf("interruption erased the supplied reason: %+v", second)
					}
					data, err := json.Marshal(result)
					if err != nil {
						t.Fatal(err)
					}
					var transported Result
					if err := json.Unmarshal(data, &transported); err != nil {
						t.Fatal(err)
					}
					transported.Output = transported.Tail(64)
					if transported.Passed() || result515(t, transported, f.plan.Tests[0]) != first || result515(t, transported, f.plan.Tests[1]) != second {
						t.Fatal("transport or clipping changed structured evidence")
					}
					for _, record := range []TestResult{first, second} {
						if !strings.Contains(transported.Summary(), record.identity()) || !strings.Contains(transported.Summary(), record.Reason) {
							t.Fatalf("clipped summary lost an identity or reason: %s", transported.Summary())
						}
					}
					closed484(t, f.runner.runner484)
				})
			}
		}
	}
}
