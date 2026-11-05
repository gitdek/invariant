package plumbing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

// Existing #515 adapters do not acquire an additional mandatory method.
// For an adapter exposing Discover with the #527 signature, the lifecycle
// starts/reuses its test phase, discovers before requesting runtime evidence,
// validates the returned binding (only Package can change), then supplies that
// binding to any later collector. Discovery errors retain setup/refusal reasons
// and cannot qualify either path. No evidence collector is added by this plan.
type observeGoAdapter527 struct {
	GoRepositoryAcceptanceAdapter
	selectedEnd    time.Time
	selectionDelay time.Duration
	inputs         []RepositoryAcceptanceInvocation
	discovered     []RepositoryAcceptanceDiscovery
	ends           []time.Time
}

func (a *observeGoAdapter527) Select(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
	a.selectedEnd, _ = ctx.Deadline()
	if a.selectionDelay > 0 {
		select {
		case <-time.After(a.selectionDelay):
		case <-ctx.Done():
			return RepositoryAcceptanceSelection{}, ctx.Err()
		}
	}
	return a.GoRepositoryAcceptanceAdapter.Select(ctx, part, test)
}

func (a *observeGoAdapter527) Discover(ctx context.Context, part repository.Part, in RepositoryAcceptanceInvocation, environment RepositoryCheckEnvironment) (RepositoryAcceptanceDiscovery, error) {
	a.inputs = append(a.inputs, in)
	end, _ := ctx.Deadline()
	a.ends = append(a.ends, end)
	got, err := a.GoRepositoryAcceptanceAdapter.Discover(ctx, part, in, environment)
	a.discovered = append(a.discovered, got)
	return got, err
}

type goRunner527 struct {
	*runner484
	parts        map[string]repository.Part
	cache        string
	environments []*goEnvironment527
	before       func(context.Context, []string) error
	suite        func(context.Context) error
}

func (r *goRunner527) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	tracked, err := r.runner484.OpenChecks(ctx, q)
	if err != nil {
		return tracked, err
	}
	part := r.parts[q.Directory]
	env := goEnvironmentFor527(r.t, q.SourceDir, part, r.cache)
	env.close, env.before = tracked.Close, r.before
	r.environments = append(r.environments, env)
	return &goLifecycleEnvironment527{goEnvironment527: env, suite: r.suite}, nil
}

type goLifecycleEnvironment527 struct {
	*goEnvironment527
	suite func(context.Context) error
}

func (e *goLifecycleEnvironment527) Run(ctx context.Context, argv []string) ([]byte, error) {
	if len(argv) == 1 && argv[0] == "suite-fixture" && e.suite != nil {
		return nil, e.suite(ctx) // Only suite scheduling is simulated, never Go discovery.
	}
	return e.goEnvironment527.Run(ctx, argv)
}

type goFixture527 struct {
	root, candidate, module string
	part                    repository.Part
	snapshot                *repository.Snapshot
	plan                    *Plan
	sandbox                 Sandbox
	runner                  *goRunner527
	adapter                 *observeGoAdapter527
}

