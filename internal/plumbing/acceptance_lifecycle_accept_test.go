package plumbing

// Issue #515's trusted, lower-level contract (no production framework adapter):
//
// RepositoryChecks gains Adapters map[string]RepositoryAcceptanceAdapter.
// A RepositoryAcceptanceAdapter has Select(context.Context, repository.Part,
// Test) (RepositoryAcceptanceSelection, error). Selection contains Package
// string and Argv []string. It validates the captured invocation form and
// supplies framework-specific selection, never candidate execution policy.
// The host completes all selections before preparing any dependency.
//
// An optional RepositoryAcceptanceEnvironment adds RunAcceptance(ctx,
// RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte,
// error) to RepositoryCheckEnvironment. The byte slice is diagnostics only;
// the error is an execution/infrastructure error, not a test verdict. Existing
// Run/Close-only environments remain usable for suites and explicitly refuse
// individual acceptance. Go now has a default adapter; there is no output parser.
//
// RepositoryAcceptanceInvocation has Binding RepositoryAcceptanceBinding,
// Argv []string and Source string. Binding is a comparable value containing
// InvocationID, Part, Framework, File, Selector, Package, SourceDigest and
// ExecutionDigest strings. Digests use sha256:<hex>; SourceDigest hashes the
// verbatim ratified source and ExecutionDigest is the captured plan digest.
// InvocationID is fresh for every attempt, including across runs. The host
// keeps an independent expected binding, not mutable environment-owned data.
//
// RepositoryAcceptanceEvidence contains Complete bool, ExitCode int and
// Records []RepositoryAcceptanceRecord. Each record has Binding and string
// Kind, Status, Reason. Exactly one Kind="selection" record identifies the
// selection (empty Status), followed by one terminal: Kind="test" with
// passed/failed/skipped/refused, Kind="compile" with failed, or Kind="setup"
// with failed. A test/running record is nonterminal. Complete asserts the
// trusted channel closed normally; it does not make missing records evidence.
// Only complete attributable test/failed or compile/failed can qualify a base.
// Setup or refusal records without a selection retain their explanation but
// cannot establish a completed test verdict. Framework-specific completion and
// compiler attribution come only from this trusted boundary, never this layer.
// TestResult gains Kind to retain test/compile/setup/evidence distinctions;
// setup failures and malformed evidence never project to Status="failed".
// Missing/unstarted, running, skipped and refused work is explicitly retained.
// A nonzero exit is compatible with attributable test/compile failure, never
// an individual pass; transport errors veto the run even with valid records.
//
// Sandbox.checkRepositoryBase(ctx, root, plan) (*Checked, error) is a distinct
// internal base path: it stages a private copy and collects all independent
// selectors after expected build/test failures while the environment is usable.
// Checked.Problem is empty only when every selector completed with attributable
// failure and no infrastructure/allowance/cancellation/cleanup veto occurred.
// Sandbox.Run is the candidate path for an explicitly host-configured adapter;
// it requires all applicable commands plus all acceptance selectors to pass.
// Both paths and public Check share validation, staging, evidence, deadlines
// and cleanup. Explicit trusted adapters still override the Go default.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

type adapter515 struct {
	selectHook func(context.Context, repository.Part, Test) (RepositoryAcceptanceSelection, error)
}

func (a *adapter515) Select(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
	if a.selectHook != nil {
		return a.selectHook(ctx, part, test)
	}
	argv := slices.Clone(part.Commands["test_one"].Argv)
	if len(argv) != 3 || argv[0] != "trusted-fixture" || argv[1] != "{test}" {
		return RepositoryAcceptanceSelection{}, errors.New("unsupported fixture invocation form")
	}
	argv[1] = test.Selector
	return RepositoryAcceptanceSelection{Package: "./" + part.Directory, Argv: argv}, ctx.Err()
}

type runner515 struct {
	*runner484
	invocations []RepositoryAcceptanceInvocation
	noEvidence  bool
	partialOpen bool
	openHook    func(context.Context, DependencyRequest) error
	accept      func(context.Context, DependencyRequest, RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error)
}

func (r *runner515) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	environment, err := r.runner484.OpenChecks(ctx, q)
	if err != nil {
		return environment, err
	}
	// Startup can fail after acquiring a handle. Keep it caller-owned so
	// timeout fixtures exercise bounded cleanup of the attempted invocation.
	if r.openHook != nil {
		if err := r.openHook(ctx, q); err != nil {
			return environment, err
		}
	}
	if r.noEvidence {
		return environment, nil
	}
	wrapped := &environment515{RepositoryCheckEnvironment: environment, runner: r, request: q}
	if r.partialOpen {
		return wrapped, errors.New("partial startup failed")
	}
	return wrapped, nil
}

