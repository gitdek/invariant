package plumbing

// Issue #484's execution API:
//
// Sandbox.Repository *RepositoryChecks opts into captured repository checks;
// nil preserves the existing Invariant Go path. RepositoryChecks has Snapshot
// *repository.Snapshot and Dependencies DependencyPreparer. A missing snapshot
// is an error, never permission to fall back or load candidate configuration.
// Each Run selects planned files, acceptance files and admitted regressions
// against Original, using Snapshot.PartsFor. Generated plan-lock paths are not
// changes to select. Selection must finish before any dependency work begins.
//
// DependencyPreparer.Runner retains its existing interface. For this path it
// additionally implements OpenChecks(context.Context, DependencyRequest)
// (RepositoryCheckEnvironment, error); nil uses DockerDependencyRunner, which
// implements this operation. RepositoryCheckEnvironment has Run(context.Context,
// []string) ([]byte, error) and Close(context.Context) error. OpenChecks receives
// a verified immutable environment request with network/pull disabled and the
// existing isolation limits. Each part gets one disposable writable environment
// for format, lint, build and test, in that order. Commands run as argument
// arrays, with exit status deciding success; a formatter must be configured to
// fail when formatting is wrong. Inapplicable phases retain their reason.
//
// The trusted Go host orchestrates commands and records results; it does not run
// repository commands on the host or assume Python exists in the pinned image.
// Parts run serially, keeping at most one candidate container active per check.
// The whole-run clock starts at Sandbox.Run entry (queue admission when queued),
// covers preparation too, and is capped by SandboxTimeout and the parent.
// The test clock starts at the first test command and is shared, without reset,
// across later parts/commands; later setup also consumes time after that point.
// Retain the existing startup/cleanup reserves and clamp overrides as before.
//
// Result.CommandResults []CommandResult records Part, Phase, Status for every
// selected format/lint/build/test, including passed, failed, skipped, running
// or not_run. A failure stops the run, leaving subsequent work not_run. Summary
// names the relevant part/phase and inapplicable reasons. Diagnostic text stays
// diagnostic: it is not fed through the legacy marker parser. Individual native
// acceptance evidence is deliberately not provided here: suite success alone
// never satisfies an Accepted entry. Existing Go TestResults remain unchanged.
// Ordinary command failures/cancellation return a failed Result with diagnostics;
// configuration/infrastructure errors may additionally return an error.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

var phases484 = []string{"format", "lint", "build", "test"}

type call484 struct {
	request  DependencyRequest
	argv     []string
	deadline time.Time
}

type runner484 struct {
	*runner468 // Existing preparation fake: production still selects and verifies dependencies.
	mu484      sync.Mutex
	opens      []DependencyRequest
	calls      []call484
	closed     []string
	active     int
	maxActive  int
	hook       func(context.Context, DependencyRequest, []string) ([]byte, error)
	openHook   func(context.Context, DependencyRequest) error
	closeHook  func(context.Context, DependencyRequest) error
}

func newRunner484(t *testing.T) *runner484 { return &runner484{runner468: newRunner468(t)} }

// Checks must use the persistent candidate environment, not silently lose the
// build's outputs by creating a fresh disposable container for every command.
func (r *runner484) Run(context.Context, DependencyRequest) ([]byte, error) {
	return nil, errors.New("one-shot dependency consumer cannot retain build outputs")
}

func (r *runner484) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	secureRequest468(r.t, q, false)
	r.mu.Lock()
	_, verified := r.images[q.Image]
	r.mu.Unlock()
	if !verified {
		return nil, fmt.Errorf("check did not use a prepared immutable environment: %s", q.Image)
	}
	r.mu484.Lock()
	r.opens = append(r.opens, q)
	r.mu484.Unlock()
	if r.openHook != nil {
		if err := r.openHook(ctx, q); err != nil {
			return nil, err
		}
	}
	r.mu484.Lock()
	r.active++
	r.maxActive = max(r.active, r.maxActive)
	r.mu484.Unlock()
	return &environment484{runner: r, request: q}, nil
}

type environment484 struct {
	runner  *runner484
	request DependencyRequest
	built   bool
	closed  bool
}

