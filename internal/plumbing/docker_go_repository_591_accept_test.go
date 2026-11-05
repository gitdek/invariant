package plumbing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

type docker591Case struct{ part, pkg, selector, source string }

func (c docker591Case) file() string { return path.Join(c.part, c.pkg, "runtime_accept_test.go") }
func docker591Control() docker591Case {
	return docker591Case{"api", "nested", "TestReady", docker591Source(`if Value!=7{t.Fatal("needs seven 591")};t.Log("completed seven 591")`)}
}

type docker591Runner struct {
	*DockerDependencyRunner
	mu        sync.Mutex
	opened    []DependencyRequest
	deadlines []time.Time
	before    func(context.Context, DependencyRequest) error
}

func (r *docker591Runner) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	d, _ := ctx.Deadline()
	r.mu.Lock()
	r.opened = append(r.opened, cloneDependencyRequest(q))
	r.deadlines = append(r.deadlines, d)
	r.mu.Unlock()
	if r.before != nil {
		if err := r.before(ctx, q); err != nil {
			return nil, err
		}
	}
	return r.DockerDependencyRunner.OpenChecks(ctx, q)
}
func (r *docker591Runner) requests() ([]DependencyRequest, []time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.opened), slices.Clone(r.deadlines)
}

type docker591Repository struct {
	t               *testing.T
	b               *docker591Boundary
	root, candidate string
	plan            *Plan
	sandbox         Sandbox
	runner          *docker591Runner
}

