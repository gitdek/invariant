package plumbing

// Issue #567's local runtime contract. These fixtures provide files, a resolved
// Go executable, isolated caches and observations of actual OS processes. They
// never return a test outcome or replace selection/discovery/production hooks.
// All existing acceptance_*, go_* and native_* fixtures remain unchanged.
//
// Add CollectGoAcceptance(expected RepositoryAcceptanceInvocation,
// stream GoAcceptanceStream, streamErr error) (RepositoryAcceptanceEvidence,
// error). expected is the host's independent, discovered invocation. The
// collector validates the entire bound stream, including raw framing, decoded
// observations and per-test lifecycles. Diagnostics cannot supply evidence.
// An infrastructure error is returned even if authentic evidence completed.
//
// The concrete environment returned by OpenLocalGoProcessTransport additionally
// implements the existing optional RepositoryAcceptanceEnvironment interface.
// RunAcceptance accepts the discovered invocation for the selection with which
// this local environment was opened. It verifies that authority against its
// retained original and real discovery, then runs the same producer/collector;
// it does not mint a replacement ID, trust a caller-supplied package, run after
// failed discovery, or reuse an already consumed invocation. This can rediscover
// using the retained preliminary binding; no change to Discover's interface or
// the shared orchestrator is required. Every accepted invocation is single-use.
// Run/ReadFile/Stage/Prepare/Close retain their existing transport contracts.
//
// Collection emits one selection, a test/running record on authentic start and
// at most one exact test terminal, with the host binding on every record. Normal
// pass/assertion/panic/skip yield passed/failed/failed/skipped. A missing selected
// runtime name has no terminal; abrupt exit after start stays running. Reasons
// explain absent or unfinished work and keep testing's assertions/skip/panics.
// Parent validity follows the event's actual Parent, not a slash heuristic.
// All lifecycles must be consistent; valid parallel interleaving is accepted.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

type runtimeFixture567 struct {
	t                               *testing.T
	root, cache, executable, source string
	part                            repository.Part
	test                            Test
	mu                              sync.Mutex
	starts                          [][]string
	pids                            []int
	started                         func(context.Context, []string, int)
	environments                    []RepositoryProcessTransport
	runs                            []*GoAcceptanceRun
}