func (e *environment484) Run(ctx context.Context, argv []string) ([]byte, error) {
	r := e.runner
	d, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("check command has no deadline")
	}
	r.mu484.Lock()
	r.calls = append(r.calls, call484{e.request, slices.Clone(argv), d})
	r.mu484.Unlock()
	if e.closed {
		return nil, errors.New("used a closed environment")
	}
	if r.hook != nil {
		return r.hook(ctx, e.request, argv)
	}
	if len(argv) < 2 {
		return nil, errors.New("fixture received an undeclared command")
	}
	if argv[1] == "build" {
		e.built = true
	}
	if argv[1] == "test" && !e.built {
		return nil, errors.New("test lost this part's build output")
	}
	return []byte("diagnostic from " + e.request.Directory + " " + argv[1] + "\n"), nil
}

func (e *environment484) Close(ctx context.Context) error {
	r := e.runner
	d, ok := ctx.Deadline()
	if ctx.Err() != nil || !ok || time.Until(d) > SandboxCleanupAllowance+time.Second {
		r.t.Error("check cleanup lacks a fresh bounded context")
	}
	if r.closeHook != nil {
		if err := r.closeHook(ctx, e.request); err != nil {
			return err
		}
	}
	r.mu484.Lock()
	defer r.mu484.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	r.closed = append(r.closed, e.request.Token)
	r.active--
	return nil
}

func part484(language, dir string) repository.Part {
	p := part468(language, dir)
	p.Name = dir
	p.Image = "registry.example/" + language + "@" + hash468(language)
	for _, phase := range phases484 {
		p.Commands[phase] = repository.Command{Argv: []string{"check-" + language, phase, "literal argument; $(not-a-shell)"}}
	}
	return p
}

func fixture484(t *testing.T, parts []repository.Part, change func(*repository.Config)) (string, string, *repository.Snapshot) {
	t.Helper()
	root := t.TempDir()
	git468(t, root, "init", "-q")
	c := repository.Config{Version: 1, Parts: parts, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
	if change != nil {
		change(&c)
	}
	for _, p := range parts {
		for f, data := range files468(p.Language, p.Directory) {
			write468(t, root, f, data)
		}
		write468(t, root, p.Directory+"/main.go", "package fixture\n")
		write468(t, root, p.Directory+"/main.ts", "export const value = 1;\n")
	}
	write468(t, root, "api/existing_test.go", "package fixture\n")
	write468(t, root, "AGENTS.md", "fixture rules\n")
	write468(t, root, "STATE.md", "fixture state\n")
	write468(t, root, "shared/settings.json", "{}\n")
	write468(t, root, "shared/input.txt", "captured dependency input\n")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	write468(t, root, repository.Path, string(data))
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-q", "--no-verify", "-m", "captured checks")
	snapshot, err := repository.Load(context.Background(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	candidate := t.TempDir()
	if err := CopyCheckout(context.Background(), root, candidate); err != nil {
		t.Fatal(err)
	}
	return root, candidate, snapshot
}

func sandbox484(t *testing.T, root string, snapshot *repository.Snapshot, r *runner484) Sandbox {
	t.Helper()
	return Sandbox{Original: root, Timeout: 8 * time.Second, ShardTimeout: 4 * time.Second,
		Repository: &RepositoryChecks{Snapshot: snapshot, Dependencies: DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: r}}}
}

func status484(t *testing.T, result Result, part, phase string) string {
	t.Helper()
	found := ""
	for _, command := range result.CommandResults {
		if command.Part == part && command.Phase == phase {
			if found != "" {
				t.Fatalf("duplicate command result for %s/%s: %+v", part, phase, result.CommandResults)
			}
			found = command.Status
		}
	}
	if found == "" {
		t.Fatalf("missing command result for %s/%s: %+v", part, phase, result.CommandResults)
	}
	return found
}

func closed484(t *testing.T, r *runner484) {
	t.Helper()
	r.mu484.Lock()
	defer r.mu484.Unlock()
	for _, q := range r.opens {
		if !slices.Contains(r.closed, q.Token) {
			t.Errorf("private candidate environment was not closed: %s", q.Token)
		}
	}
	if r.active != 0 || r.maxActive > 1 {
		t.Errorf("candidate containers active=%d maximum=%d; limit is one per admitted check", r.active, r.maxActive)
	}
}

func TestRepositorySandboxRunsDeclaredChecksOffline(t *testing.T) {
	for _, language := range []string{"go", "python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			part := part484(language, "app")
			root, candidate, snapshot := fixture484(t, []repository.Part{part}, nil)
			// Neither mutable candidate declarations nor the legacy image/cache
			// are authority for an explicitly configured repository check.
			write468(t, candidate, repository.Path, `{"candidate":"not authority"}`)
			r := newRunner484(t)
			s := sandbox484(t, root, snapshot, r)
			s.Image, s.ModCache = "must-not-use-legacy-image", "/must/not/mount/host/cache"
			result, err := s.Run(context.Background(), candidate, &Plan{Files: []string{"app/main.go"}})
			if err != nil || !result.Passed() {
				t.Fatalf("declared checks: %v\n%s\n%s", err, result.Summary(), result.Output)
			}
			if len(r.opens) != 1 || len(r.calls) != 4 || len(r.installs) != 1 {
				t.Fatalf("want one prepared part and four commands: opens=%d calls=%d installs=%d", len(r.opens), len(r.calls), len(r.installs))
			}
			q := r.opens[0]
			artifact := r.images[q.Image]
			if artifact.Base != part.Image || q.Directory != part.Directory || q.Platform != "linux/amd64" || q.SourceDir == candidate || q.SourceDir == root {
				t.Fatalf("wrong environment, cwd, platform or candidate isolation: %+v / %+v", q, artifact)
			}
			for i, phase := range phases484 {
				if !reflect.DeepEqual(r.calls[i].argv, part.Commands[phase].Argv) || status484(t, result, part.Name, phase) != "passed" {
					t.Errorf("lost declared %s command/result: %+v", phase, r.calls[i])
				}
			}
			switch language {
			case "go":
				if q.Env["GOMODCACHE"] != "/invariant/gomod" || q.Env["GOPROXY"] != "off" || q.Env["GOTOOLCHAIN"] != "local" || !strings.Contains(q.Env["GOFLAGS"], "-mod=readonly") {
					t.Errorf("Go lost the prepared offline environment: %v", q.Env)
				}
			case "python":
				if !strings.HasPrefix(q.Env["PATH"], "/invariant/venv/bin:") || q.Env["PIP_NO_INDEX"] != "1" {
					t.Errorf("Python lost its offline venv: %v", q.Env)
				}
			case "typescript":
				if q.Env["NPM_CONFIG_OFFLINE"] != "true" || q.Env["NPM_CONFIG_IGNORE_SCRIPTS"] != "true" {
					t.Errorf("npm lost offline policy: %v", q.Env)
				}
			}
			closed484(t, r)
		})
	}
}