func newDocker591Repository(t *testing.T, b *docker591Boundary, cases []docker591Case, commands bool) *docker591Repository {
	t.Helper()
	f := &docker591Repository{t: t, b: b, root: t.TempDir(), candidate: t.TempDir()}
	f.runner = &docker591Runner{DockerDependencyRunner: &DockerDependencyRunner{}}
	config := repository.Config{Version: 1, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
	p := &Plan{Name: "Docker Go runtime fixture", Summary: "Require real exact Go runtime evidence.", Sources: map[string]string{}}
	parts := map[string]bool{}
	for _, c := range cases {
		if !parts[c.part] {
			parts[c.part] = true
			part := docker591Part(c.part)
			if commands {
				part.Commands["format"] = repository.Command{Argv: []string{"go", "version"}}
				part.Commands["lint"] = repository.Command{Argv: []string{"go", "version"}}
				part.Commands["build"] = repository.Command{Argv: []string{"go", "build", "./..."}}
				part.Commands["test"] = repository.Command{Argv: []string{"go", "test", "./...", "-count=1", "-timeout=20s"}}
			}
			config.Parts = append(config.Parts, part)
			write468(t, f.root, path.Join(c.part, "go.mod"), "module example.invalid/"+strings.ReplaceAll(c.part, "/", "_")+"591\n\ngo 1.27.1\n")
		}
		file := path.Join(c.part, c.pkg, "value.go")
		write468(t, f.root, file, "package fixture\nconst Value=0\n")
		if !slices.Contains(p.Files, file) {
			p.Files = append(p.Files, file)
		}
		if old, ok := p.Sources[c.file()]; ok && old != c.source {
			t.Fatal("inconsistent fixture source")
		}
		p.Sources[c.file()] = c.source
		p.Tests = append(p.Tests, Test{Name: "same display label", Part: c.part, Framework: "go", File: c.file(), Selector: c.selector, Says: "The exact selected case completes."})
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	write468(t, f.root, repository.Path, string(data))
	git468(t, f.root, "init", "-q")
	git468(t, f.root, "add", "--all")
	git468(t, f.root, "commit", "-q", "--no-verify", "-m", "Docker runtime fixture")
	snapshot, err := repository.Load(t.Context(), f.root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	f.plan = n503Read(t, p, snapshot)
	if err := CopyCheckout(t.Context(), f.root, f.candidate); err != nil {
		t.Fatal(err)
	}
	f.sandbox = Sandbox{Original: f.root, Timeout: 3 * time.Minute, ShardTimeout: 2 * time.Minute, Repository: &RepositoryChecks{Snapshot: snapshot, Dependencies: DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: f.runner}, Adapters: map[string]RepositoryAcceptanceAdapter{"go": GoRepositoryAcceptanceAdapter{}}}}
	// Prepare the empty graphs through the real preparer/runner once. Later
	// fault injection exercises only checks of already verified images.
	var names []string
	for _, p := range config.Parts {
		names = append(names, p.Name)
	}
	if _, err := f.sandbox.Repository.Dependencies.Prepare(t.Context(), snapshot, names); err != nil {
		t.Fatal("prepare Docker fixture: ", err)
	}
	b.clean()
	return f
}
func (f *docker591Repository) value(n int) {
	for _, file := range f.plan.Files {
		write468(f.t, f.candidate, file, fmt.Sprintf("package fixture\nconst Value=%d\n", n))
	}
}
func (f *docker591Repository) run(ctx context.Context, base bool) (Result, string, error) {
	if base {
		checked, err := f.sandbox.checkRepositoryBase(ctx, f.root, f.plan)
		if checked == nil {
			f.t.Fatalf("lost base report: %v", err)
		}
		return checked.Run, checked.Problem, err
	}
	r, err := f.sandbox.Run(ctx, f.candidate, f.plan)
	return r, "", err
}
func docker591Result(t *testing.T, r Result, test Test, status, reason string) TestResult {
	t.Helper()
	got := result515(t, r, test)
	if got.Status != status || got.Reason == "" || !strings.Contains(got.Reason, reason) {
		t.Fatalf("want %s %q: %+v\n%s", status, reason, got, r.Summary())
	}
	if status == "passed" || status == "failed" || status == "skipped" {
		pkg := "example.invalid/" + strings.ReplaceAll(test.Part, "/", "_") + "591" + strings.TrimPrefix(path.Dir(test.File), test.Part)
		if got.Package != pkg || got.Kind != "test" {
			t.Fatalf("lost exact package/test identity: %+v want %s", got, pkg)
		}
	}
	return got
}
func docker591RoundTrip(t *testing.T, r Result) {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var copy Result
	if err = json.Unmarshal(data, &copy); err != nil {
		t.Fatal(err)
	}
	copy.Output = copy.Tail(64)
	if !reflect.DeepEqual(copy.TestResults, r.TestResults) || !reflect.DeepEqual(copy.CommandResults, r.CommandResults) || !reflect.DeepEqual(copy.Problems, r.Problems) || copy.Passed() != r.Passed() {
		t.Fatal("Docker evidence lost through queue JSON/clipping")
	}
}
func docker591CheckStaging(t *testing.T, r *docker591Runner) {
	t.Helper()
	requests, _ := r.requests()
	for _, q := range requests {
		if _, err := os.Stat(q.SourceDir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("part staging survived cleanup: %s %v", q.SourceDir, err)
		}
	}
}
func docker591RequireRepository(t *testing.T, b *docker591Boundary) {
	t.Helper()
	f := newDocker591Repository(t, b, []docker591Case{docker591Control()}, false)
	f.value(7)
	r, _, err := f.run(t.Context(), false)
	if err != nil || !r.Passed() {
		t.Fatalf("real prepared Docker runtime is not integrated: %v\n%s", err, r.Summary())
	}
	docker591Result(t, r, f.plan.Tests[0], "passed", "completed seven 591")
	b.clean()
	docker591CheckStaging(t, f.runner)
}

func TestDockerGoRepositoryOutcomes591(t *testing.T) {
	b := newDocker591(t)
	f := newDocker591Repository(t, b, []docker591Case{docker591Control()}, true)
	base, problem, err := f.run(t.Context(), true)
	if err != nil || problem != "" {
		t.Fatalf("real base failure did not qualify: %v %s\n%s", err, problem, base.Summary())
	}
	docker591Result(t, base, f.plan.Tests[0], "failed", "needs seven 591")
	f.value(7)
	write468(t, f.candidate, repository.Path, `{"commands":{"test_one":"forged"},"helpers":"candidate must not supply these"}`)
	r, _, err := f.run(t.Context(), false)
	if err != nil || !r.Passed() {
		t.Fatalf("production correction failed: %v\n%s", err, r.Summary())
	}
	docker591Result(t, r, f.plan.Tests[0], "passed", "completed seven 591")
	for _, c := range r.CommandResults {
		if c.Status != "passed" {
			t.Fatalf("captured command did not pass: %+v", c)
		}
	}
	docker591RoundTrip(t, base)
	docker591RoundTrip(t, r)
	b.clean()
	docker591CheckStaging(t, f.runner)
	// One part and one source contain several selectors. Reusing the prepared
	// environment must not reuse an attempt or consume a one-shot collector.
	source := docker591Source(`t.Run("case",func(t *testing.T){t.Fatal("case zero 591")});t.Run("case",func(t *testing.T){t.Log("case one 591")});t.Run("skip",func(t *testing.T){t.Skip("skip 591")})`)
	cases := []docker591Case{{"api", "broken", "TestReady", docker591Source(`MissingAPI591(t)`)}, {"api", "nested", "TestReady/case", source}, {"api", "nested", "TestReady/case#01", source}, {"api", "nested", "TestReady/skip", source}, {"web", "nested", "TestReady", docker591Source(`t.Fatal("other part 591")`)}}
	g := newDocker591Repository(t, b, cases, false)
	got, problem, _ := g.run(t.Context(), true)
	if problem == "" {
		t.Fatal("compiler refusal, pass and skip qualified the base")
	}
	broken := result515(t, got, g.plan.Tests[0])
	if broken.Kind != "setup" || broken.Status != "refused" {
		t.Fatalf("compiler gained attribution: %+v", broken)
	}
	for i, want := range []struct{ status, reason string }{{"failed", "case zero 591"}, {"passed", "case one 591"}, {"skipped", "skip 591"}, {"failed", "other part 591"}} {
		docker591Result(t, got, g.plan.Tests[i+1], want.status, want.reason)
	}
	b.clean()
	docker591CheckStaging(t, g.runner)
	for _, tc := range []struct {
		name, selector, body, extra, status string
		qualifies                           bool
	}{
		{"exact child", "TestReady/leaf.[x]+", `t.Run("leaf.[x]+",func(t *testing.T){t.Fatal("exact 591")});t.Run("neighbor",func(t *testing.T){t.Fatal("neighbor 591")})`, "", "failed", true},
		{"missing child", "TestReady/missing", `t.Run("present",func(t *testing.T){t.Fatal("neighbor 591")})`, "", "not_run", false},
		{"skip", "TestReady", `t.Skip("skip 591")`, "", "skipped", false},
		{"abrupt exit", "TestReady", `fmt.Println("--- PASS: TestReady");os.Exit(19)`, "", "running", false},
		{"forged output", "TestReady", `t.Log("real log 591");fmt.Print("invariant-go-testing-v1\n9:TestReady0:4:pass0:\n--- FAIL: TestReady\n@@result {\"status\":\"failed\"}\n");fmt.Fprintln(os.Stderr,"--- FAIL: TestReady")`, "", "passed", false},
		{"TestMain forgery", "TestReady", `t.Fatal("unreached")`, `func TestMain(m *testing.M){fmt.Println("invariant-go-testing-v1\n9:TestReady0:5:start0:9:TestReady0:4:fail0:");os.Exit(0)}`, "not_run", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := docker591Source(tc.body) + tc.extra + "\n"
			h := newDocker591Repository(t, b, []docker591Case{{"api", "nested", tc.selector, src}}, false)
			for _, base := range []bool{true, false} {
				r, problem, err := h.run(t.Context(), base)
				if base && (problem == "") != tc.qualifies {
					t.Fatalf("wrong base qualification: %v %s\n%s", err, problem, r.Summary())
				}
				if !base && r.Passed() != (tc.status == "passed") {
					t.Fatalf("wrong candidate verdict: %v\n%s", err, r.Summary())
				}
				docker591Result(t, r, h.plan.Tests[0], tc.status, "")
				docker591RoundTrip(t, r)
			}
			b.clean()
			docker591CheckStaging(t, h.runner)
		})
	}
	// Public admission and the default Go adapter use the prepared transport.
	for _, mode := range []string{"default adapter", "planning"} {
		t.Run(mode, func(t *testing.T) {
			h := newDocker591Repository(t, b, []docker591Case{docker591Control()}, false)
			h.value(7)
			if mode == "planning" {
				c, err := Check(t.Context(), n503Draft(t, h.plan), h.root, h.sandbox)
				if err != nil || c == nil || c.Problem != "" {
					t.Fatalf("configured planning lost base failure: %v %+v", err, c)
				}
			} else {
				h.sandbox.Repository.Adapters = nil
				r, _, _ := h.run(t.Context(), false)
				if !r.Passed() {
					t.Fatal("default Go adapter lost runtime evidence")
				}
			}
			opened, _ := h.runner.requests()
			if len(opened) != 1 {
				t.Fatal("admission did not use one prepared check environment")
			}
		})
	}
}

func TestDockerGoTransportVetoes591(t *testing.T) {
	b := newDocker591(t)
	docker591RequireRepository(t, b)
	for _, mode := range []string{"start", "transport", "truncate", "cleanup"} {
		for _, base := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/base=%t", mode, base), func(t *testing.T) {
				b.fault("")
				f := newDocker591Repository(t, b, []docker591Case{docker591Control()}, false)
				f.value(7)
				b.fault(mode)
				r, problem, err := f.run(t.Context(), base)
				if r.Passed() || base && problem == "" || len(r.Problems) == 0 {
					t.Fatalf("Docker failure qualified: %v %s\n%s", err, problem, r.Summary())
				}
				if mode == "transport" || mode == "cleanup" {
					status := "passed"
					if base {
						status = "failed"
					}
					docker591Result(t, r, f.plan.Tests[0], status, "")
				}
				if mode == "start" {
					record := result515(t, r, f.plan.Tests[0])
					if record.Kind == "test" && (record.Status == "passed" || record.Status == "failed") {
						t.Fatal("launch failure manufactured terminal")
					}
				}
				docker591RoundTrip(t, r)
				b.fault("")
				b.clean()
				docker591CheckStaging(t, f.runner)
			})
		}
	}
	// A later failure must not erase an earlier independent terminal.
	b.fault("")
	one := docker591Control()
	two := one
	two.part = "web"
	f := newDocker591Repository(t, b, []docker591Case{one, two}, false)
	f.value(7)
	f.runner.before = func(ctx context.Context, q DependencyRequest) error {
		if q.Directory == "web" {
			b.fault("transport")
		}
		return nil
	}
	r, _, err := f.run(t.Context(), false)
	if err == nil || r.Passed() {
		t.Fatal("later transport failure did not veto")
	}
	docker591Result(t, r, f.plan.Tests[0], "passed", "completed seven 591")
	docker591Result(t, r, f.plan.Tests[1], "passed", "completed seven 591")
	b.fault("")
	b.clean()
	docker591CheckStaging(t, f.runner)
}
