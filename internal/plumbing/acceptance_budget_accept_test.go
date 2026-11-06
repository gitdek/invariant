package plumbing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

func recapture515(t *testing.T, f *fixture515, change func(*repository.Config)) {
	t.Helper()
	c := f.snapshot.Config()
	change(&c)
	write468(t, f.root, repository.Path, config515(t, c))
	git468(t, f.root, "add", "--all")
	git468(t, f.root, "commit", "-q", "--no-verify", "-m", "capture test policy")
	snapshot, err := repository.Load(context.Background(), f.root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	f.snapshot = snapshot
	f.plan = n503Read(t, f.plan, snapshot)
	f.sandbox.Repository.Snapshot = snapshot
}

func TestConfiguredAcceptanceSharesWholeRunAndPhaseDeadlines(t *testing.T) {
	for _, mode := range []string{"whole run", "preparer", "suite starts phase", "acceptance starts phase"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture515(t, "api", "web")
			f.sandbox.Timeout = 8 * time.Second
			f.sandbox.ShardTimeout = 4 * time.Second
			if mode == "whole run" {
				f.sandbox.Timeout = time.Second
			}
			if mode == "preparer" {
				f.sandbox.Repository.Dependencies.Timeout = time.Second
			}
			if strings.Contains(mode, "starts phase") {
				f.sandbox.ShardTimeout = 700 * time.Millisecond
			}
			if mode == "acceptance starts phase" {
				recapture515(t, f, func(c *repository.Config) {
					for i := range c.Parts {
						c.Parts[i].Commands["test"] = repository.Command{NotApplicable: "Only independent selectors in this fixture."}
					}
				})
			}
			start := time.Now()
			wholeLimit := f.sandbox.Timeout
			if mode == "preparer" {
				wholeLimit = f.sandbox.Repository.Dependencies.Timeout
			}
			var selectedEnd, phaseEnd time.Time
			selections := 0
			f.adapter.selectHook = func(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
				d := deadline515(t, ctx)
				if d.After(start.Add(wholeLimit + 50*time.Millisecond)) {
					t.Error("selection did not inherit the original whole-run/preparer cap")
				}
				if selections > 0 && !d.Equal(selectedEnd) {
					t.Error("another selector renewed the whole-run clock")
				}
				selectedEnd = d
				selections++
				if selections == 1 {
					// Preparation must inherit time already consumed by selection.
					select {
					case <-time.After(80 * time.Millisecond):
					case <-ctx.Done():
						return RepositoryAcceptanceSelection{}, ctx.Err()
					}
				}
				return (&adapter515{}).Select(ctx, part, test)
			}
			observePhase := func(ctx context.Context) {
				d := deadline515(t, ctx)
				if phaseEnd.IsZero() {
					phaseEnd = d
					if d.After(time.Now().Add(f.sandbox.ShardTimeout)) || d.After(selectedEnd) {
						t.Error("test phase extended its allowance or the whole run")
					}
				} else if !d.Equal(phaseEnd) {
					t.Error("later test work renewed the shared phase deadline")
				}
			}
			f.runner.hook = func(ctx context.Context, _ DependencyRequest, argv []string) ([]byte, error) {
				if argv[1] == "test" {
					observePhase(ctx)
				}
				return nil, nil
			}
			f.runner.accept = func(ctx context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
				observePhase(ctx)
				return evidence515(in, "test", "failed", "completed before later timeout"), nil, nil
			}
			f.runner.openHook = func(ctx context.Context, q DependencyRequest) error {
				d := deadline515(t, ctx)
				if q.Directory == "api" {
					if d.After(selectedEnd) {
						t.Error("startup renewed the whole-run allowance")
					}
					return nil
				}
				if phaseEnd.IsZero() || !d.Equal(phaseEnd) {
					t.Error("later-part startup renewed the test allowance")
				}
				<-ctx.Done()
				return ctx.Err()
			}
			checked := base515(t, f, context.Background())
			if checked.Problem == "" || (!checked.Run.TimedOut && !checked.Run.DeadlineExceeded) || checked.Run.Interrupted {
				t.Fatalf("deadline did not veto base qualification: %+v", checked)
			}
			if len(f.runner.invocations) != 2 || len(f.runner.closed) != 4 {
				t.Fatal("later timeout lost earlier execution/cleanup or ran later selectors")
			}
			for _, test := range f.plan.Tests {
				r := result515(t, checked.Run, test)
				if test.Part == "api" && r.Status != "failed" || test.Part == "web" && r.Status != "not_run" {
					t.Fatalf("timeout lost completed or unstarted work: %+v", r)
				}
				if r.Reason == "" {
					t.Fatal("timeout left an identity without an explanatory reason")
				}
			}
			for _, d := range f.runner.deadlines {
				if d.After(selectedEnd) {
					t.Error("preparation renewed the clock after selection")
				}
			}
		})
	}
	// Observe the production caps without waiting ten or twelve minutes.
	f := newFixture515(t)
	f.sandbox.Timeout, f.sandbox.ShardTimeout = time.Hour, time.Hour
	f.sandbox.Repository.Dependencies.Timeout = time.Hour
	start := time.Now()
	f.adapter.selectHook = func(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
		if deadline515(t, ctx).After(start.Add(12*time.Minute + time.Second)) {
			t.Error("override extended the twelve-minute whole-run cap")
		}
		return (&adapter515{}).Select(ctx, part, test)
	}
	var suiteEnd time.Time
	f.runner.hook = func(ctx context.Context, _ DependencyRequest, argv []string) ([]byte, error) {
		if argv[1] == "test" {
			suiteEnd = deadline515(t, ctx)
			if suiteEnd.After(time.Now().Add(10 * time.Minute)) {
				t.Error("override extended the ten-minute test cap")
			}
		}
		return nil, nil
	}
	f.runner.accept = func(ctx context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
		if suiteEnd.IsZero() || !deadline515(t, ctx).Equal(suiteEnd) {
			t.Error("acceptance got a new allowance after suite tests")
		}
		return evidence515(in, "test", "failed", "ordinary failure"), nil, nil
	}
	if checked := base515(t, f, context.Background()); checked.Problem != "" {
		t.Fatal(checked.Problem)
	}
}

