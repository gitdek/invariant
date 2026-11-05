package plumbing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

// A built-in Go run keeps its rule: a passing acceptance test may skip a
// subtest. Only configured acceptance needs every record to pass.
func TestBuiltInAcceptanceMaySkipASubtest(t *testing.T) {
	r := Result{Vet: true, Tests: true, Accepted: map[string]bool{"x/x_accept_test.go:TestX": true},
		TestResults: []TestResult{
			{Package: "x", Name: "TestX", File: "x/x_accept_test.go", Phase: "accept", Status: "passed"},
			{Package: "x", Name: "TestX/needs_docker", File: "x/x_accept_test.go", Phase: "accept", Status: "skipped"},
		}}
	if !r.Passed() || len(r.Failing()) != 0 {
		t.Fatalf("a skipped subtest failed a passing built-in acceptance test: passed=%v, failing=%v", r.Passed(), r.Failing())
	}
	r.CommandResults = []CommandResult{}
	if r.Passed() {
		t.Fatal("configured acceptance passed with a skipped record")
	}
}

// Configured evidence is matched by the complete identity, never by the
// display label, and a test no result names is still named as failing.
func TestConfiguredEvidenceIsMatchedByIdentity(t *testing.T) {
	ready := Test{Name: "Worker readiness", Part: "worker", Framework: "pytest", File: "worker/test_ready.py", Selector: "test_ready.py::test_ready"}
	other := ready
	other.Selector = "test_ready.py::test_other"
	result := func(t Test, name string) TestResult {
		return TestResult{Name: name, Part: t.Part, Framework: t.Framework, File: t.File, Selector: t.Selector, Phase: "accept", Status: "passed"}
	}
	if result(ready, "test_ready").identity() != ready.Identity() {
		t.Fatal("a result's runner title changed its identity")
	}
	r := Result{CommandResults: []CommandResult{}, Accepted: map[string]bool{ready.Identity(): false, other.Identity(): false},
		TestResults: []TestResult{result(ready, "test_ready")}}
	if r.Passed() {
		t.Fatal("a plan test with no result passed")
	}
	if got := r.Failing(); len(got) != 1 || got[0] != other.Identity() {
		t.Fatalf("Failing() = %q, want only the unreported %s", got, other.Identity())
	}
	r.TestResults = append(r.TestResults, result(other, "the runner's own title"))
	if !r.Passed() || len(r.Failing()) != 0 {
		t.Fatalf("results labeled unlike the plan weren't matched by identity: %q", r.Failing())
	}
}

// A configured check that ran reports its own error once.
func TestRepositoryCheckErrorsAreReportedOnce(t *testing.T) {
	plan := &Plan{Name: "legacy", Summary: "Shape only.", Files: []string{"a/a.go"}, Tests: []Test{{Name: "TestA", File: "a/a_accept_test.go", Says: "A works."}}}
	r, _ := Sandbox{Repository: &RepositoryChecks{}}.Run(context.Background(), t.TempDir(), plan)
	const want = "repository checks need a captured snapshot, plan and original checkout"
	if n := strings.Count(strings.Join(r.Problems, "\n"), want); n != 1 {
		t.Fatalf("the error appears %d times: %q", n, r.Problems)
	}
	if r.Passed() {
		t.Fatal("a configured check that couldn't run passed")
	}
}

// The built-in runner's failure is the factory's to report. It must not
// refuse a sound plan and spend one of the planning agent's checks.
func TestBuiltInCheckReturnsRunnerErrors(t *testing.T) {
	ws, repo := t.TempDir(), t.TempDir()
	write468(t, ws, "plan.json", `{"name":"n","summary":"s","files":["a/a.go"],"tests":[{"name":"TestA","file":"a/a_accept_test.go","says":"A works."}]}`)
	write468(t, ws, "tests/a/a_accept_test.go", "package a\nimport \"testing\"\nfunc TestA(t *testing.T) { t.Fatal(\"pending\") }\n")
	write468(t, repo, "a/a.go", "package a\n")
	t.Setenv("PATH", t.TempDir()) // no git and no docker
	c, err := Check(context.Background(), ws, repo, Sandbox{Image: "unavailable"})
	if err == nil || !strings.Contains(err.Error(), "docker") {
		t.Fatalf("the runner's failure became the plan's problem: %+v, %v", c, err)
	}
}

// Nothing infers a part for a legacy record, so a plan can't mix them.
func TestNativeAndLegacyRecordsDoNotMix(t *testing.T) {
	p := &Plan{Name: "mixed", Summary: "Both kinds.", Files: []string{"worker/main.py"},
		Tests: []Test{
			{Name: "ready", Part: "worker", Framework: "pytest", File: "worker/test_ready.py", Selector: "test_ready.py::test_ready", Says: "Ready."},
			{Name: "TestLegacy", File: "worker/legacy_accept_test.go", Says: "Legacy."},
		},
		Sources: map[string]string{
			"worker/test_ready.py":         "def test_ready():\n    assert False\n",
			"worker/legacy_accept_test.go": "package worker\nimport \"testing\"\nfunc TestLegacy(t *testing.T) {}\n",
		}}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "native or all legacy") {
		t.Fatalf("a mixed plan validated: %v", err)
	}
}

