package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

// Only the environment boundary is simulated here. Real selection ordering,
// captured preparation, staging, result validation, deadlines and cleanup run.
// Each environment has a writable image filesystem as well as its source tree.
type isolationRunner621 struct {
	*runner484
	t          *testing.T
	base       bool
	envs       []*isolationEnvironment621
	accepted   []string
	discovered []string
}

type isolationEnvironment621 struct {
	RepositoryCheckEnvironment
	runner      *isolationRunner621
	request     DependencyRequest
	imageRoot   string
	closed      bool
	discoveryID string
}

var imageFiles621 = []string{"bin/go", "bin/python3", "lib/pytest.py", "cache/compiler", "lib/sitecustomize.py"}

func (r *isolationRunner621) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	tracked, err := r.runner484.OpenChecks(ctx, q)
	if err != nil {
		return tracked, err
	}
	e := &isolationEnvironment621{RepositoryCheckEnvironment: tracked, runner: r, request: q, imageRoot: r.t.TempDir()}
	for _, name := range imageFiles621 {
		write468(r.t, e.imageRoot, name, "immutable prepared bytes\n")
	}
	r.envs = append(r.envs, e)
	return e, nil
}

func (e *isolationEnvironment621) poison() {
	for _, name := range imageFiles621 {
		write468(e.runner.t, e.imageRoot, name, "earlier workload replacement\n")
	}
	write468(e.runner.t, e.request.SourceDir, "app/main.go", "earlier workload changed production\n")
	write468(e.runner.t, e.request.SourceDir, "app/generated621", "earlier build output\n")
}

func (e *isolationEnvironment621) pristine() error {
	for _, name := range imageFiles621 {
		if got := read484(e.runner.t, e.imageRoot, name); got != "immutable prepared bytes\n" {
			return fmt.Errorf("later invocation trusted earlier workload's %s", name)
		}
	}
	if got := read484(e.runner.t, e.request.SourceDir, "app/main.go"); got != "package fixture\n" {
		return errors.New("later invocation inherited modified production inputs")
	}
	if _, err := os.Stat(filepath.Join(e.request.SourceDir, "app/generated621")); !errors.Is(err, os.ErrNotExist) {
		return errors.New("later invocation inherited generated files")
	}
	return nil
}

func (e *isolationEnvironment621) Run(ctx context.Context, argv []string) ([]byte, error) {
	// The existing fake insists that the suite sees its own build output.
	out, err := e.RepositoryCheckEnvironment.Run(ctx, argv)
	if err == nil && len(argv) > 1 && argv[1] == "test" {
		e.poison()
	}
	return out, err
}

func (e *isolationEnvironment621) RunAcceptance(ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
	if err := e.pristine(); err != nil {
		return RepositoryAcceptanceEvidence{}, nil, err
	}
	if e.closed || e.discoveryID != in.Binding.InvocationID {
		return RepositoryAcceptanceEvidence{}, nil, errors.New("discovery and execution must belong to the same live invocation")
	}
	if got := read484(e.runner.t, e.request.SourceDir, in.Binding.File); got != in.Source || hash468(got) != in.Binding.SourceDigest {
		return RepositoryAcceptanceEvidence{}, nil, errors.New("invocation lost pristine pinned acceptance bytes")
	}
	e.runner.accepted = append(e.runner.accepted, in.Binding.InvocationID)
	e.poison()
	status := "passed"
	if e.runner.base {
		status = "failed"
	}
	return evidence515(in, "test", status, "authentic isolated completion"), nil, ctx.Err()
}

func (e *isolationEnvironment621) Close(ctx context.Context) error {
	if e.closed {
		return nil
	}
	e.closed = true
	return errors.Join(e.RepositoryCheckEnvironment.Close(ctx), os.RemoveAll(e.imageRoot))
}

type isolationAdapter621 struct{ adapter515 }