func TestRepositorySandboxSelectsAllAndOnlyAffectedParts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		files      []string
		accept     bool
		regression bool
		want       []string
		refuse     bool
	}{
		{name: "Go part", files: []string{"api/main.go"}, want: []string{"api"}},
		{name: "TypeScript part", files: []string{"web/main.ts"}, want: []string{"web"}},
		{name: "deleted file", files: []string{"api/deleted.go"}, want: []string{"api"}},
		{name: "shared path", files: []string{"shared/settings.json"}, want: []string{"api", "web"}},
		{name: "shared input", files: []string{"shared/input.txt"}, want: []string{"api", "web"}},
		{name: "agent rules", files: []string{"AGENTS.md"}, want: []string{"api", "web", "worker"}},
		{name: "current state", files: []string{"STATE.md"}, want: []string{"api", "web", "worker"}},
		{name: "contract", files: []string{repository.Path}, want: []string{"api", "web", "worker"}},
		{name: "acceptance owner", files: []string{"api/main.go"}, accept: true, want: []string{"api", "web"}},
		{name: "regression owner", files: []string{"api/main.go"}, regression: true, want: []string{"api", "web"}},
		{name: "unowned", files: []string{"unowned/file.go"}, refuse: true},
		{name: "unsafe", files: []string{"../escape.go"}, refuse: true},
		{name: "empty selection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := []repository.Part{part484("python", "worker"), part484("typescript", "web"), part484("go", "api")}
			for i := 1; i < len(parts); i++ {
				parts[i].Inputs = append(parts[i].Inputs, "shared/input.txt")
			}
			root, candidate, snapshot := fixture484(t, parts, func(c *repository.Config) {
				c.AgentRules, c.CurrentState = []string{"AGENTS.md"}, []string{"STATE.md"}
				c.SharedPaths = []repository.SharedPath{
					{Path: "shared/settings.json", Parts: []string{"web", "api"}},
					{Path: "api/new_test.go", Parts: []string{"web"}},
					{Path: "api/existing_test.go", Parts: []string{"worker"}},
				}
			})
			plan := &Plan{Files: tc.files}
			if tc.accept {
				plan.Tests = []Test{{Name: "TestNativeLater", File: "web/new_accept_test.go"}}
				write468(t, candidate, "web/new_accept_test.go", "package fixture\n")
			}
			if tc.regression {
				write468(t, candidate, "api/new_test.go", "package fixture\n")
			}
			// Unadmitted files must not widen execution to the worker part.
			write468(t, candidate, "worker/unrelated_test.go", "package fixture\n")
			r := newRunner484(t)
			if tc.accept {
				// Ownership selection includes acceptance independently of
				// framework admission. Exercise command execution separately.
				selected, err := sandbox484(t, root, snapshot, r).repositoryParts(context.Background(), candidate, plan)
				if err != nil || len(selected) != len(tc.want) {
					t.Fatalf("acceptance ownership: %v %v", selected, err)
				}
				for i, part := range selected {
					if part.Name != tc.want[i] {
						t.Fatalf("acceptance owner %s, want %s", part.Name, tc.want[i])
					}
				}
				plan.Files = append(plan.Files, plan.Tests[0].File)
				plan.Tests = nil
			}
			result, err := sandbox484(t, root, snapshot, r).Run(context.Background(), candidate, plan)
			if tc.refuse {
				if err == nil && result.Passed() {
					t.Fatal("invalid ownership passed")
				}
				if len(r.installs) != 0 || len(r.opens) != 0 {
					t.Fatal("invalid selection started execution")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, q := range r.opens {
				got = append(got, q.Directory)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("selected %v; want %v", got, tc.want)
			}
			for _, name := range tc.want {
				for _, phase := range phases484 {
					if status484(t, result, name, phase) != "passed" {
						t.Fatal(result.Summary())
					}
				}
			}
			closed484(t, r)
		})
	}
}