func runtimeContext567(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func runtimeWrite567(t *testing.T, root, name, contents string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func runtimeFixtureFor567(t *testing.T, cache, source string) *runtimeFixture567 {
	t.Helper()
	exe, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("default runtime acceptance requires real Go: ", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	f := &runtimeFixture567{t: t, root: t.TempDir(), cache: cache, executable: exe, source: source}
	f.part = repository.Part{Name: "api", Directory: "api", Language: "go", Commands: map[string]repository.Command{
		"test_one": {Argv: []string{"go", "test", "./...", "-run", "^{test}$", "-count=1", "-v=false"}},
	}}
	f.test = Test{Name: "display label is not a selector", Says: "one real runtime test", Part: "api", Framework: "go", File: "api/nested/runtime_accept_test.go", Selector: "TestReady"}
	runtimeWrite567(t, f.root, "api/go.mod", "module example.invalid/runtime567\n\ngo 1.27.1\n")
	runtimeWrite567(t, f.root, "api/go.sum", "")
	runtimeWrite567(t, f.root, "api/nested/value.go", "package fixture\nconst Value = 7\n")
	runtimeWrite567(t, f.root, f.test.File, strings.ReplaceAll(source, "\r\n", "\n"))
	t.Cleanup(func() {
		for _, run := range f.runs {
			c, done := context.WithTimeout(context.Background(), 5*time.Second)
			_ = run.Close(c)
			done()
		}
		for _, env := range f.environments {
			c, done := context.WithTimeout(context.Background(), 5*time.Second)
			_ = env.Close(c)
			done()
		}
		f.assertGone()
	})
	return f
}

func (f *runtimeFixture567) observe(ctx context.Context, argv []string, pid int) {
	f.mu.Lock()
	f.starts = append(f.starts, slices.Clone(argv))
	f.pids = append(f.pids, pid)
	f.mu.Unlock()
	if f.started != nil {
		f.started(ctx, argv, pid)
	}
}

func (f *runtimeFixture567) assertGone() {
	f.t.Helper()
	f.mu.Lock()
	pids := slices.Clone(f.pids)
	f.mu.Unlock()
	for _, pid := range pids {
		for _, target := range []int{pid, -pid} {
			if err := syscall.Kill(target, 0); !errors.Is(err, syscall.ESRCH) {
				f.t.Errorf("owned process/group %d survived synchronous completion: %v", target, err)
				_ = syscall.Kill(target, syscall.SIGKILL)
			}
		}
	}
}

func (f *runtimeFixture567) invocation(ctx context.Context) (RepositoryAcceptanceInvocation, error) {
	sel, err := (GoRepositoryAcceptanceAdapter{}).Select(ctx, f.part, f.test)
	if err != nil {
		return RepositoryAcceptanceInvocation{}, err
	}
	p := &Plan{Sources: map[string]string{f.test.File: f.source}, Execution: &Execution{Digest: dependencyHash([]byte("host captured execution policy 567"))}}
	return repositoryInvocation(p, f.test, sel), nil
}

func (f *runtimeFixture567) open(ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryProcessTransport, error) {
	env, err := OpenLocalGoProcessTransport(ctx, LocalGoProcessOptions{SourceDir: f.root, CacheDir: f.cache, GoExecutable: f.executable, Part: f.part, Invocation: in, OnStart: f.observe})
	if env != nil {
		f.environments = append(f.environments, env)
	}
	return env, err
}

func (f *runtimeFixture567) local(ctx context.Context) (RepositoryProcessTransport, RepositoryAcceptanceInvocation, error) {
	in, err := f.invocation(ctx)
	if err != nil {
		return nil, in, err
	}
	env, err := f.open(ctx, in)
	if err != nil {
		return env, in, err
	}
	discovery, err := (GoRepositoryAcceptanceAdapter{}).Discover(ctx, f.part, in, env)
	if err != nil {
		return env, in, err
	}
	file := strings.TrimPrefix(f.test.File, f.part.Directory+"/")
	staged, err := env.ReadFile(ctx, file)
	if err != nil || string(staged) != f.source {
		f.t.Fatalf("local runtime did not stage verbatim source: %v", err)
	}
	in.Binding = discovery.Binding
	return env, in, nil
}

func runtimeEnvironment567(t *testing.T, env RepositoryProcessTransport) RepositoryAcceptanceEnvironment {
	t.Helper()
	got, ok := env.(RepositoryAcceptanceEnvironment)
	if !ok {
		t.Fatal("local process environment has no production runtime acceptance collector")
	}
	return got
}

func (f *runtimeFixture567) execute(ctx context.Context) (RepositoryAcceptanceInvocation, RepositoryAcceptanceEvidence, []byte, error) {
	f.t.Helper()
	env, in, err := f.local(ctx)
	if err != nil {
		f.t.Fatal("real selection/discovery: ", err)
	}
	evidence, diagnostics, runErr := runtimeEnvironment567(f.t, env).RunAcceptance(ctx, in)
	f.assertGone()
	c, done := context.WithTimeout(context.Background(), 5*time.Second)
	closeErr := env.Close(c)
	done()
	return in, evidence, diagnostics, errors.Join(runErr, closeErr)
}

func (f *runtimeFixture567) raw(ctx context.Context) (GoAcceptanceStream, error) {
	f.t.Helper()
	run, err := StartGoAcceptance(ctx, f.part, f.test, f.source, dependencyHash([]byte("host captured execution policy 567")), f.open)
	if run != nil {
		f.runs = append(f.runs, run)
	}
	if err != nil {
		f.t.Fatal("real producer startup: ", err)
	}
	stream, waitErr := run.Wait(ctx)
	f.assertGone()
	return stream, waitErr
}

func runtimeResult567(in RepositoryAcceptanceInvocation, evidence RepositoryAcceptanceEvidence) (TestResult, error) {
	b := in.Binding
	return repositoryEvidence(b, evidence, TestResult{Phase: "accept", Name: "display label is not a selector", Part: b.Part, Framework: b.Framework, File: b.File, Selector: b.Selector})
}

func runtimeCompleted567(t *testing.T, in RepositoryAcceptanceInvocation, evidence RepositoryAcceptanceEvidence, runErr error, status, reason string) TestResult {
	t.Helper()
	r, err := runtimeResult567(in, evidence)
	if runErr != nil || err != nil || r.Kind != "test" || r.Status != status || !evidence.Complete || r.Reason == "" {
		t.Fatalf("wanted real test/%s: result=%+v evidence=%+v runtime=%v validation=%v", status, r, evidence, runErr, err)
	}
	if reason != "" && !strings.Contains(r.Reason, reason) {
		t.Fatalf("lost framework reason %q: %+v", reason, r)
	}
	selection, running, terminal := 0, 0, 0
	for _, record := range evidence.Records {
		if record.Binding != in.Binding {
			t.Fatalf("changed evidence authority: %+v vs %+v", record.Binding, in.Binding)
		}
		if record.Kind == "selection" {
			selection++
		}
		if record.Kind == "test" && record.Status == "running" {
			running++
		}
		if record.Kind == "test" && record.Status == status {
			terminal++
		}
	}
	if selection != 1 || running != 1 || terminal != 1 {
		t.Fatalf("not one selection, authentic start and terminal: %+v", evidence)
	}
	return r
}

func runtimeUnfinished567(t *testing.T, in RepositoryAcceptanceInvocation, evidence RepositoryAcceptanceEvidence, runErr error, started bool) {
	t.Helper()
	r, err := runtimeResult567(in, evidence)
	if r.Status == "passed" || r.Status == "failed" || r.Status == "skipped" || r.Reason == "" || err == nil && runErr == nil {
		t.Fatalf("unfinished execution became a verdict or lost explanation: %+v %+v %v %v", r, evidence, err, runErr)
	}
	if started && (r.Kind != "test" || r.Status != "running") {
		t.Fatalf("authentic partial start lost: %+v", r)
	}
}

func TestLocalGoRuntimeEvidence(t *testing.T) {
	ctx, cache := runtimeContext567(t), t.TempDir()
	source := "package fixture\r\nimport \"testing\"\r\nfunc TestReady(t *testing.T){ if Value != 8 { t.Fatalf(\"real value assertion: got %d want 8\", Value) }; t.Log(\"real value passed\") }\r\n"
	f := runtimeFixtureFor567(t, cache, source)
	base, failed, _, err := f.execute(ctx)
	runtimeCompleted567(t, base, failed, err, "failed", "real value assertion")
	runtimeWrite567(t, f.root, "api/nested/value.go", "package fixture\nconst Value = 8\n")
	changed, passed, _, err := f.execute(ctx)
	runtimeCompleted567(t, changed, passed, err, "passed", "real value passed")
	if base.Binding.InvocationID == changed.Binding.InvocationID || base.Binding.InvocationID == "" {
		t.Fatal("attempt reused invocation")
	}
	b, c := base.Binding, changed.Binding
	b.InvocationID, c.InvocationID = "", ""
	if b != c || c.Package != "example.invalid/runtime567/nested" || c.SourceDigest != dependencyHash([]byte(source)) || c.ExecutionDigest != dependencyHash([]byte("host captured execution policy 567")) || changed.Source != source {
		t.Fatalf("production-only change lost pinned source/identity: %+v %+v", base, changed)
	}
	onDisk, err := os.ReadFile(filepath.Join(f.root, f.test.File))
	if err != nil || string(onDisk) != strings.ReplaceAll(source, "\r\n", "\n") {
		t.Fatal("private runtime rewrote caller source")
	}
	for _, tc := range []struct {
		name, extra, body, status, reason string
		started                           bool
	}{
		{"pass", "", `t.Log("ordinary pass")`, "passed", "ordinary pass", true},
		{"assertion", "", `t.Fatal("ordinary assertion")`, "failed", "ordinary assertion", true},
		{"panic", "", `panic("body panic")`, "failed", "body panic", true},
		{"parallel assertion", "", `t.Run("bad",func(t *testing.T){t.Parallel();t.Fatal("parallel assertion")});t.Run("skip",func(t *testing.T){t.Parallel();t.Skip("parallel skip")})`, "failed", "parallel assertion", true},
		{"nested panic", "", `t.Run("child",func(t *testing.T){panic("nested body panic")})`, "failed", "nested body panic", true},
		{"cleanup panic", "", `t.Cleanup(func(){panic("cleanup panic")})`, "failed", "cleanup panic", true},
		{"exit zero panic", "", `t.Cleanup(func(){t.Log("exit cleanup")});os.Exit(0)`, "failed", "", true},
		{"selected skip", "", `t.Skip("selected skip reason")`, "skipped", "selected skip reason", true},
		{"failure exit overridden", `func TestMain(m *testing.M){m.Run();os.Exit(0)}`, `t.Fatal("failure before exit override")`, "failed", "failure before exit override", true},
		{"unattributed exit after pass", `func TestMain(m *testing.M){m.Run();os.Exit(23)}`, `t.Log("real pass before unrelated exit")`, "", "", false},
		{"zero tests", `func TestMain(m *testing.M){flag.Parse();flag.Set("test.run","^Absent567$");os.Exit(m.Run())}`, `t.Fatal("not reached")`, "", "", false},
		{"TestMain exit zero", `func TestMain(m *testing.M){os.Exit(0)}`, `t.Fatal("not reached")`, "", "", false},
		{"TestMain early failure", `func TestMain(m *testing.M){os.Exit(17)}`, `t.Fatal("not reached")`, "", "", false},
		{"initialization exit", `func init(){os.Exit(18)}`, `t.Fatal("not reached")`, "", "", false},
		{"abrupt test exit", "", `fmt.Fprintln(os.Stderr,"abrupt test diagnostic");os.Exit(19)`, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "package fixture\nimport (\"testing\";\"os\";\"fmt\";\"flag\")\nvar _=os.Exit\nvar _=fmt.Fprintln\nvar _=flag.Parse\n" + tc.extra + "\nfunc TestReady(t *testing.T){" + tc.body + "}\n"
			g := runtimeFixtureFor567(t, cache, src)
			in, e, out, runErr := g.execute(ctx)
			if tc.status == "" {
				runtimeUnfinished567(t, in, e, runErr, tc.started)
				if tc.name == "abrupt test exit" && !strings.Contains(string(out), "abrupt test diagnostic") {
					t.Fatal("lost abrupt exit diagnostic")
				}
			} else {
				r := runtimeCompleted567(t, in, e, runErr, tc.status, tc.reason)
				if tc.status == "skipped" {
					result := Result{TestResults: []TestResult{r}, Accepted: map[string]bool{g.test.Identity(): true}}
					if result.Passed() {
						t.Fatal("skip qualified a candidate")
					}
				}
			}
		})
	}
}

func TestLocalGoRuntimeExactSelectors(t *testing.T) {
	ctx, cache := runtimeContext567(t), t.TempDir()
	source := `package fixture
import "testing"
func TestReadyNeighbor(t *testing.T){t.Fatal("prefix neighbor ran")}
func TestReady(t *testing.T){
 t.Run("case",func(t *testing.T){t.Fatal("parameter case zero")})
 t.Run("case",func(t *testing.T){t.Log("parameter case one")})
 t.Run("nested",func(t *testing.T){t.Run("leaf.[x]+",func(t *testing.T){t.Log("exact nested leaf")})})
 t.Run("one/two",func(t *testing.T){t.Log("actual parent has no one node")})
 t.Run("parallel",func(t *testing.T){
  for _,name:=range []string{"left","right"}{t.Run(name,func(t *testing.T){t.Parallel();t.Log("parallel child "+name)})}
 })
 t.Run("skip",func(t *testing.T){t.Skip("exact skipped child")})
}
`
	for _, tc := range []struct{ selector, status, reason string }{
		{"TestReady/case", "failed", "parameter case zero"},
		{"TestReady/case#01", "passed", "parameter case one"},
		{"TestReady/nested/leaf.[x]+", "passed", "exact nested leaf"},
		{"TestReady/one/two", "passed", "actual parent has no one node"},
		{"TestReady/parallel", "passed", ""},
		{"TestReady/parallel/right", "passed", "parallel child right"},
		{"TestReady/skip", "skipped", "exact skipped child"},
		{"TestReady/missing", "", ""},
		{"TestReady/cas", "", ""},
		{"TestReady/case#02", "", ""},
		{"TestReady/nested/leaf", "", ""},
		{"TestReady/one", "", ""},
	} {
		t.Run(tc.selector, func(t *testing.T) {
			f := runtimeFixtureFor567(t, cache, source)
			f.test.Selector = tc.selector
			runtimeWrite567(t, f.root, "api/other/other_test.go", "package other\nimport \"testing\"\nfunc TestReady(t *testing.T){t.Fatal(\"same name other package\")}\n")
			runtimeWrite567(t, f.root, "other/go.mod", "module example.invalid/other567\n\ngo 1.27.1\n")
			runtimeWrite567(t, f.root, "other/other_test.go", "package other\nimport \"testing\"\nfunc TestReady(t *testing.T){t.Fatal(\"same name other part\")}\n")
			in, e, _, err := f.execute(ctx)
			if tc.status == "" {
				runtimeUnfinished567(t, in, e, err, false)
			} else {
				runtimeCompleted567(t, in, e, err, tc.status, tc.reason)
			}
			if in.Binding.Package != "example.invalid/runtime567/nested" || in.Binding.Selector != tc.selector {
				t.Fatal("neighbor changed attribution")
			}
		})
	}
	// External tests still bind to the ordinary discovered import path.
	f := runtimeFixtureFor567(t, cache, "package fixture_test\nimport (\"testing\"; app \"example.invalid/runtime567/nested\")\nfunc TestReady(t *testing.T){if app.Value!=7{t.Fatal(app.Value)};t.Log(\"external exact test\")}\n")
	in, e, _, err := f.execute(ctx)
	runtimeCompleted567(t, in, e, err, "passed", "external exact test")
	if in.Binding.Package != "example.invalid/runtime567/nested" {
		t.Fatal("external test changed package identity")
	}
}