type environment515 struct {
	RepositoryCheckEnvironment
	runner  *runner515
	request DependencyRequest
}

func (e *environment515) RunAcceptance(ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
	copy := in
	copy.Argv = slices.Clone(in.Argv)
	e.runner.invocations = append(e.runner.invocations, copy)
	if e.runner.accept != nil {
		return e.runner.accept(ctx, e.request, in)
	}
	return evidence515(in, "test", "passed", "selected test completed"), []byte("acceptance diagnostic\n"), nil
}

func evidence515(in RepositoryAcceptanceInvocation, kind, status, reason string) RepositoryAcceptanceEvidence {
	exit := 0
	if status == "failed" {
		exit = 1
	}
	return RepositoryAcceptanceEvidence{Complete: true, ExitCode: exit, Records: []RepositoryAcceptanceRecord{
		{Binding: in.Binding, Kind: "selection"},
		{Binding: in.Binding, Kind: kind, Status: status, Reason: reason},
	}}
}

type fixture515 struct {
	root, candidate string
	snapshot        *repository.Snapshot
	plan            *Plan
	sandbox         Sandbox
	runner          *runner515
	adapter         *adapter515
}

func newFixture515(t *testing.T, dirs ...string) *fixture515 {
	t.Helper()
	if len(dirs) == 0 {
		dirs = []string{"api"}
	}
	var parts []repository.Part
	p := &Plan{Name: "independent evidence", Summary: "Collect each selector independently.", Sources: map[string]string{}}
	for _, dir := range dirs {
		part := part484("go", dir)
		part.Commands["test_one"] = repository.Command{Argv: []string{"trusted-fixture", "{test}", "literal argument; $(not-a-shell)"}}
		parts = append(parts, part)
		p.Files = append(p.Files, dir+"/main.go")
		file := dir + "/lifecycle_accept_test.go"
		p.Sources[file] = "package fixture\r\nimport \"testing\"\r\nfunc TestFirst(t *testing.T) {}\r\nfunc TestSecond(t *testing.T) {}\r\n"
		for _, selector := range []string{"TestFirst", "TestSecond/a b; $(literal)"} {
			p.Tests = append(p.Tests, Test{Name: "same display label", Part: dir, Framework: "go", File: file, Selector: selector, Says: "The selected case completes independently."})
		}
	}
	root, candidate, snapshot := fixture484(t, parts, nil)
	f := &fixture515{root: root, candidate: candidate, snapshot: snapshot, plan: n503Read(t, p, snapshot), adapter: &adapter515{}}
	f.runner = &runner515{runner484: newRunner484(t)}
	f.sandbox = sandbox484(t, root, snapshot, f.runner.runner484)
	f.sandbox.Repository.Dependencies.Runner = f.runner
	f.sandbox.Repository.Adapters = map[string]RepositoryAcceptanceAdapter{"go": f.adapter}
	return f
}

func result515(t *testing.T, result Result, test Test) TestResult {
	t.Helper()
	var got []TestResult
	for _, record := range result.TestResults {
		if record.Phase == "accept" && record.identity() == test.Identity() {
			got = append(got, record)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one result for %s, got %+v in %+v", test.Identity(), got, result)
	}
	return got[0]
}

func base515(t *testing.T, f *fixture515, ctx context.Context) *Checked {
	t.Helper()
	checked, err := f.sandbox.checkRepositoryBase(ctx, f.root, f.plan)
	if checked == nil {
		t.Fatalf("base check lost its partial result: %v", err)
	}
	if err != nil && checked.Problem == "" {
		t.Fatalf("base infrastructure error has no qualification veto: %v", err)
	}
	return checked
}

func allFail515(r *runner515) {
	r.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
		return evidence515(in, "test", "failed", "assertion failed"), []byte("ordinary test failure\n"), nil
	}
}

func assertBindings515(t *testing.T, f *fixture515) {
	t.Helper()
	ids := map[string]bool{}
	for _, in := range f.runner.invocations {
		b := in.Binding
		if b.InvocationID == "" || ids[b.InvocationID] {
			t.Fatalf("invocation identity absent or reused: %+v", b)
		}
		ids[b.InvocationID] = true
		if b.SourceDigest != hash468(f.plan.Sources[b.File]) || in.Source != f.plan.Sources[b.File] || b.ExecutionDigest != f.plan.Execution.Digest || b.Package != "./"+b.Part || b.Framework != "go" {
			t.Fatalf("invocation not bound to its source, package and captured policy: %+v", in)
		}
		want := []string{"trusted-fixture", b.Selector, "literal argument; $(not-a-shell)"}
		if !reflect.DeepEqual(in.Argv, want) {
			t.Fatalf("literal captured arguments changed: %q, want %q", in.Argv, want)
		}
	}
}