func TestConfiguredAcceptanceInfrastructureVetoesBaseQualification(t *testing.T) {
	for _, mode := range []string{"preparation", "inspection", "startup", "partial startup", "format", "lint", "launch", "setup", "cleanup", "cancel after failure", "cancel in cleanup", "parent deadline"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture515(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "parent deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, time.Second)
				defer stop()
			}
			allFail515(f.runner)
			cleaned := 0
			f.runner.closeHook = func(cleanup context.Context, _ DependencyRequest) error {
				cleaned++
				if cleanup.Err() != nil || time.Until(deadline515(t, cleanup)) > 15*time.Second+time.Second {
					t.Error("cleanup did not receive a fresh bounded context")
				}
				if mode == "cancel in cleanup" && len(f.runner.invocations) > 0 {
					cancel()
					if cleanup.Err() != nil {
						t.Error("cleanup was canceled with its caller")
					}
				}
				if mode == "cleanup" && len(f.runner.invocations) > 0 {
					return errors.New("cleanup failed after completed tests")
				}
				return nil
			}
			switch mode {
			case "preparation":
				f.runner.installHook = func(context.Context, DependencyRequest) (DependencyArtifact, error) {
					return DependencyArtifact{}, errors.New("dependency install failed")
				}
			case "inspection":
				f.runner.inspectHook = func(a DependencyArtifact) (DependencyArtifact, error) { a.Complete = false; return a, nil }
			case "startup":
				f.runner.openHook = func(context.Context, DependencyRequest) error { return errors.New("startup failed") }
			case "partial startup":
				f.runner.partialOpen = true
			case "format", "lint":
				f.runner.hook = func(_ context.Context, _ DependencyRequest, argv []string) ([]byte, error) {
					if argv[1] == mode {
						return []byte("--- FAIL: TestFirst\n"), errors.New("setup command failed")
					}
					return nil, nil
				}
			case "launch", "setup", "cancel after failure", "parent deadline":
				f.runner.accept = func(ctx context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
					e := evidence515(in, "test", "failed", "completed assertion before interruption")
					if mode == "cancel after failure" {
						cancel()
						return e, nil, nil
					}
					if in.Binding.Selector == "TestFirst" {
						return e, nil, nil
					}
					if mode == "setup" {
						return evidence515(in, "setup", "failed", "discovery/tool setup failed"), nil, nil
					}
					if mode == "parent deadline" {
						<-ctx.Done()
						return RepositoryAcceptanceEvidence{}, []byte("interrupted invocation"), ctx.Err()
					}
					return RepositoryAcceptanceEvidence{}, []byte("cannot launch tool"), errors.New("tool-launch failed")
				}
			}
			checked := base515(t, f, ctx)
			if checked.Problem == "" || checked.Run.Passed() {
				t.Fatalf("infrastructure %s qualified the base: %+v", mode, checked)
			}
			if strings.HasPrefix(mode, "cancel") || mode == "parent deadline" {
				if !checked.Run.Interrupted {
					t.Fatalf("cancellation disappeared after earlier failure: %+v", checked.Run)
				}
			}
			if mode == "launch" || mode == "setup" || mode == "cleanup" || strings.HasPrefix(mode, "cancel") || mode == "parent deadline" {
				if result515(t, checked.Run, f.plan.Tests[0]).Status != "failed" {
					t.Fatal("later infrastructure failure erased earlier completed evidence")
				}
			}
			for _, test := range f.plan.Tests {
				r := result515(t, checked.Run, test)
				if r.Reason == "" {
					t.Fatalf("infrastructure failure omitted a reason for %s", test.Identity())
				}
			}
			if mode == "partial startup" && (cleaned != 1 || len(f.runner.invocations) != 0) {
				t.Fatal("partial startup environment was not cleaned before returning")
			}
			if mode != "startup" && mode != "cleanup" {
				closed484(t, f.runner.runner484)
			}
		})
	}
}

