package plumbing

// These integration fixtures supply real local processes, never Go verdicts.
// Only immutable dependency-image preparation uses the existing stand-in.
// The environment exposes RepositoryProcessTransport, deliberately hiding the
// local transport's single-invocation RunAcceptance convenience method: the
// shared lifecycle owns discovery, fresh invocation IDs, collection and cleanup.
// Run/ReadFile/Close-only and trusted evidence515 environments keep their old
// contracts. No existing fixture or fake needs another method or new arguments.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

type repositoryCase582 struct{ part, pkg, selector, source string }

func (c repositoryCase582) file() string { return path.Join(c.part, c.pkg, "runtime_accept_test.go") }
func repositorySource582(body, extra string) string {
	return "package fixture\nimport (\"testing\";\"os\";\"fmt\";\"flag\";\"time\")\n" +
		"var _=os.Exit; var _=fmt.Println; var _=flag.Parse; var _=time.Second\n" + extra +
		"\nfunc TestReady(t *testing.T){" + body + "}\n"
}
func repositoryControl582() repositoryCase582 {
	return repositoryCase582{"api", "nested", "TestReady", repositorySource582(`if Value!=7 {t.Fatal("needs seven",Value)};t.Log("real completion 582")`, "")}
}

type repositoryObservation582 struct {
	stage, part string
	argv        []string
	at, end     time.Time
}
type repositoryFixture582 struct {
	t               *testing.T
	root, candidate string
	plan            *Plan
	sandbox         Sandbox
	runner          *repositoryRunner582
}
type repositoryRunner582 struct {
	*runner468
	fixture     *repositoryFixture582
	parts       map[string]repository.Part
	cache, exe  string
	mu          sync.Mutex
	observed    []repositoryObservation582
	envs        []*repositoryEnvironment582
	before      func(context.Context, string, string, []string) error
	damage      func(string, RepositoryProcessOutput) (RepositoryProcessOutput, error)
	duringWait  func(context.Context, RepositoryProcess)
	checkStages bool
}