func TestConfiguredAcceptanceBaseCollectsIndependentFailures(t *testing.T) {
	for _, suitePhase := range []string{"build", "test"} {
		t.Run(suitePhase, func(t *testing.T) {
			f := newFixture515(t, "api", "web")
			f.runner.hook = func(_ context.Context, _ DependencyRequest, argv []string) ([]byte, error) {
				if argv[1] == suitePhase {
					return []byte("ordinary suite failure"), errors.New("exit status 1")
				}
				return nil, nil
			}
			f.runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
				kind, reason := "test", "assertion failed"
				if in.Binding.Selector == "TestFirst" {
					kind, reason = "compile", "selected package lacks the proposed API"
				}
				return evidence515(in, kind, "failed", reason), []byte(reason), nil
			}
			checked := base515(t, f, context.Background())
			if checked.Problem != "" || checked.Run.Passed() || len(f.runner.invocations) != len(f.plan.Tests) {
				t.Fatalf("independent base failures did not qualify: %s\n%s", checked.Problem, checked.Run.Summary())
			}
			for _, test := range f.plan.Tests {
				r := result515(t, checked.Run, test)
				kind := "test"
				if test.Selector == "TestFirst" {
					kind = "compile"
				}
				if r.Status != "failed" || r.Kind != kind || r.Package != "./"+test.Part || r.Reason == "" {
					t.Fatalf("lost attributable failure: %+v", r)
				}
			}
			if len(f.runner.opens) != 2+len(f.plan.Tests) || f.runner.maxActive != 1 {
				t.Fatal("independent parts did not run serially")
			}
			assertBindings515(t, f)
			closed484(t, f.runner.runner484)
		})
	}
	f := newFixture515(t)
	f.runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
		status := "failed"
		if in.Binding.Selector == "TestFirst" {
			status = "passed"
		}
		return evidence515(in, "test", status, "mixed base verdict"), nil, nil
	}
	checked := base515(t, f, context.Background())
	if checked.Problem == "" || len(f.runner.invocations) != 2 || result515(t, checked.Run, f.plan.Tests[0]).Status != "passed" || result515(t, checked.Run, f.plan.Tests[1]).Status != "failed" {
		t.Fatalf("an already-passing selector did not invalidate the base: %+v", checked)
	}
	if !strings.Contains(checked.Problem, f.plan.Tests[0].Identity()) {
		t.Fatal("base verdict lost the already-passing full identity")
	}
}

func TestConfiguredAcceptanceCandidateNeedsCommandsAndSelectors(t *testing.T) {
	for _, mode := range []string{"all pass", "acceptance fails", "suite fails", "format fails", "lint fails", "build fails"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture515(t)
			if mode == "acceptance fails" {
				allFail515(f.runner)
			}
			if strings.HasSuffix(mode, " fails") && mode != "acceptance fails" {
				failed := strings.TrimSuffix(mode, " fails")
				if failed == "suite" {
					failed = "test"
				}
				f.runner.hook = func(_ context.Context, _ DependencyRequest, argv []string) ([]byte, error) {
					if argv[1] == failed {
						return []byte("--- PASS: TestFirst\n@@invariant test=0\n"), errors.New("exit status 2")
					}
					return nil, nil
				}
			}
			result, err := f.sandbox.Run(context.Background(), f.candidate, f.plan)
			if mode == "all pass" {
				if err != nil || !result.Passed() || len(f.runner.calls) != 4 || len(f.runner.invocations) != 2 {
					t.Fatalf("complete candidate failed: %v\n%s", err, result.Summary())
				}
				for _, test := range f.plan.Tests {
					if result515(t, result, test).Status != "passed" {
						t.Fatal(result.Summary())
					}
				}
			} else if result.Passed() {
				t.Fatalf("%s was hidden by another success", mode)
			}
			closed484(t, f.runner.runner484)
		})
	}
	// Applicability is captured policy and survives alongside acceptance.
	f := newFixture515(t)
	config := f.snapshot.Config()
	config.Parts[0].Commands["lint"] = repository.Command{NotApplicable: "No linter for this fixture."}
	data := config515(t, config)
	write468(t, f.root, repository.Path, data)
	git468(t, f.root, "add", "--all")
	git468(t, f.root, "commit", "-q", "--no-verify", "-m", "capture applicability")
	snapshot, err := repository.Load(context.Background(), f.root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	f.plan = n503Read(t, f.plan, snapshot)
	f.sandbox.Repository.Snapshot = snapshot
	result, err := f.sandbox.Run(context.Background(), f.candidate, f.plan)
	if err != nil || !result.Passed() || status484(t, result, "api", "lint") != "skipped" || !strings.Contains(result.Summary(), "No linter for this fixture.") {
		t.Fatalf("captured applicability lost: %v\n%s", err, result.Summary())
	}
}

