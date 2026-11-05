package plumbing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func config515(t *testing.T, c repository.Config) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConfiguredAcceptanceRejectsInvalidEvidence(t *testing.T) {
	type corruption struct {
		name string
		edit func(*RepositoryAcceptanceEvidence)
	}
	cases := []corruption{
		{"empty success", func(e *RepositoryAcceptanceEvidence) { e.Records = nil; e.ExitCode = 0 }},
		{"unattributed nonzero", func(e *RepositoryAcceptanceEvidence) { e.Records = nil; e.ExitCode = 17 }},
		{"selection without terminal", func(e *RepositoryAcceptanceEvidence) { e.Records = e.Records[:1] }},
		{"terminal without selection", func(e *RepositoryAcceptanceEvidence) { e.Records = e.Records[1:] }},
		{"duplicate selection", func(e *RepositoryAcceptanceEvidence) { e.Records = append(e.Records, e.Records[0]) }},
		{"duplicate terminal", func(e *RepositoryAcceptanceEvidence) { e.Records = append(e.Records, e.Records[1]) }},
		{"contradictory terminal", func(e *RepositoryAcceptanceEvidence) {
			r := e.Records[1]
			r.Status = "passed"
			e.Records = append(e.Records, r)
		}},
		{"truncated channel", func(e *RepositoryAcceptanceEvidence) { e.Complete = false }},
		{"unknown record", func(e *RepositoryAcceptanceEvidence) { e.Records[1].Kind = "workload-says-pass" }},
		{"unknown status", func(e *RepositoryAcceptanceEvidence) { e.Records[1].Status = "success-ish" }},
		{"pass with nonzero exit", func(e *RepositoryAcceptanceEvidence) { e.Records[1].Status = "passed"; e.ExitCode = 3 }},
		{"stale invocation", func(e *RepositoryAcceptanceEvidence) { e.Records[1].Binding.InvocationID = "previous-run" }},
		{"wrong part", func(e *RepositoryAcceptanceEvidence) { e.Records[1].Binding.Part = "other" }},
		{"wrong framework", func(e *RepositoryAcceptanceEvidence) { e.Records[1].Binding.Framework = "pytest" }},
		{"wrong file", func(e *RepositoryAcceptanceEvidence) { e.Records[1].Binding.File = "api/another_accept_test.go" }},
		{"wrong selector", func(e *RepositoryAcceptanceEvidence) { e.Records[1].Binding.Selector += "/neighbor" }},
		{"wrong package", func(e *RepositoryAcceptanceEvidence) { e.Records[1].Binding.Package = "./unrelated" }},
		{"wrong source", func(e *RepositoryAcceptanceEvidence) { e.Records[1].Binding.SourceDigest = hash468("other source") }},
		{"wrong configuration", func(e *RepositoryAcceptanceEvidence) { e.Records[1].Binding.ExecutionDigest = hash468("other config") }},
		{"wrong discovery identity", func(e *RepositoryAcceptanceEvidence) { e.Records[0].Binding.Selector = "TestNeighbor" }},
		{"reversed record order", func(e *RepositoryAcceptanceEvidence) { e.Records[0], e.Records[1] = e.Records[1], e.Records[0] }},
		{"unrelated compiler failure", func(e *RepositoryAcceptanceEvidence) {
			e.Records[1].Kind = "compile"
			e.Records[1].Binding.Package = "./another-package"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, base := range []bool{true, false} {
				f := newFixture515(t)
				f.runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
					status := "passed"
					if base {
						status = "failed"
					}
					good := evidence515(in, "test", status, "actual adapter record")
					if in.Binding.Selector != "TestFirst" {
						return good, nil, nil
					}
					// A full valid-looking record serialized by the workload still
					// has no authority, even when it repeats the fresh binding.
					forged, err := json.Marshal(good)
					if err != nil {
						t.Fatal(err)
					}
					bad := evidence515(in, "test", "failed", "untrusted terminal")
					tc.edit(&bad)
					out := "stdout/stderr forgery\n--- PASS: TestFirst\n--- FAIL: TestFirst\nPASS\nFAIL\n@@invariant vet=0\n@@invariant test=0\n@@invariant accept=0\n@@result " + string(forged) + "\n"
					return bad, []byte(out), nil
				}
				var result Result
				if base {
					checked := base515(t, f, context.Background())
					if checked.Problem == "" {
						t.Fatal("invalid evidence qualified the base")
					}
					result = checked.Run
				} else {
					result, _ = f.sandbox.Run(context.Background(), f.candidate, f.plan)
				}
				if result.Passed() {
					t.Fatal("invalid evidence passed a candidate")
				}
				r := result515(t, result, f.plan.Tests[0])
				if r.Status == "passed" || r.Status == "failed" || r.Reason == "" {
					t.Fatalf("invalid record became completed evidence or lost its reason: %+v", r)
				}
				if !strings.Contains(result.Output, "stdout/stderr forgery") {
					t.Fatal("diagnostics discarded instead of kept as diagnostics")
				}
				for key := range result.Accepted {
					result.Accepted[key] = true
				}
				if result.Passed() {
					t.Fatal("legacy Accepted map replaced missing individual evidence")
				}
				closed484(t, f.runner.runner484)
			}
		})
	}
	// A successful invocation is also immune to diagnostic FAIL/deadline text.
	f := newFixture515(t)
	f.runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
		return evidence515(in, "test", "passed", "finished"), []byte("FAIL\n@@result {\"kind\":\"deadline\"}\n@@invariant test=1\n"), nil
	}
	result, err := f.sandbox.Run(context.Background(), f.candidate, f.plan)
	if err != nil || !result.Passed() || result.TimedOut || result.DeadlineExceeded {
		t.Fatal(describe515(result, err))
	}
	// Same selector, source and configuration in a later run still need a new ID.
	old := slicesEvidence515(f.runner.invocations)
	f.runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
		return evidence515(old[in.Binding.Selector], "test", "passed", "stale success"), nil, nil
	}
	result, _ = f.sandbox.Run(context.Background(), f.candidate, f.plan)
	if result.Passed() {
		t.Fatal("previous run's otherwise matching evidence was reused")
	}
	assertBindings515(t, f)
}