func TestRepositorySandboxFailsEveryCommandPhase(t *testing.T) {
	for _, failedPhase := range phases484 {
		t.Run(failedPhase, func(t *testing.T) {
			root, candidate, snapshot := fixture484(t, []repository.Part{part484("typescript", "web")}, nil)
			r := newRunner484(t)
			r.hook = func(_ context.Context, _ DependencyRequest, argv []string) ([]byte, error) {
				if argv[1] == failedPhase {
					return []byte("specific diagnostic\n"), errors.New("exit status 23")
				}
				return []byte("completed " + argv[1] + "\n"), nil
			}
			result, err := sandbox484(t, root, snapshot, r).Run(context.Background(), candidate, &Plan{Files: []string{"web/main.ts"}})
			if err != nil || result.Passed() {
				t.Fatalf("command failure: err=%v result=%+v", err, result)
			}
			failedAt := slices.Index(phases484, failedPhase)
			for i, phase := range phases484 {
				want := "passed"
				if i == failedAt {
					want = "failed"
				} else if i > failedAt {
					want = "not_run"
				}
				if got := status484(t, result, "web", phase); got != want {
					t.Errorf("%s=%s; want %s", phase, got, want)
				}
			}
			if len(r.calls) != failedAt+1 || !strings.Contains(result.Output, "specific diagnostic") || !strings.Contains(result.Summary(), "web") || !strings.Contains(result.Summary(), failedPhase) {
				t.Fatalf("failure lost identity/diagnostics or ran unfinished work: %s", result.Summary())
			}
			closed484(t, r)
		})
	}
}

func TestRepositorySandboxHonorsInapplicableChecks(t *testing.T) {
	part := part484("python", "worker")
	part.Commands["lint"] = repository.Command{NotApplicable: "No linter is configured for this fixture."}
	root, candidate, snapshot := fixture484(t, []repository.Part{part}, nil)
	r := newRunner484(t)
	result, err := sandbox484(t, root, snapshot, r).Run(context.Background(), candidate, &Plan{Files: []string{"worker/main.go"}})
	if err != nil || !result.Passed() || len(r.calls) != 3 || status484(t, result, "worker", "lint") != "skipped" || !strings.Contains(result.Summary(), part.Commands["lint"].NotApplicable) {
		t.Fatalf("inapplicable phase was lost or executed: %v\n%s", err, result.Summary())
	}
	closed484(t, r)
}