func TestConfiguredAcceptanceLifecycleIsFrameworkIndependent(t *testing.T) {
	for _, tc := range []struct {
		framework, language, file, selector, source string
	}{
		{"pytest", "python", "app/test_ready.py", "test_ready.py::test_ready[one]", "def test_ready():\n    assert True\n"},
		{"vitest", "typescript", "app/ready.test.ts", "outer ready [one]", "// pinned Vitest fixture\r\n"},
		{"jest", "typescript", "app/ready.spec.ts", "outer ready [one]", "// pinned Jest fixture\n"},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			part := part484(tc.language, "app")
			part.Commands["test_one"] = repository.Command{Argv: []string{"trusted-fixture", "{test}", "literal argument; $(not-a-shell)"}}
			root, candidate, snapshot := fixture484(t, []repository.Part{part}, nil)
			p := n503Read(t, n503Plan(t, tc.framework, "app", tc.file, tc.selector, tc.source), snapshot)
			runner := &runner515{runner484: newRunner484(t)}
			sandbox := sandbox484(t, root, snapshot, runner.runner484)
			sandbox.Repository.Dependencies.Runner = runner
			sandbox.Repository.Adapters = map[string]RepositoryAcceptanceAdapter{
				tc.framework: &adapter515{selectHook: func(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
					selection, err := (&adapter515{}).Select(ctx, part, test)
					selection.Package = "" // These frameworks have no Go package attribution.
					return selection, err
				}},
			}
			result, err := sandbox.Run(context.Background(), candidate, p)
			if err != nil || !result.Passed() || len(runner.invocations) != 1 {
				t.Fatalf("shared lifecycle assumed a Go framework: %s", describe515(result, err))
			}
			in := runner.invocations[0]
			if in.Binding.Part != "app" || in.Binding.Framework != tc.framework || in.Binding.File != tc.file || in.Binding.Selector != tc.selector || in.Binding.Package != "" || in.Binding.SourceDigest != hash468(tc.source) || in.Binding.ExecutionDigest != p.Execution.Digest || in.Source != tc.source {
				t.Fatalf("framework-independent identity was changed: %+v", in)
			}
			if got := result515(t, result, p.Tests[0]); got.Status != "passed" || got.Package != "" {
				t.Fatalf("shared layer invented package attribution: %+v", got)
			}
			closed484(t, runner.runner484)
		})
	}
}

func TestConfiguredAcceptanceValidatesBeforePreparation(t *testing.T) {
	for _, bad := range []string{"snapshot", "digest", "ownership", "source missing", "duplicate identity", "adapter", "invocation", "selection error"} {
		t.Run(bad, func(t *testing.T) {
			f := newFixture515(t)
			switch bad {
			case "snapshot":
				f.sandbox.Repository.Snapshot = nil
			case "digest":
				f.plan.Execution.Digest = hash468("different policy")
			case "ownership":
				f.plan.Tests[0].Part = "wrong part"
			case "source missing":
				delete(f.plan.Sources, f.plan.Tests[0].File)
			case "duplicate identity":
				f.plan.Tests = append(f.plan.Tests, f.plan.Tests[0])
			case "adapter":
				f.sandbox.Repository.Adapters = nil
			case "invocation":
				f.adapter.selectHook = func(context.Context, repository.Part, Test) (RepositoryAcceptanceSelection, error) {
					return RepositoryAcceptanceSelection{}, errors.New("unsupported invocation form")
				}
			case "selection error":
				f.adapter.selectHook = func(_ context.Context, _ repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
					if test.Selector != "TestFirst" {
						return RepositoryAcceptanceSelection{}, errors.New("second selector cannot be resolved")
					}
					return RepositoryAcceptanceSelection{Package: "./api", Argv: []string{"trusted-fixture", test.Selector}}, nil
				}
			}
			checked := base515(t, f, context.Background())
			if checked.Problem == "" || len(f.runner.installs) != 0 || len(f.runner.inspections) != 0 || len(f.runner.opens) != 0 {
				t.Fatalf("invalid %s reached preparation or qualified: %+v", bad, checked)
			}
		})
	}
	f := newFixture515(t)
	f.runner.noEvidence = true
	result, _ := f.sandbox.Run(context.Background(), f.candidate, f.plan)
	if result.Passed() || len(f.runner.invocations) != 0 {
		t.Fatal("Run/Close-only environment supplied acceptance")
	}
	for _, test := range f.plan.Tests {
		r := result515(t, result, test)
		if (r.Status != "refused" && r.Status != "not_run") || r.Reason == "" {
			t.Fatalf("missing capability was not explicit: %+v", r)
		}
	}
	closed484(t, f.runner.runner484)
	// Public planning uses the same trusted adapter and independent base rules.
	f = newFixture515(t)
	allFail515(f.runner)
	checked, err := Check(context.Background(), n503Draft(t, f.plan), f.root, f.sandbox)
	if err != nil || checked == nil || checked.Problem != "" || len(f.runner.installs) != 1 {
		t.Fatalf("public planning lost trusted Go failures: %v %+v", err, checked)
	}
}