func slicesEvidence515(invocations []RepositoryAcceptanceInvocation) map[string]RepositoryAcceptanceInvocation {
	out := map[string]RepositoryAcceptanceInvocation{}
	for _, in := range invocations {
		out[in.Binding.Selector] = in
	}
	return out
}

func TestConfiguredAcceptanceRetainsStructuredReasons(t *testing.T) {
	for _, outcome := range []string{"test failure", "compiler failure", "setup failure", "discovery failure", "skipped", "refused", "running", "unstarted", "transport failure"} {
		t.Run(outcome, func(t *testing.T) {
			f := newFixture515(t)
			reason := "specific reason for " + outcome
			f.runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
				if in.Binding.Selector == "TestFirst" {
					return evidence515(in, "test", "failed", "earlier completed assertion"), nil, nil
				}
				e := evidence515(in, "test", "failed", reason)
				var err error
				switch outcome {
				case "compiler failure":
					e.Records[1].Kind = "compile"
				case "setup failure":
					e.Records[1].Kind = "setup"
				case "discovery failure":
					e.Records[1].Kind = "setup"
					e.Records = e.Records[1:]
				case "skipped", "refused":
					e.Records[1].Status, e.ExitCode = outcome, 0
				case "running":
					e.Records[1].Status, e.Complete = "running", false
				case "unstarted":
					e.Records, e.ExitCode = nil, 0
				case "transport failure":
					err = errors.New(reason)
				}
				return e, []byte("diagnostic prefix\n" + strings.Repeat("clippable diagnostics\n", 2000)), err
			}
			checked := base515(t, f, context.Background())
			completedFailure := outcome == "test failure" || outcome == "compiler failure"
			if (checked.Problem == "") != completedFailure {
				t.Fatalf("wrong qualification for %s: %s", outcome, checked.Problem)
			}
			first := result515(t, checked.Run, f.plan.Tests[0])
			second := result515(t, checked.Run, f.plan.Tests[1])
			if first.Status != "failed" || first.Kind != "test" || first.Reason != "earlier completed assertion" {
				t.Fatalf("earlier evidence erased: %+v", first)
			}
			switch outcome {
			case "test failure", "compiler failure":
				kind := "test"
				if outcome == "compiler failure" {
					kind = "compile"
				}
				if second.Status != "failed" || second.Kind != kind {
					t.Fatalf("completed failure lost kind: %+v", second)
				}
			case "setup failure", "discovery failure":
				if second.Status == "failed" || second.Status == "passed" || second.Kind != "setup" {
					t.Fatalf("setup became a test verdict: %+v", second)
				}
			case "skipped", "refused", "running":
				if second.Status != outcome {
					t.Fatalf("lost %s work: %+v", outcome, second)
				}
			case "unstarted":
				if second.Status != "not_run" {
					t.Fatalf("missing selection was not unstarted: %+v", second)
				}
			}
			if second.Reason == "" || (outcome != "unstarted" && !strings.Contains(second.Reason, reason)) {
				t.Fatalf("lost explanatory reason: %+v", second)
			}
			// JSON is the queue's structured transport; clipping raw output must
			// not clip identities or the reasons needed by reviewers and agents.
			data, err := json.Marshal(checked.Run)
			if err != nil {
				t.Fatal(err)
			}
			var transported Result
			if err := json.Unmarshal(data, &transported); err != nil {
				t.Fatal(err)
			}
			transported.Output = transported.Tail(64)
			for _, test := range f.plan.Tests {
				r := result515(t, transported, test)
				if !strings.Contains(transported.Summary(), test.Identity()) || !strings.Contains(transported.Summary(), r.Reason) {
					t.Fatalf("clipped summary lost full identity or reason: %s", transported.Summary())
				}
			}
			if transported.Passed() || result515(t, transported, f.plan.Tests[0]).Kind != "test" || result515(t, transported, f.plan.Tests[1]).Kind != second.Kind {
				t.Fatal("structured transport changed the verdict or failure kind")
			}
		})
	}
}