// A Python or JavaScript acceptance test is never a Go package's embedded
// asset, so it can't widen the Go regression allowance.
func TestOtherFrameworksDoNotWidenGoRegressions(t *testing.T) {
	base := []string{"web/server.go", "lib/x.py"}
	p := &Plan{Files: []string{"lib/x.py"}, Tests: []Test{{Name: "ready", Part: "web", Framework: "vitest", File: "web/__tests__/a.test.ts", Selector: "a works"}}}
	if got := p.GoPackages(base); len(got) != 0 {
		t.Fatalf("a Vitest acceptance test selected Go packages %v", got)
	}
	if got := p.RegressionFiles(base, []string{"web/new_test.go"}); len(got) != 0 {
		t.Fatalf("a Vitest acceptance test admitted Go regressions %v", got)
	}
	native := &Plan{Files: []string{"lib/x.py"}, Tests: []Test{{Name: "ready", Part: "web", Framework: "go", File: "web/a_accept_test.go", Selector: "TestReady"}}}
	if got := native.GoPackages(base); len(got) != 1 || got[0] != "web" {
		t.Fatalf("a native Go acceptance test selected %v, want its package", got)
	}
}

// Labels, parts and selectors can't hide or reorder what a person ratifies.
func TestNativeIdentityRefusesInvisibleCharacters(t *testing.T) {
	for _, text := range []string{"\u200b", "ready\u200b", "ready \u202eydaer", "ready\u2066", "\ufeffready", "ready\u2028now"} {
		for _, field := range []string{"name", "part", "selector"} {
			test := Test{Name: "ready", Part: "web", Framework: "vitest", File: "web/a.test.ts", Selector: "a works", Says: "Ready."}
			switch field {
			case "name":
				test.Name = text
			case "part":
				test.Part = text
			case "selector":
				test.Selector = text
			}
			if err := test.validateNative(); err == nil {
				t.Errorf("%s %q validated", field, text)
			}
		}
	}
	plain := Test{Name: "ready: the page [case one] — 100%", Part: "web", Framework: "vitest", File: "web/a.test.ts", Selector: "page › ready (case one)", Says: "Ready."}
	if err := plain.validateNative(); err != nil {
		t.Fatalf("spaces and punctuation were refused: %v", err)
	}
}