func TestConfiguredAcceptanceStagesPinnedSources(t *testing.T) {
	for _, mode := range []string{"absent", "line endings", "changed", "symlink", "hard link", "ancestor link"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture515(t)
			file := f.plan.Tests[0].File
			original := f.plan.Sources[file]
			path := filepath.Join(f.candidate, filepath.FromSlash(file))
			switch mode {
			case "line endings":
				write468(t, f.candidate, file, strings.ReplaceAll(original, "\r\n", "\n"))
			case "changed":
				write468(t, f.candidate, file, original+"// candidate edit\n")
			case "symlink", "hard link":
				outside := filepath.Join(t.TempDir(), "source.go")
				if err := os.WriteFile(outside, []byte(original), 0o644); err != nil {
					t.Fatal(err)
				}
				link := os.Symlink
				if mode == "hard link" {
					link = os.Link
				}
				if err := link(outside, path); err != nil {
					t.Fatal(err)
				}
			case "ancestor link":
				outside := filepath.Join(t.TempDir(), "api")
				if err := os.Rename(filepath.Join(f.candidate, "api"), outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(f.candidate, "api")); err != nil {
					t.Fatal(err)
				}
			}
			staged := 0
			f.runner.openHook = func(_ context.Context, q DependencyRequest) error {
				if q.SourceDir == f.root || q.SourceDir == f.candidate || read484(t, q.SourceDir, file) != original {
					t.Error("acceptance did not execute verbatim bytes from a private copy")
				}
				staged++
				return nil
			}
			write468(t, f.candidate, repository.Path, `{"candidate":"cannot register adapters or replace commands"}`)
			result, err := f.sandbox.Run(context.Background(), f.candidate, f.plan)
			if mode == "absent" || mode == "line endings" {
				if err != nil || !result.Passed() || staged != 1+len(f.plan.Tests) {
					t.Fatalf("safe source did not run: %v\n%s", err, result.Summary())
				}
				// Repeated runs must not accept stale records from a prior attempt.
				if _, err := f.sandbox.Run(context.Background(), f.candidate, f.plan); err != nil {
					t.Fatal(err)
				}
				assertBindings515(t, f)
				if mode == "absent" {
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("staging wrote acceptance into the caller's candidate")
					}
				} else if read484(t, f.candidate, file) != strings.ReplaceAll(original, "\r\n", "\n") {
					t.Fatal("staging rewrote the caller's normalized source")
				}
			} else if result.Passed() || staged != 0 || len(f.runner.invocations) != 0 {
				t.Fatalf("unsafe source %s executed: %v\n%s", mode, err, result.Summary())
			}
			if _, err := os.Lstat(filepath.Join(f.root, filepath.FromSlash(file))); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("lifecycle changed the base checkout")
			}
			closed484(t, f.runner.runner484)
		})
	}
}

// Keep helper errors useful without importing a separate test support package.
func deadline515(t *testing.T, ctx context.Context) time.Time {
	t.Helper()
	d, ok := ctx.Deadline()
	if !ok {
		t.Fatal("lifecycle operation has no bounded context")
	}
	return d
}

func describe515(result Result, err error) string {
	return fmt.Sprintf("error=%v\n%s", err, result.Summary())
}