func newRepositoryFixture582(t *testing.T, cache string, cases []repositoryCase582, commands bool, files map[string]string, edit func(*repository.Config)) *repositoryFixture582 {
	t.Helper()
	f := &repositoryFixture582{t: t, root: t.TempDir(), candidate: t.TempDir()}
	exe, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("default integration requires real Go: ", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	r := &repositoryRunner582{runner468: newRunner468(t), fixture: f, cache: cache, exe: exe, parts: map[string]repository.Part{}, checkStages: true}
	f.runner = r
	p := &Plan{Name: "real Go integration fixture", Summary: "Require independent real Go evidence.", Sources: map[string]string{}}
	config := repository.Config{Version: 1, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
	for _, c := range cases {
		if _, exists := r.parts[c.part]; !exists {
			part := part468("go", c.part)
			part.Name = c.part
			part.Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-run", "^{test}$", "-count=1", "-v=false", "-timeout=0"}}
			if commands {
				part.Commands["format"] = repository.Command{Argv: []string{"go", "version"}}
				part.Commands["lint"] = repository.Command{Argv: []string{"go", "version"}}
				part.Commands["build"] = repository.Command{Argv: []string{"go", "build", "./..."}}
				part.Commands["test"] = repository.Command{Argv: []string{"go", "test", "./...", "-count=1", "-timeout=20s"}}
			}
			r.parts[c.part] = part
			config.Parts = append(config.Parts, part)
			write468(t, f.root, path.Join(c.part, "go.mod"), "module example.invalid/"+strings.ReplaceAll(c.part, "/", "_")+"582\n\ngo 1.27.1\n")
			write468(t, f.root, path.Join(c.part, "go.sum"), "")
		}
		value := path.Join(c.part, c.pkg, "value.go")
		write468(t, f.root, value, "package fixture\nconst Value=0\n")
		if !slices.Contains(p.Files, value) {
			p.Files = append(p.Files, value)
		}
		if previous, ok := p.Sources[c.file()]; ok && previous != c.source {
			t.Fatal("fixture supplied different bytes for one file")
		}
		p.Sources[c.file()] = c.source
		p.Tests = append(p.Tests, Test{Name: "same display label", Part: c.part, Framework: "go", File: c.file(), Selector: c.selector, Says: "The exact selected case completes."})
	}
	for file, source := range files {
		write468(t, f.root, file, source)
	}
	if edit != nil {
		edit(&config)
	}
	for _, part := range config.Parts {
		r.parts[part.Name] = part
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	write468(t, f.root, repository.Path, string(data))
	git468(t, f.root, "init", "-q")
	git468(t, f.root, "add", "--all")
	git468(t, f.root, "commit", "-q", "--no-verify", "-m", "real Go integration base")
	snapshot, err := repository.Load(t.Context(), f.root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	f.plan = n503Read(t, p, snapshot)
	if err := CopyCheckout(t.Context(), f.root, f.candidate); err != nil {
		t.Fatal(err)
	}
	f.sandbox = Sandbox{Original: f.root, Timeout: 3 * time.Minute, ShardTimeout: 2 * time.Minute,
		Repository: &RepositoryChecks{Snapshot: snapshot, Dependencies: DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: r}, Adapters: map[string]RepositoryAcceptanceAdapter{"go": GoRepositoryAcceptanceAdapter{}}}}
	t.Cleanup(func() {
		for _, env := range r.envs {
			if !env.closed {
				t.Error("shared lifecycle did not close its environment")
			}
			ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
			_ = env.RepositoryProcessTransport.Close(ctx)
			done()
		}
	})
	return f
}

func (r *repositoryRunner582) visit(ctx context.Context, stage, part string, argv []string) error {
	d, ok := ctx.Deadline()
	if !ok {
		r.t.Errorf("%s has no inherited deadline", stage)
	}
	r.mu.Lock()
	r.observed = append(r.observed, repositoryObservation582{stage, part, slices.Clone(argv), time.Now(), d})
	r.mu.Unlock()
	if r.before != nil {
		return r.before(ctx, stage, part, argv)
	}
	return ctx.Err()
}
func (r *repositoryRunner582) Install(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
	if err := r.visit(ctx, "install", q.Directory, q.Argv); err != nil {
		return DependencyArtifact{}, err
	}
	return r.runner468.Install(ctx, q)
}
func (r *repositoryRunner582) Inspect(ctx context.Context, image, platform string) (DependencyArtifact, error) {
	if err := r.visit(ctx, "inspect", "", nil); err != nil {
		return DependencyArtifact{}, err
	}
	return r.runner468.Inspect(ctx, image, platform)
}
func (r *repositoryRunner582) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	if err := r.visit(ctx, "open", q.Directory, nil); err != nil {
		return nil, err
	}
	if q.Network || q.Pull || q.Env["GOPROXY"] != "off" || q.Env["GOTOOLCHAIN"] != "local" || q.Env["GOWORK"] != "off" {
		return nil, errors.New("shared lifecycle lost captured offline execution policy")
	}
	if r.checkStages && len(r.envs) > 0 {
		last := r.envs[len(r.envs)-1]
		if !last.closed {
			r.t.Error("next part started before previous environment closed")
		}
		if _, err := os.Stat(last.request.SourceDir); !errors.Is(err, os.ErrNotExist) {
			r.t.Error("previous part staging still exists when next environment opens")
		}
	}
	part := r.parts[q.Directory]
	var selected Test
	for _, test := range r.fixture.plan.Tests {
		if test.Part == part.Name {
			selected = test
			break
		}
	}
	selection, err := (GoRepositoryAcceptanceAdapter{}).Select(ctx, part, selected)
	if err != nil {
		return nil, err
	}
	// This opening invocation only allows the existing local transport to stage
	// a verbatim source. It never supplies runtime evidence to the orchestrator.
	in := repositoryInvocation(r.fixture.plan, selected, selection)
	transport, err := OpenLocalGoProcessTransport(ctx, LocalGoProcessOptions{SourceDir: q.SourceDir, GoExecutable: r.exe, CacheDir: r.cache, Part: part, Invocation: in})
	if transport == nil {
		return nil, err
	}
	env := &repositoryEnvironment582{RepositoryProcessTransport: transport, runner: r, request: q}
	r.envs = append(r.envs, env)
	return env, err
}

type repositoryEnvironment582 struct {
	RepositoryProcessTransport
	runner  *repositoryRunner582
	request DependencyRequest
	closed  bool
}

func repositoryStage582(argv []string) string {
	if len(argv) < 2 {
		return "command"
	}
	switch argv[1] {
	case "list":
		return "discovery"
	case "run":
		return "helper"
	case "build":
		return "build"
	case "test":
		if slices.Contains(argv, "-c") {
			for _, arg := range argv {
				if strings.HasPrefix(arg, "-overlay=") {
					return "instrument"
				}
			}
			return "compile"
		}
		return "suite"
	}
	return "command"
}
func (e *repositoryEnvironment582) Run(ctx context.Context, argv []string) ([]byte, error) {
	if err := e.runner.visit(ctx, repositoryStage582(argv), e.request.Directory, argv); err != nil {
		return nil, err
	}
	return e.RepositoryProcessTransport.Run(ctx, argv)
}
func (e *repositoryEnvironment582) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := e.runner.visit(ctx, "read", e.request.Directory, []string{name}); err != nil {
		return nil, err
	}
	return e.RepositoryProcessTransport.ReadFile(ctx, name)
}
func (e *repositoryEnvironment582) Stage(ctx context.Context, name string, data []byte) (string, error) {
	if err := e.runner.visit(ctx, "stage", e.request.Directory, []string{name}); err != nil {
		return "", err
	}
	return e.RepositoryProcessTransport.Stage(ctx, name, data)
}
func (e *repositoryEnvironment582) Prepare(ctx context.Context, spec RepositoryProcessSpec) (RepositoryPreparedProcess, error) {
	if err := e.runner.visit(ctx, "prepare", e.request.Directory, spec.Argv); err != nil {
		return nil, err
	}
	p, err := e.RepositoryProcessTransport.Prepare(ctx, spec)
	if p == nil {
		return nil, err
	}
	return &repositoryPrepared582{RepositoryPreparedProcess: p, env: e, argv: slices.Clone(spec.Argv)}, err
}
func (e *repositoryEnvironment582) Close(ctx context.Context) error {
	if e.closed {
		return nil
	}
	if ctx.Err() != nil {
		e.runner.t.Error("cleanup inherited cancellation")
	}
	if d, ok := ctx.Deadline(); !ok || time.Until(d) > SandboxCleanupAllowance+time.Second || time.Until(d) < 10*time.Second {
		e.runner.t.Error("cleanup did not receive its fresh fifteen-second reserve")
	}
	if _, err := os.Stat(e.request.SourceDir); err != nil {
		e.runner.t.Error("staging was removed before environment cleanup")
	}
	err := e.runner.visit(ctx, "close", e.request.Directory, nil)
	err = errors.Join(err, e.RepositoryProcessTransport.Close(ctx))
	e.closed = true
	return err
}

type repositoryPrepared582 struct {
	RepositoryPreparedProcess
	env  *repositoryEnvironment582
	argv []string
}

func (p *repositoryPrepared582) Start(ctx context.Context) (RepositoryProcess, error) {
	if err := p.env.runner.visit(ctx, "start", p.env.request.Directory, p.argv); err != nil {
		return nil, err
	}
	child, err := p.RepositoryPreparedProcess.Start(ctx)
	if child == nil {
		return nil, err
	}
	return &repositoryProcess582{RepositoryProcess: child, env: p.env}, err
}

type repositoryProcess582 struct {
	RepositoryProcess
	env *repositoryEnvironment582
}

func (p *repositoryProcess582) Wait(ctx context.Context) (RepositoryProcessOutput, error) {
	if p.env.runner.duringWait != nil {
		p.env.runner.duringWait(ctx, p.RepositoryProcess)
	}
	before := p.env.runner.visit(ctx, "wait", p.env.request.Directory, nil)
	out, err := p.RepositoryProcess.Wait(ctx)
	err = errors.Join(before, err)
	if p.env.runner.damage != nil {
		var damageErr error
		out, damageErr = p.env.runner.damage(p.env.request.Directory, out)
		err = errors.Join(err, damageErr)
	}
	return out, err
}
func (f *repositoryFixture582) run(ctx context.Context, base bool) (Result, string, error) {
	if base {
		checked, err := f.sandbox.checkRepositoryBase(ctx, f.root, f.plan)
		if checked == nil {
			f.t.Fatalf("base lost partial report: %v", err)
		}
		return checked.Run, checked.Problem, err
	}
	r, err := f.sandbox.Run(ctx, f.candidate, f.plan)
	return r, "", err
}
func (f *repositoryFixture582) candidateValue(value int) {
	for _, file := range f.plan.Files {
		write468(f.t, f.candidate, file, fmt.Sprintf("package fixture\nconst Value=%d\n", value))
	}
}
func repositoryResult582(t *testing.T, result Result, test Test, status, reason string) TestResult {
	t.Helper()
	r := result515(t, result, test)
	if r.Status != status || r.Reason == "" || !strings.Contains(r.Reason, reason) {
		t.Fatalf("want %s %q for %s, got %+v\n%s", status, reason, test.Identity(), r, result.Summary())
	}
	if status == "passed" || status == "failed" || status == "skipped" {
		want := "example.invalid/" + strings.ReplaceAll(test.Part, "/", "_") + "582"
		rel := strings.TrimPrefix(path.Dir(test.File), test.Part)
		want += rel
		if r.Kind != "test" || r.Package != want {
			t.Fatalf("lost exact test/package attribution: %+v want %s", r, want)
		}
	}
	return r
}
func repositoryRoundTrip582(t *testing.T, result Result) Result {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var copied Result
	if err := json.Unmarshal(data, &copied); err != nil {
		t.Fatal(err)
	}
	copied.Output = copied.Tail(64)
	if !reflect.DeepEqual(copied.TestResults, result.TestResults) || !reflect.DeepEqual(copied.CommandResults, result.CommandResults) || !reflect.DeepEqual(copied.Problems, result.Problems) || copied.Passed() != result.Passed() {
		t.Fatal("JSON/clipping changed structured evidence")
	}
	for _, r := range copied.TestResults {
		if !strings.Contains(copied.Summary(), r.identity()) || !strings.Contains(copied.Summary(), r.Reason) {
			t.Fatal("summary lost identity or reason")
		}
	}
	return copied
}
func repositoryRequireControl582(t *testing.T, cache string) {
	t.Helper()
	f := newRepositoryFixture582(t, cache, []repositoryCase582{repositoryControl582()}, false, nil, nil)
	f.candidateValue(7)
	r, _, err := f.run(t.Context(), false)
	if err != nil || !r.Passed() {
		t.Fatalf("real Go collector is not integrated with Sandbox.Run: %v\n%s", err, r.Summary())
	}
	repositoryResult582(t, r, f.plan.Tests[0], "passed", "real completion 582")
}

func TestRepositoryGoRealOutcomes582(t *testing.T) {
	cache := t.TempDir()
	t.Run("production-only correction", func(t *testing.T) {
		c := repositoryControl582()
		c.source = strings.ReplaceAll(c.source, "\n", "\r\n")
		f := newRepositoryFixture582(t, cache, []repositoryCase582{c}, true, nil, nil)
		base, problem, err := f.run(t.Context(), true)
		if err != nil || problem != "" {
			t.Fatalf("real base assertion did not qualify: %v %s\n%s", err, problem, base.Summary())
		}
		repositoryResult582(t, base, f.plan.Tests[0], "failed", "needs seven")
		f.candidateValue(7)
		write468(t, f.candidate, c.file(), strings.ReplaceAll(c.source, "\r\n", "\n"))
		write468(t, f.candidate, repository.Path, `{"adapters":{"go":"forged"},"commands":{"test_one":"skip"}}`)
		got, _, err := f.run(t.Context(), false)
		if err != nil || !got.Passed() {
			t.Fatalf("production correction did not pass: %v\n%s", err, got.Summary())
		}
		repositoryResult582(t, got, f.plan.Tests[0], "passed", "real completion 582")
		if read484(t, f.candidate, c.file()) != strings.ReplaceAll(c.source, "\r\n", "\n") || f.plan.Sources[c.file()] != c.source {
			t.Fatal("execution rewrote ratified or caller bytes")
		}
		if _, err := os.Stat(filepath.Join(f.root, c.file())); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("base checkout was modified")
		}
		repositoryRoundTrip582(t, base)
		repositoryRoundTrip582(t, got)
	})
	for _, tc := range []struct {
		name, body, extra, selector, status, reason string
		qualifies                                   bool
	}{
		{"pass", `t.Log("real pass")`, "", "TestReady", "passed", "real pass", false},
		{"assertion", `t.Fatal("real assertion")`, "", "TestReady", "failed", "real assertion", true},
		{"panic", `panic("real panic")`, "", "TestReady", "failed", "real panic", true},
		{"skip", `t.Skip("real skip")`, "", "TestReady", "skipped", "real skip", false},
		{"absent child", `t.Run("exists",func(t *testing.T){t.Fatal("neighbor")})`, "", "TestReady/missing", "not_run", "", false},
		{"zero tests", `t.Fatal("not reached")`, `func TestMain(m *testing.M){flag.Parse();flag.Set("test.run","^Absent$");os.Exit(m.Run())}`, "TestReady", "not_run", "", false},
		{"TestMain exits", `t.Fatal("not reached")`, `func TestMain(m *testing.M){os.Exit(0)}`, "TestReady", "not_run", "", false},
		{"abrupt exit", `fmt.Println("--- PASS: TestReady");os.Exit(19)`, "", "TestReady", "running", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRepositoryFixture582(t, cache, []repositoryCase582{{"api", "nested", tc.selector, repositorySource582(tc.body, tc.extra)}}, false, nil, nil)
			for _, base := range []bool{true, false} {
				r, problem, err := f.run(t.Context(), base)
				if base && (problem == "") != tc.qualifies {
					t.Fatalf("wrong base qualification %v: %s\n%s", err, problem, r.Summary())
				}
				if !base && r.Passed() != (tc.status == "passed") {
					t.Fatalf("wrong candidate verdict: %v\n%s", err, r.Summary())
				}
				repositoryResult582(t, r, f.plan.Tests[0], tc.status, tc.reason)
				repositoryRoundTrip582(t, r)
			}
		})
	}
}