func (a *isolationAdapter621) Discover(ctx context.Context, part repository.Part, in RepositoryAcceptanceInvocation, environment RepositoryCheckEnvironment) (RepositoryAcceptanceDiscovery, error) {
	e, ok := environment.(*isolationEnvironment621)
	if !ok {
		return RepositoryAcceptanceDiscovery{}, errors.New("missing test environment")
	}
	if err := e.pristine(); err != nil {
		return RepositoryAcceptanceDiscovery{}, err
	}
	if e.discoveryID != "" || e.closed {
		return RepositoryAcceptanceDiscovery{}, errors.New("discovery reused a workload environment")
	}
	e.discoveryID = in.Binding.InvocationID
	e.runner.discovered = append(e.runner.discovered, in.Binding.InvocationID)
	return RepositoryAcceptanceDiscovery{Binding: in.Binding, TestName: strings.Split(in.Binding.Selector, "/")[0]}, ctx.Err()
}

func fixtureIsolation621(t *testing.T, framework string, commands bool) (Sandbox, string, *Plan, *isolationRunner621) {
	t.Helper()
	language, file, source := "go", "app/isolation_accept_test.go", "package fixture\nimport \"testing\"\nfunc TestFirst(t *testing.T){}\nfunc TestSecond(t *testing.T){}\n"
	selectors := []string{"TestFirst", "TestSecond"}
	if framework == "pytest" {
		language, file, source = "python", "app/test_isolation.py", "def test_first(): pass\ndef test_second(): pass\n"
		selectors = []string{"test_isolation.py::test_first", "test_isolation.py::test_second"}
	} else if framework == "vitest" || framework == "jest" {
		language, file, source = "typescript", "app/isolation.test.ts", "test('first',()=>{}); test('second',()=>{});\n"
		selectors = []string{"first", "second"}
	}
	part := part484(language, "app")
	if !commands {
		for _, phase := range phases484 {
			part.Commands[phase] = repository.Command{NotApplicable: "Only individual selectors."}
		}
	}
	part.Commands["test_one"] = repository.Command{Argv: []string{"trusted-fixture", "{test}", "literal argument; $(not-a-shell)"}}
	root, candidate, snapshot := fixture484(t, []repository.Part{part}, nil)
	p := &Plan{Name: "isolated selectors", Summary: "Exercise the isolation boundary.", Files: []string{"app/main.go"}, Sources: map[string]string{file: source}}
	for _, selector := range selectors {
		p.Tests = append(p.Tests, Test{Name: "same display label", Part: "app", Framework: framework, File: file, Selector: selector, Says: "Independent completion."})
	}
	p = n503Read(t, p, snapshot)
	r := &isolationRunner621{runner484: newRunner484(t), t: t}
	s := sandbox484(t, root, snapshot, r.runner484)
	s.Repository.Dependencies.Runner = r
	s.Repository.Adapters = map[string]RepositoryAcceptanceAdapter{framework: &isolationAdapter621{}}
	return s, candidate, p, r
}

func TestConfiguredInvocationIsolation621(t *testing.T) {
	for _, framework := range []string{"go", "pytest", "vitest", "jest"} {
		for _, commands := range []bool{false, true} {
			for _, base := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/commands=%v/base=%v", framework, commands, base), func(t *testing.T) {
					s, candidate, p, r := fixtureIsolation621(t, framework, commands)
					r.base = base
					var result Result
					if base {
						checked, err := Check(t.Context(), n503Draft(t, p), s.Original, s)
						if err != nil || checked == nil || checked.Problem != "" {
							t.Fatalf("isolated base failed: %v %+v", err, checked)
						}
						result = checked.Run
					} else {
						var err error
						result, err = s.Run(t.Context(), candidate, p)
						if err != nil || !result.Passed() {
							t.Fatalf("isolated candidate failed: %v\n%s", err, result.Summary())
						}
					}
					wantEnvs := len(p.Tests)
					if commands {
						wantEnvs++
					}
					if len(r.envs) != wantEnvs || len(r.accepted) != 2 || r.accepted[0] == r.accepted[1] {
						t.Fatalf("wrong independent invocations: environments=%d accepted=%v", len(r.envs), r.accepted)
					}
					seenTokens, seenStages := map[string]bool{}, map[string]bool{}
					for _, e := range r.envs {
						q := e.request
						if seenTokens[q.Token] || seenStages[q.SourceDir] || q.SourceDir == candidate || q.SourceDir == s.Original || q.Image != r.envs[0].request.Image || q.Network || q.Pull {
							t.Fatal("invocation reused mutable staging or lost captured immutable/offline policy")
						}
						seenTokens[q.Token], seenStages[q.SourceDir] = true, true
						if !e.closed {
							t.Fatal("invocation was not closed")
						}
						if _, err := os.Stat(q.SourceDir); !errors.Is(err, os.ErrNotExist) {
							t.Fatal("private invocation staging survived cleanup")
						}
					}
					closed484(t, r.runner484)
					if read484(t, candidate, "app/main.go") != "package fixture\n" || read484(t, s.Original, "app/main.go") != "package fixture\n" {
						t.Fatal("workload modified an authoritative input tree")
					}
					for _, test := range p.Tests {
						want := "passed"
						if base {
							want = "failed"
						}
						if got := result515(t, result, test); got.Status != want || got.Kind != "test" {
							t.Fatalf("lost authentic evidence: %+v", got)
						}
					}
				})
			}
		}
	}
}