func newGoFixture527(t *testing.T, cache string, nested, suite bool) *goFixture527 {
	t.Helper()
	dirs := []string{"api"}
	if nested {
		dirs = append(dirs, "api/child")
	}
	var parts []repository.Part
	for _, dir := range dirs {
		p := goPart527("go", "test", "./...", "-run", "^{test}$", "-count=1")
		p.Name, p.Directory = dir, dir
		p.Inputs = []string{dir + "/go.mod"}
		p.Lockfiles = []string{dir + "/go.sum"}
		for _, phase := range repositoryPhases {
			p.Commands[phase] = repository.Command{NotApplicable: "Discovery fixture has no applicable " + phase + " command."}
		}
		if suite {
			p.Commands["test"] = repository.Command{Argv: []string{"suite-fixture"}}
		}
		parts = append(parts, p)
	}
	root, _, _ := fixture484(t, parts, nil)
	for _, p := range parts {
		write468(t, root, p.Directory+"/go.mod", "module example.invalid/"+strings.ReplaceAll(p.Directory, "/", "_")+"527\n\ngo 1.23\n")
		write468(t, root, p.Directory+"/go.sum", "")
	}
	part := parts[len(parts)-1]
	module := "example.invalid/" + strings.ReplaceAll(part.Directory, "/", "_") + "527"
	write468(t, root, part.Directory+"/nested/value.go", "package fixture\nconst Value = 7\n")
	// The ancestor part has an identical name; ownership must still select the
	// child module and its package, never the ancestor's declaration.
	write468(t, root, "api/elsewhere/other_test.go", "package elsewhere\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n")
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-q", "--no-verify", "-m", "standard library discovery fixture")
	snapshot, err := repository.Load(context.Background(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	candidate := t.TempDir()
	if err := CopyCheckout(context.Background(), root, candidate); err != nil {
		t.Fatal(err)
	}
	file := part.Directory + "/nested/ready_accept_test.go"
	source := "package fixture\r\nimport \"testing\"\r\nfunc TestReady(t *testing.T) { if Value != 7 { t.Fatal(Value) } }\r\n"
	test := goTest527(file, "TestReady/missing.[x]/--literal")
	test.Part = part.Name
	p := &Plan{Name: "discover only", Summary: "Discover one declaration without runtime evidence.", Files: []string{part.Directory + "/nested/value.go"}, Tests: []Test{test}, Sources: map[string]string{file: source}}
	p = n503Read(t, p, snapshot)
	runner := &goRunner527{runner484: newRunner484(t), cache: cache, parts: map[string]repository.Part{}}
	for _, p := range parts {
		runner.parts[p.Directory] = p
	}
	s := sandbox484(t, root, snapshot, runner.runner484)
	s.Timeout, s.ShardTimeout = 90*time.Second, 60*time.Second
	s.Repository.Dependencies.Runner = runner
	a := &observeGoAdapter527{}
	s.Repository.Adapters = map[string]RepositoryAcceptanceAdapter{"go": a}
	return &goFixture527{root, candidate, module, part, snapshot, p, s, runner, a}
}

func TestGoAcceptanceLifecycleDiscoversWithoutCompleting(t *testing.T) {
	cache := goCache527(t)
	for _, nested := range []bool{false, true} {
		f := newGoFixture527(t, cache, nested, false)
		file, source := f.plan.Tests[0].File, f.plan.Sources[f.plan.Tests[0].File]
		// A normalized candidate and candidate-controlled registration cannot
		// replace the ratified source or captured adapter/policy.
		write468(t, f.candidate, file, strings.ReplaceAll(source, "\r\n", "\n"))
		write468(t, f.candidate, repository.Path, `{"adapters":{"go":"candidate-wrapper"}}`)
		f.runner.openHook = func(_ context.Context, q DependencyRequest) error {
			if q.SourceDir == f.root || q.SourceDir == f.candidate || read484(t, q.SourceDir, file) != source || q.Directory != f.part.Directory {
				t.Error("discovery lost private verbatim staging or deepest ownership")
			}
			if q.Network || q.Pull || q.Env["GOPROXY"] != "off" || q.Env["GOTOOLCHAIN"] != "local" || q.Env["GOWORK"] != "off" {
				t.Error("discovery opened an online/unpinned environment")
			}
			return nil
		}
		result, _ := f.sandbox.Run(context.Background(), f.candidate, f.plan)
		if len(f.adapter.discovered) != 1 || len(f.runner.opens) != 1 {
			t.Fatalf("lifecycle never used production discovery: %s", result.Summary())
		}
		in := f.adapter.inputs[0]
		assertDiscovery527(t, f.adapter.discovered[0], in, f.module+"/nested")
		if in.Source != source || in.Binding.SourceDigest != hash468(source) || in.Binding.ExecutionDigest != f.plan.Execution.Digest || in.Binding.Part != f.part.Name || in.Binding.InvocationID == "" {
			t.Fatalf("discovery lost captured authority: %+v", in)
		}
		got := result515(t, result, f.plan.Tests[0])
		if result.Passed() || result.Accepted[f.plan.Tests[0].Identity()] || got.Status == "passed" || got.Status == "failed" || got.Reason == "" || got.Package != f.module+"/nested" {
			t.Fatalf("selection became completion or lost discovered attribution: %+v", got)
		}
		closed484(t, f.runner.runner484)
		if read484(t, f.candidate, file) != strings.ReplaceAll(source, "\r\n", "\n") {
			t.Fatal("private discovery rewrote candidate bytes")
		}
		if _, err := os.Stat(filepath.Join(f.root, file)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("discovery wrote the original checkout")
		}
		checked, err := Check(context.Background(), n503Draft(t, f.plan), f.root, f.sandbox)
		if err != nil || checked == nil || checked.Problem == "" || len(f.runner.opens) != 2 {
			t.Fatal("public planning treated discovery as completed evidence")
		}
		// The lower-level base path also cannot qualify discovery as a failure.
		base, _ := f.sandbox.checkRepositoryBase(context.Background(), f.root, f.plan)
		if base == nil || base.Problem == "" || base.Run.Passed() {
			t.Fatal("discovery qualified a base without runtime evidence")
		}
		if len(f.adapter.inputs) != 3 || f.adapter.inputs[1].Binding.InvocationID == in.Binding.InvocationID || f.adapter.inputs[2].Binding.InvocationID == f.adapter.inputs[1].Binding.InvocationID {
			t.Fatal("discovery reused invocation identity")
		}
		closed484(t, f.runner.runner484)
		// Default discovery also needs a runtime evidence collector.
		f.sandbox.Repository.Adapters = nil
		opens, installs := len(f.runner.opens), len(f.runner.installs)
		refused, _ := f.sandbox.Run(context.Background(), f.candidate, f.plan)
		if refused.Passed() || len(f.runner.opens) != opens+1 || len(f.runner.installs) != installs {
			t.Fatal("default Go discovery supplied completion or lost dependency reuse")
		}
	}
}

func TestGoAcceptanceDiscoveryUsesSharedAllowances(t *testing.T) {
	for _, suite := range []bool{false, true} {
		f := newGoFixture527(t, goCache527(t), false, suite)
		f.sandbox.Timeout, f.sandbox.ShardTimeout = 5*time.Second, 250*time.Millisecond
		f.adapter.selectionDelay = 70 * time.Millisecond
		var phaseEnd time.Time
		observe := func(ctx context.Context) {
			d := deadline515(t, ctx)
			if phaseEnd.IsZero() {
				phaseEnd = d
			} else if !phaseEnd.Equal(d) {
				t.Error("discovery renewed the shared phase allowance")
			}
			if d.After(f.adapter.selectedEnd) || d.After(time.Now().Add(f.sandbox.ShardTimeout)) {
				t.Error("discovery extended an inherited allowance")
			}
		}
		f.runner.suite = func(ctx context.Context) error { observe(ctx); return nil }
		f.runner.before = func(ctx context.Context, _ []string) error {
			observe(ctx)
			<-ctx.Done() // Interrupted discovery supplies no invented Go response.
			return ctx.Err()
		}
		started := time.Now()
		result, _ := f.sandbox.Run(context.Background(), f.candidate, f.plan)
		if len(f.adapter.ends) != 1 || phaseEnd.IsZero() || !phaseEnd.Equal(f.adapter.ends[0]) {
			t.Fatal("discovery did not join the existing test phase")
		}
		if f.adapter.selectedEnd.IsZero() || f.adapter.selectedEnd.After(started.Add(5*time.Second+50*time.Millisecond)) {
			t.Fatal("command validation started a later whole-run allowance")
		}
		for _, d := range f.runner.deadlines {
			if !d.Equal(f.adapter.selectedEnd) {
				t.Error("preparation renewed time consumed by command validation")
			}
		}
		got := result515(t, result, f.plan.Tests[0])
		if result.Passed() || !result.TimedOut || result.DeadlineExceeded || result.Interrupted || got.Status == "passed" || got.Status == "failed" || got.Reason == "" || got.Package != "" {
			t.Fatalf("interrupted discovery left a selection/completion claim or lost its reason: %s", result.Summary())
		}
		closed484(t, f.runner.runner484)
	}
}