// The same native plan can be ratified again: the record is rewritten,
// while its acceptance sources keep their exclusive creation.
func TestNativeLockRecordCanBeRatifiedAgain(t *testing.T) {
	_, _, p := n503BindFixture(t)
	dir := t.TempDir()
	first := Lock{Plan: *p, Ratified: Ratification{By: "owner", At: "2026-11-03T00:00:00Z", Issue: 503, Comment: "https://example.invalid/1", Proposal: p.Hash()}}
	if err := first.Write(dir); err != nil {
		t.Fatal(err)
	}
	again := first
	again.Ratified.At, again.Ratified.Comment = "2026-11-04T00:00:00Z", "https://example.invalid/2"
	if err := again.Write(dir); err != nil {
		t.Fatalf("ratifying the same native plan again was refused: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(LockPath(503))))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := ReadLock(data)
	if err != nil || lock.Ratified.Comment != again.Ratified.Comment {
		t.Fatalf("the record wasn't the second ratification: %v", err)
	}
	for f := range p.Sources {
		write468(t, dir, f, "a substituted source\n")
	}
	if err := again.Write(dir); err == nil {
		t.Fatal("a ratified source was overwritten")
	}
}

// Only the parts running native tests bind their declared bytes: the
// repository's rules, documentation and other parts' dependencies don't.
func TestNativeAuthorityIgnoresDocsAndOtherParts(t *testing.T) {
	worker, web := n503Part("python", "worker"), n503Part("typescript", "web")
	root, _, snapshot := fixture484(t, []repository.Part{worker, web}, func(c *repository.Config) {
		c.AgentRules, c.CurrentState = []string{"AGENTS.md"}, []string{"STATE.md"}
	})
	p := n503Read(t, n503Plan(t, "pytest", "worker", "worker/test_accept.py", "test_accept.py::test_ready", "def test_ready():\n    assert False\n"), snapshot)
	ctx := context.Background()
	commit := func(file, data string) *repository.Snapshot {
		t.Helper()
		write468(t, root, file, data)
		git468(t, root, "add", "--all")
		git468(t, root, "commit", "-q", "--no-verify", "-m", "change "+file)
		next, err := repository.Load(ctx, root, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	for _, f := range append([]string{"AGENTS.md", "STATE.md"}, append(web.Lockfiles, web.Inputs...)...) {
		if err := p.ValidateRepository(ctx, commit(f, "changed "+f+"\n")); err != nil {
			t.Fatalf("a change to %s invalidated a worker plan: %v", f, err)
		}
	}
	if len(worker.Lockfiles) == 0 {
		t.Fatal("the worker fixture declares no lockfile")
	}
	if p.ValidateRepository(ctx, commit(worker.Lockfiles[0], "changed\n")) == nil {
		t.Fatal("a change to the worker's own lockfile kept its authority")
	}
}

// A repository that normalizes line endings stores a CRLF source as LF.
// Staging still accepts that checkout, and its tests run the ratified bytes.
func TestNativeStagingRunsRatifiedBytesUnderLineEndingNormalization(t *testing.T) {
	root, _, p := n503BindFixture(t)
	file := p.Tests[0].File
	if !strings.Contains(p.Sources[file], "\r\n") {
		t.Fatal("the fixture's source has no CRLF")
	}
	write468(t, root, ".gitattributes", "* text=auto\n")
	lock := Lock{Plan: *p, Ratified: Ratification{Issue: 503, Proposal: p.Hash()}}
	if err := lock.Write(root); err != nil {
		t.Fatal(err)
	}
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-q", "--no-verify", "-m", "ratify")
	checkout := t.TempDir()
	git468(t, checkout, "clone", "-q", root, ".")
	if data, err := os.ReadFile(filepath.Join(checkout, file)); err != nil || string(data) == p.Sources[file] {
		t.Fatalf("git didn't normalize the source, so this checks nothing: %v", err)
	}
	ws := t.TempDir()
	if err := CopyCheckout(context.Background(), checkout, ws); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := Stage(context.Background(), checkout, ws, p, dst); err != nil {
		t.Fatalf("a normalized source was refused: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, file)); err != nil || string(data) != p.Sources[file] {
		t.Fatalf("staging didn't run the ratified bytes: %q, %v", data, err)
	}
	if SameSource("assert True\r\n", p.Sources[file]) {
		t.Fatal("a different source counted as the ratified one")
	}
}

// Until an adapter exists, configured planning refuses before copying the
// checkout or preparing dependencies, even when the suite would pass.
func TestConfiguredPlanningRefusesBeforePreparation(t *testing.T) {
	root, _, snapshot := fixture484(t, []repository.Part{part484("go", "api")}, nil)
	file := "api/unproved_accept_test.go"
	p := &Plan{Name: "legacy shape", Summary: "Refuse cheaply.", Files: []string{"api/main.go"}, Tests: []Test{{Name: "TestUnproved", File: file, Says: "This test fails on the base."}}, Sources: map[string]string{file: "package fixture\nimport \"testing\"\nfunc TestUnproved(t *testing.T) { t.Fatal(\"pending\") }\n"}}
	runner := newRunner484(t)
	checked, err := Check(context.Background(), n503Draft(t, p), root, sandbox484(t, root, snapshot, runner))
	if err != nil || checked == nil || !strings.Contains(checked.Problem, "no individual execution adapter") {
		t.Fatalf("configured draft wasn't refused for its missing adapter: %+v, %v", checked, err)
	}
	if len(runner.installs) != 0 || len(runner.inspections) != 0 || len(runner.opens) != 0 || len(runner.calls) != 0 {
		t.Fatal("a refusal that was already certain prepared dependencies or ran commands")
	}
	if checked.Plan == nil || len(checked.Run.Failing()) != 1 {
		t.Fatalf("the refusal lost the plan or its unstarted acceptance: %+v", checked)
	}
}

// Every reader of a ratified plan applies the same check.
func TestRatifiedPlanCheckIsShared(t *testing.T) {
	_, _, p := n503BindFixture(t)
	missing := *p
	missing.Execution = nil
	for name, lock := range map[string]Lock{
		"no execution": {Plan: missing, Ratified: Ratification{Issue: 503, Proposal: missing.Hash()}},
		"another hash": {Plan: *p, Ratified: Ratification{Issue: 503, Proposal: "sha256:" + strings.Repeat("0", 64)}},
	} {
		if lock.Verify() == nil {
			t.Errorf("%s: Verify accepted", name)
		}
		if lock.Write(t.TempDir()) == nil {
			t.Errorf("%s: Write accepted", name)
		}
		data, err := json.Marshal(lock)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadLock(data); err == nil {
			t.Errorf("%s: ReadLock accepted", name)
		}
	}
	if Stage(context.Background(), t.TempDir(), t.TempDir(), &missing, t.TempDir()) == nil {
		t.Error("Stage accepted a native plan without captured execution")
	}
}