func TestRepositoryGoIndependentEvidence582(t *testing.T) {
	cache := t.TempDir()
	source := repositorySource582(`t.Run("case",func(t *testing.T){t.Fatal("case zero")});t.Run("case",func(t *testing.T){t.Log("case one")})`, "")
	cases := []repositoryCase582{
		{"api", "broken", "TestReady", repositorySource582(`MissingAPI582(t)`, "")},
		{"api", "nested", "TestReady/case", source},
		{"api", "nested", "TestReady/case#01", source},
		{"api", "other", "TestReady", repositorySource582(`t.Fatal("other package")`, "")},
		{"api/child", "nested", "TestReady", repositorySource582(`t.Fatal("child module")`, "")},
		{"web", "nested", "TestReady", repositorySource582(`t.Fatal("other part")`, "")},
	}
	f := newRepositoryFixture582(t, cache, cases, true, map[string]string{"api/broken/broken.go": "package fixture\nvar BrokenBuild=MissingProduction582\n"}, nil)
	r, problem, _ := f.run(t.Context(), true)
	if problem == "" {
		t.Fatal("compiler refusal and passing neighbor qualified base")
	}
	first := result515(t, r, f.plan.Tests[0])
	if first.Status != "refused" || first.Kind != "setup" || first.Reason == "" {
		t.Fatalf("compiler error became test evidence: %+v", first)
	}
	for i, want := range []struct{ status, reason string }{{"failed", "case zero"}, {"passed", "case one"}, {"failed", "other package"}, {"failed", "child module"}, {"failed", "other part"}} {
		repositoryResult582(t, r, f.plan.Tests[i+1], want.status, want.reason)
	}
	if status484(t, r, "api", "build") != "failed" || status484(t, r, "api", "test") != "failed" {
		t.Fatal("lost earlier real build/suite failures")
	}
	repositoryRoundTrip582(t, r)
	// An unrelated broken package fails the broad build and suite but cannot
	// prevent either exact healthy selector from proving its own assertion.
	f = newRepositoryFixture582(t, cache, []repositoryCase582{
		{"api", "nested", "TestReady", repositorySource582(`t.Fatal("first independent")`, "")},
		{"web", "nested", "TestReady", repositorySource582(`t.Fatal("later independent")`, "")},
	}, true, map[string]string{"api/unrelated/broken.go": "package unrelated\nvar Broken=Missing582\n"}, nil)
	r, problem, err := f.run(t.Context(), true)
	if err != nil || problem != "" {
		t.Fatalf("unrelated build/suite failure broadcast to selectors: %v %s", err, problem)
	}
	for i, test := range f.plan.Tests {
		repositoryResult582(t, r, test, "failed", []string{"first independent", "later independent"}[i])
	}
	// Candidates must also satisfy every applicable command, not merely an
	// acceptance pass. Each failing command is a real Go process exit.
	for _, phase := range []string{"format", "lint", "build", "test"} {
		t.Run("candidate "+phase, func(t *testing.T) {
			c := repositoryControl582()
			g := newRepositoryFixture582(t, cache, []repositoryCase582{c}, true, nil, func(config *repository.Config) {
				config.Parts[0].Commands[phase] = repository.Command{Argv: []string{"go", "run", "missing582.go"}}
			})
			g.candidateValue(7)
			r, _, _ := g.run(t.Context(), false)
			if r.Passed() || status484(t, r, "api", phase) != "failed" {
				t.Fatal("candidate command failure did not veto")
			}
			repositoryResult582(t, r, g.plan.Tests[0], "not_run", "")
		})
	}
}
