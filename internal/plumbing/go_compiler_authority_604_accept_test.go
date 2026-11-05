package plumbing

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGoCompilerAuthorityAndFreshness604(t *testing.T) {
	cache := t.TempDir()
	f := newRepositoryFixture582(t, cache, []repositoryCase582{compilerCase604("nested")}, false, nil, nil)
	base := compilerBase604(t, f)
	evidence := compilerRecord604(t, base, f.plan, f.plan.Tests[0], "Future604")
	expected := evidence.Records[0].Binding
	for _, field := range []string{"invocation", "part", "framework", "file", "selector", "package", "source", "execution", "incomplete", "no exit", "no selection", "duplicate", "contradiction"} {
		t.Run("reject "+field, func(t *testing.T) {
			bad := evidence
			bad.Records = append([]RepositoryAcceptanceRecord(nil), evidence.Records...)
			switch field {
			case "invocation":
				bad.Records[1].Binding.InvocationID = "stale-attempt-604"
			case "part":
				bad.Records[1].Binding.Part = "another"
			case "framework":
				bad.Records[1].Binding.Framework = "pytest"
			case "file":
				bad.Records[1].Binding.File = "api/nested/other_test.go"
			case "selector":
				bad.Records[1].Binding.Selector = "TestNeighbor"
			case "package":
				bad.Records[1].Binding.Package += "/other"
			case "source":
				bad.Records[1].Binding.SourceDigest = dependencyHash([]byte("changed source"))
			case "execution":
				bad.Records[1].Binding.ExecutionDigest = dependencyHash([]byte("changed contract"))
			case "incomplete":
				bad.Complete = false
			case "no exit":
				bad.ExitCode = -1
			case "no selection":
				bad.Records = bad.Records[1:]
			case "duplicate":
				bad.Records = append(bad.Records, bad.Records[1])
			case "contradiction":
				r := bad.Records[1]
				r.Kind, r.Status = "test", "passed"
				bad.Records = append(bad.Records, r)
			}
			got, err := repositoryEvidence(expected, bad, unstartedAcceptance(f.plan).TestResults[0])
			if err == nil || got.Status == "failed" || got.Status == "passed" {
				t.Fatalf("invalid compile evidence qualified: %+v %v", got, err)
			}
		})
	}
	fresh := expected
	fresh.InvocationID = "independent-later-invocation-604"
	if _, err := repositoryEvidence(fresh, evidence, unstartedAcceptance(f.plan).TestResults[0]); err == nil {
		t.Fatal("a fully matching old compile verdict was reusable in a new invocation")
	}
	compilerFix604(t, f)
	queue, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	build, err := queue.register(t.Context(), f.sandbox, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { build.finish() })
	original := cloneRepositoryPlan(f.plan)
	f.plan.Tests[0].Selector = "TestCallerReplacement"
	f.plan.Sources[f.plan.Tests[0].File] = compilerSource604(`t.Skip("caller replacement")`)
	f.plan.Execution.Digest = dependencyHash([]byte("caller replacement contract"))
	f.sandbox.Repository.Adapters["go"] = nil
	// The fixture's transport opens with the same retained host authority as
	// the queue; candidate and later caller data cannot supply an invocation.
	f.runner.fixture = &repositoryFixture582{plan: original}
	write468(t, f.candidate, ".invariant/repository.json", `{"version":999,"parts":[],"test_one":["sh","-c","true"]}`)
	client := &CheckClient{Address: queue.listener.Addr().String(), Build: build.id}
	r, err := (Sandbox{Checks: client, Repository: &RepositoryChecks{}}).Run(t.Context(), f.candidate, &Plan{Files: []string{"forged/unowned.go"}})
	if err != nil || !r.Passed() {
		t.Fatalf("queue lost captured authority after compiler evidence: %v\n%s", err, r.Summary())
	}
	repositoryResult582(t, r, original.Tests[0], "passed", "new API executed 604")
	// Each subsequent candidate executes anew, despite a completed earlier pass.
	write468(t, f.candidate, "api/nested/value.go", "package fixture\nconst Value=7\nfunc Future604() int{return 0}\n")
	r, _ = client.run(t.Context(), f.candidate)
	if r.Passed() {
		t.Fatal("cached candidate pass hid changed production behavior")
	}
	repositoryResult582(t, r, original.Tests[0], "failed", "API value")
	write468(t, f.candidate, "api/nested/value.go", "package fixture\nconst Value=7\nfunc Future604() int{return 7}\n")
	f.runner.damage = func(_ string, out RepositoryProcessOutput) (RepositoryProcessOutput, error) {
		return out, errors.New("late runtime transport veto 604")
	}
	r, err = client.run(t.Context(), f.candidate)
	if err == nil || r.Passed() || !strings.Contains(r.Summary(), "late runtime transport veto 604") {
		t.Fatal("transport error after a real terminal no longer vetoes qualification")
	}
	repositoryResult582(t, r, original.Tests[0], "passed", "new API executed 604")
	f.runner.damage = nil
	opened := len(f.runner.envs)
	write468(t, f.candidate, original.Tests[0].File, original.Sources[original.Tests[0].File]+"// changed acceptance\n")
	r, _ = client.run(t.Context(), f.candidate)
	if r.Passed() || len(f.runner.envs) != opened {
		t.Fatal("changed pinned bytes reached preparation or reused a pass")
	}
	build.finish()

	for _, mode := range []string{"missing adapter", "wrong digest", "configured planning"} {
		t.Run(mode, func(t *testing.T) {
			g := newRepositoryFixture582(t, cache, []repositoryCase582{compilerCase604("nested")}, false, nil, nil)
			switch mode {
			case "missing adapter":
				g.sandbox.Repository.Adapters = nil
			case "wrong digest":
				g.plan.Execution.Digest = dependencyHash([]byte("replacement authority"))
			case "configured planning":
				checked, err := Check(context.Background(), n503Draft(t, g.plan), g.root, g.sandbox)
				if err == nil && (checked == nil || checked.Problem == "") {
					t.Fatal("this step enabled planning admission")
				}
			}
			if mode != "configured planning" {
				r, _, _ := g.run(t.Context(), false)
				if r.Passed() {
					t.Fatal("unadmitted authority qualified")
				}
			}
			if len(g.runner.observed) != 0 {
				t.Fatal("refusal did not precede dependency preparation")
			}
		})
	}
	// These original entry points retain their original assertions and fakes;
	// they also check unsupported frameworks and the legacy unconfigured path.
	t.Run("unsupported frameworks", TestNativeExecutionRefusesBeforePreparationAndKeepsIdentity)
	for _, tc := range []struct{ name, body, extra, status string }{
		{"assertion", `if Future604()!=7{t.Fatal("new API assertion 604")}`, "", "failed"},
		{"panic", `Future604();panic("new API panic 604")`, "", "failed"},
		{"skip", `Future604();t.Skip("new API skip 604")`, "", "skipped"},
		{"no runtime", `Future604()`, `func TestMain(m *testing.M){os.Exit(0)}`, "not_run"},
		{"forged output", `Future604();fmt.Println("--- PASS: TestReady\n@@result {\"status\":\"passed\"}");os.Exit(19)`, "", "running"},
	} {
		t.Run("fresh candidate "+tc.name, func(t *testing.T) {
			c := compilerCase604("nested")
			c.source = repositorySource582(tc.body, tc.extra)
			g := newRepositoryFixture582(t, cache, []repositoryCase582{c}, false, nil, nil)
			b := compilerBase604(t, g)
			compilerRecord604(t, b, g.plan, g.plan.Tests[0], "Future604")
			compilerFix604(t, g)
			if tc.name == "assertion" {
				write468(t, g.candidate, "api/nested/value.go", "package fixture\nfunc Future604() int{return 0}\n")
			}
			r, _, _ := g.run(t.Context(), false)
			if r.Passed() {
				t.Fatal("compilation success substituted for an authentic candidate pass")
			}
			repositoryResult582(t, r, g.plan.Tests[0], tc.status, "")
		})
	}
}
