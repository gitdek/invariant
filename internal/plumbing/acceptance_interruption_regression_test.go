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

func TestAcceptanceBaseContinuesOnlyAfterOrdinaryDiscoveryExits(t *testing.T) {
	cache := t.TempDir()
	exit := acceptanceExitError515(1)
	for _, tc := range []struct {
		name      string
		err       error
		continues bool
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
		t.Run(tc.name, func(t *testing.T) {
			first := repositoryControl582()
			second, third := first, first
			second.part, third.part = "web", "worker"
			f := newRepositoryFixture582(t, cache, []repositoryCase582{first, second, third}, false, nil, nil)
			f.runner.before = func(_ context.Context, stage, part string, _ []string) error {
				if part == "web" && stage == "discovery" {
					return tc.err
				}
				return nil
			}
			result, problem, _ := f.run(t.Context(), true)
			if problem == "" || result.Passed() || !strings.Contains(problem, tc.err.Error()) {
				t.Fatalf("discovery error lost its refusal: %s\n%s", problem, result.Summary())
			}
			repositoryResult582(t, result, f.plan.Tests[0], "failed", "needs seven")
			refused := result515(t, result, f.plan.Tests[1])
			if refused.Kind != "setup" || refused.Status != "refused" || !strings.Contains(refused.Reason, tc.err.Error()) {
				t.Fatalf("discovery error became test evidence: %+v", refused)
			}
			want, reason := "not_run", tc.err.Error()
			if tc.continues {
				want, reason = "failed", "needs seven"
			}
			repositoryResult582(t, result, f.plan.Tests[2], want, reason)
			starts := 0
			for _, observation := range f.runner.observed {
				if observation.stage == "start" {
					starts++
				}
			}
			wantStarts := 1
			if tc.continues {
				wantStarts++
			}
			if starts != wantStarts {
				t.Fatalf("ran %d selectors, want %d", starts, wantStarts)
			}
			repositoryRoundTrip582(t, result)
		})
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