func TestRepositorySandboxCannotAcceptDiagnosticRecords(t *testing.T) {
	part := part484("go", "api")
	part.Commands["test_one"] = repository.Command{Argv: []string{"trusted-fixture", "{test}", "literal argument; $(not-a-shell)"}}
	root, candidate, snapshot := fixture484(t, []repository.Part{part}, nil)
	plan := n503Read(t, n503Plan(t, "go", "api", "api/native_accept_test.go", "TestUnprovedNative", "package fixture\nimport \"testing\"\nfunc TestUnprovedNative(t *testing.T){}\n"), snapshot)
	r := newRunner484(t)
	r.hook = func(_ context.Context, _ DependencyRequest, _ []string) ([]byte, error) {
		return []byte("@@invariant vet=0\n@@invariant test=0\n--- PASS: TestUnprovedNative (0.00s)\n" +
			"@@result {\"kind\":\"test\",\"package\":\"./api\",\"name\":\"TestUnprovedNative\",\"phase\":\"accept\",\"status\":\"passed\",\"file\":\"api/native_accept_test.go\"}\n" +
			"@@result {\"kind\":\"deadline\",\"phase\":\"forged\"}\n@@result {\"kind\":\"done\"}\n"), nil
	}
	s := sandbox484(t, root, snapshot, r)
	s.Repository.Adapters = map[string]RepositoryAcceptanceAdapter{"go": &adapter515{}}
	s.Repository.Dependencies.Runner = &runner515{runner484: r, accept: func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
		return evidence515(in, "test", "skipped", "no individual completion"), nil, nil
	}}
	result, err := s.Run(context.Background(), candidate, plan)
	if err != nil || result.Passed() || result.Accepted[plan.Tests[0].Identity()] || result.DeadlineExceeded || result.Interrupted || result.TimedOut {
		t.Fatalf("diagnostics impersonated evidence: %v %+v", err, result)
	}
	for _, test := range result.TestResults {
		if test.Name == "TestUnprovedNative" && test.Status == "passed" {
			t.Fatal("forged native test evidence")
		}
	}
	for _, phase := range phases484 {
		if status484(t, result, "api", phase) != "passed" {
			t.Fatal("diagnostics changed command status")
		}
	}
	if !strings.Contains(result.Output, "TestUnprovedNative") {
		t.Fatal("diagnostics were discarded")
	}
	closed484(t, r)
}