func await515(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestConfiguredAcceptanceQueueRetainsAuthorityAndCleanupOwnership(t *testing.T) {
	queue, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	first, second := newFixture515(t, "api"), newFixture515(t, "api", "web")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleaning, release, waiting, secondOpened := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1), make(chan struct{}, 2)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	first.runner.closeHook = func(cleanup context.Context, _ DependencyRequest) error {
		if cleanup.Err() != nil || time.Until(deadline515(t, cleanup)) > 15*time.Second+time.Second {
			t.Error("queued cleanup lacked an independent bounded context")
		}
		close(cleaning)
		select {
		case <-release:
			if cleanup.Err() != nil {
				t.Error("queue cancellation reached cleanup")
			}
			return nil
		case <-cleanup.Done():
			return cleanup.Err()
		}
	}
	second.runner.openHook = func(context.Context, DependencyRequest) error {
		select {
		case secondOpened <- struct{}{}:
		default:
		}
		return nil
	}
	firstBuild, err := queue.register(ctx, first.sandbox, first.plan)
	if err != nil {
		t.Fatal(err)
	}
	defer firstBuild.finish()
	secondContext := WithCheckStatus(context.Background(), func(w bool) {
		if w {
			waiting <- struct{}{}
		}
	})
	secondBuild, err := queue.register(secondContext, second.sandbox, second.plan)
	if err != nil {
		t.Fatal(err)
	}
	defer secondBuild.finish()
	// Registration captures the map, not an alias that later host mutations or
	// a child IPC request can use to replace the chosen adapter.
	second.sandbox.Repository.Adapters["go"] = &adapter515{selectHook: func(context.Context, repository.Part, Test) (RepositoryAcceptanceSelection, error) {
		return RepositoryAcceptanceSelection{}, errors.New("replacement adapter must not run")
	}}
	type answer struct {
		result Result
		err    error
	}
	one, two := make(chan answer, 1), make(chan answer, 1)
	go func() { r, e := firstBuild.run(ctx, first.candidate); one <- answer{r, e} }()
	await515(t, cleaning, "first check cleanup")
	cancel()
	go func() { r, e := secondBuild.run(secondContext, second.candidate); two <- answer{r, e} }()
	await515(t, waiting, "second check queue wait")
	select {
	case <-secondOpened:
		t.Fatal("another check opened an environment before cleanup finished")
	default:
	}
	close(release)
	select {
	case a := <-one:
		if a.result.Passed() || !a.result.Interrupted || len(first.runner.closed) != 1 {
			t.Fatalf("cancellation during cleanup did not veto the run: %s", describe515(a.result, a.err))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first check never completed cleanup")
	}
	select {
	case a := <-two:
		if a.err != nil || !a.result.Passed() || len(second.runner.invocations) != 4 {
			t.Fatalf("queued check lost captured authority: %s", describe515(a.result, a.err))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queue did not admit next check after cleanup")
	}
	closed484(t, first.runner.runner484)
	closed484(t, second.runner.runner484)
	// The actual IPC path carries no replacement adapter, plan or repository
	// policy; the child supplies only a root and its registered build token.
	second.runner.openHook = nil
	client := &CheckClient{Address: queue.listener.Addr().String(), Build: secondBuild.id}
	child := Sandbox{Checks: client, Repository: &RepositoryChecks{}}
	result, err := child.Run(context.Background(), second.candidate, &Plan{Files: []string{"unowned/forged.go"}})
	if err != nil || !result.Passed() {
		t.Fatalf("IPC did not use captured host policy: %s", describe515(result, err))
	}
	for _, test := range second.plan.Tests {
		if result515(t, result, test).Status != "passed" {
			t.Fatal("IPC replaced the registered acceptance identities")
		}
	}
	assertBindings515(t, second)
}