func noCommands621(t *testing.T, f *fixture515) {
	t.Helper()
	recapture515(t, f, func(c *repository.Config) {
		for i := range c.Parts {
			for _, phase := range phases484 {
				c.Parts[i].Commands[phase] = repository.Command{NotApplicable: "Only individual selectors."}
			}
		}
	})
}

func TestInvocationCleanupStopsLaterSelectors621(t *testing.T) {
	f := newFixture515(t)
	noCommands621(t, f)
	allFail515(f.runner)
	closes := 0
	f.runner.closeHook = func(ctx context.Context, q DependencyRequest) error {
		closes++
		if ctx.Err() != nil || time.Until(deadline515(t, ctx)) > SandboxCleanupAllowance+time.Second {
			t.Error("invocation cleanup lost its uncanceled bounded reserve")
		}
		return errors.New("621 invocation cleanup failed")
	}
	c := base515(t, f, t.Context())
	if c.Problem == "" || closes != 1 || len(f.runner.invocations) != 1 || len(f.runner.opens) != 1 {
		t.Fatalf("cleanup failure allowed a later selector: closes=%d invocations=%d opens=%d\n%s", closes, len(f.runner.invocations), len(f.runner.opens), c.Run.Summary())
	}
	if result515(t, c.Run, f.plan.Tests[0]).Status != "failed" || result515(t, c.Run, f.plan.Tests[1]).Status != "not_run" {
		t.Fatal("cleanup failure erased earlier evidence or fabricated later completion")
	}
}

func TestInvocationStartupSharesTestAllowance621(t *testing.T) {
	f := newFixture515(t)
	noCommands621(t, f)
	f.sandbox.ShardTimeout = 300 * time.Millisecond
	var firstEnd time.Time
	opens := 0
	f.runner.openHook = func(ctx context.Context, q DependencyRequest) error {
		opens++
		end := deadline515(t, ctx)
		if opens == 1 {
			firstEnd = end
			if end.After(time.Now().Add(f.sandbox.ShardTimeout + 20*time.Millisecond)) {
				t.Error("first invocation startup is outside the test allowance")
			}
			return nil
		}
		if !end.Equal(firstEnd) {
			t.Error("later startup renewed the test allowance")
		}
		<-ctx.Done()
		return ctx.Err()
	}
	allFail515(f.runner)
	c := base515(t, f, t.Context())
	if c.Problem == "" || !c.Run.TimedOut || opens != 2 || len(f.runner.invocations) != 1 {
		t.Fatalf("later invocation startup did not consume the original allowance: opens=%d invocations=%d\n%s", opens, len(f.runner.invocations), c.Run.Summary())
	}
	if result515(t, c.Run, f.plan.Tests[0]).Status != "failed" || result515(t, c.Run, f.plan.Tests[1]).Status != "not_run" {
		t.Fatal("startup timeout lost completed or unfinished evidence")
	}
	closed484(t, f.runner.runner484)
}