func TestRepositorySandboxSharesDeadlinesAcrossParts(t *testing.T) {
	parts := []repository.Part{part484("go", "api"), part484("typescript", "web"), part484("python", "worker")}
	root, candidate, snapshot := fixture484(t, parts, nil)
	r := newRunner484(t)
	s := sandbox484(t, root, snapshot, r)
	s.ShardTimeout = 350 * time.Millisecond
	var firstTestDeadline time.Time
	r.hook = func(ctx context.Context, q DependencyRequest, argv []string) ([]byte, error) {
		if argv[1] != "test" {
			return nil, nil
		}
		d, _ := ctx.Deadline()
		if q.Directory == "api" {
			firstTestDeadline = d
			select {
			case <-time.After(50 * time.Millisecond):
				return []byte("api completed\n"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if !d.Equal(firstTestDeadline) {
			t.Errorf("another part renewed the test deadline: %v != %v", d, firstTestDeadline)
		}
		<-ctx.Done()
		return []byte("partial second suite\n"), ctx.Err()
	}
	start := time.Now()
	result, err := s.Run(context.Background(), candidate, &Plan{Files: []string{"api/main.go", "web/main.ts", "worker/main.go"}})
	if err != nil || result.Passed() || !result.TimedOut || result.DeadlineExceeded || result.Interrupted {
		t.Fatalf("shared test budget did not time out correctly: %v %+v", err, result)
	}
	if status484(t, result, "api", "test") != "passed" || status484(t, result, "worker", "test") != "not_run" || !strings.Contains(result.Output, "partial second suite") {
		t.Fatal("timeout lost completed, partial or unstarted work")
	}
	if got := status484(t, result, "web", "test"); got != "failed" && got != "running" {
		t.Fatalf("timed out command = %s", got)
	}
	if len(r.deadlines) < 3 {
		t.Fatal("not all dependency preparations were observed")
	}
	whole := r.deadlines[0]
	for _, d := range r.deadlines {
		if !d.Equal(whole) {
			t.Errorf("preparation reset whole-run deadline: %v != %v", d, whole)
		}
	}
	if whole.After(start.Add(s.Timeout + 50*time.Millisecond)) {
		t.Fatal("whole-run allowance started late")
	}
	for _, call := range r.calls {
		if call.deadline.After(whole) {
			t.Fatal("command extended whole-run deadline")
		}
	}
	closed484(t, r)
}

func TestRepositorySandboxCancellationRetainsPartialWork(t *testing.T) {
	for _, mode := range []string{"cancel", "parent deadline", "sandbox deadline"} {
		t.Run(mode, func(t *testing.T) {
			root, candidate, snapshot := fixture484(t, []repository.Part{part484("go", "api"), part484("typescript", "web")}, nil)
			r := newRunner484(t)
			s := sandbox484(t, root, snapshot, r)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "sandbox deadline" {
				s.Timeout = 2 * time.Second
			}
			if mode == "parent deadline" {
				var done context.CancelFunc
				ctx, done = context.WithTimeout(ctx, 2*time.Second)
				defer done()
			}
			r.hook = func(ctx context.Context, _ DependencyRequest, argv []string) ([]byte, error) {
				if argv[1] != "lint" {
					return []byte("format completed\n"), nil
				}
				if mode == "cancel" {
					cancel()
				}
				<-ctx.Done()
				return []byte("lint stopped halfway\n"), ctx.Err()
			}
			result, err := s.Run(ctx, candidate, &Plan{Files: []string{"api/main.go", "web/main.ts"}})
			if err != nil || result.Passed() {
				t.Fatalf("canceled check = %v %+v", err, result)
			}
			if result.DeadlineExceeded != (mode == "sandbox deadline") || result.Interrupted != (mode != "sandbox deadline") {
				t.Fatalf("wrong cancellation classification: %+v", result)
			}
			if status484(t, result, "api", "format") != "passed" || status484(t, result, "web", "test") != "not_run" || !strings.Contains(result.Output, "lint stopped halfway") {
				t.Fatal("cancellation erased partial work")
			}
			closed484(t, r)
		})
	}
}

func TestRepositorySandboxPreparationAndCleanupFailuresFailCheck(t *testing.T) {
	for _, failure := range []string{"prepare", "inspect", "open", "close", "missing snapshot"} {
		t.Run(failure, func(t *testing.T) {
			root, candidate, snapshot := fixture484(t, []repository.Part{part484("go", "api")}, nil)
			r := newRunner484(t)
			s := sandbox484(t, root, snapshot, r)
			switch failure {
			case "prepare":
				r.installHook = func(context.Context, DependencyRequest) (DependencyArtifact, error) {
					return DependencyArtifact{}, errors.New("install failed")
				}
			case "inspect":
				r.inspectHook = func(a DependencyArtifact) (DependencyArtifact, error) { a.Complete = false; return a, nil }
			case "open":
				r.openHook = func(context.Context, DependencyRequest) error { return errors.New("container startup failed") }
			case "close":
				r.closeHook = func(context.Context, DependencyRequest) error { return errors.New("container cleanup failed") }
			case "missing snapshot":
				s.Repository.Snapshot = nil
			}
			result, err := s.Run(context.Background(), candidate, &Plan{Files: []string{"api/main.go"}})
			if result.Passed() || (err == nil && len(result.Problems) == 0) {
				t.Fatalf("%s failure disappeared: %v %+v", failure, err, result)
			}
			if failure != "close" && len(r.calls) != 0 {
				t.Fatal("ran commands after environment admission failed")
			}
			if failure != "close" && failure != "missing snapshot" {
				for _, phase := range phases484 {
					if status484(t, result, "api", phase) != "not_run" {
						t.Fatal("environment failure lost the selected unfinished work")
					}
				}
			}
			if failure == "close" && status484(t, result, "api", "test") != "passed" {
				t.Fatal("cleanup failure erased completed work")
			}
		})
	}
}

// Helpers shared by the acceptance files, kept separate from production code.
func read484(t *testing.T, root, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
