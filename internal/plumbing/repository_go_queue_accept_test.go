package plumbing

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func repositoryAwait582(t *testing.T, ch <-chan struct{}, why string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(45 * time.Second):
		t.Fatal("did not reach " + why)
	}
}

func TestRepositoryGoQueueAndCleanup582(t *testing.T) {
	cache := t.TempDir()
	repositoryRequireControl582(t, cache)
	queue, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := queue.Close(); err != nil {
			t.Error(err)
		}
	})
	// IPC requests supply only location and registered identity. A later change
	// to the caller's plan/adapter map or candidate policy cannot replace them.
	f := newRepositoryFixture582(t, cache, []repositoryCase582{repositoryControl582()}, false, nil, nil)
	f.candidateValue(7)
	build, err := queue.register(t.Context(), f.sandbox, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { build.finish() })
	original := cloneRepositoryPlan(f.plan)
	f.plan.Tests[0].Selector = "TestReplacedByCaller"
	f.plan.Sources[f.plan.Tests[0].File] = "package fixture\n"
	f.sandbox.Repository.Adapters["go"] = nil
	// The fixture's dependency boundary needs its original source for opening
	// local process storage, just as the queue keeps its original host authority.
	f.runner.fixture = &repositoryFixture582{plan: original}
	write468(t, f.candidate, ".invariant/repository.json", `{"version":999,"parts":[]}`)
	client := &CheckClient{Address: queue.listener.Addr().String(), Build: build.id}
	child := Sandbox{Checks: client, Repository: &RepositoryChecks{}}
	r, err := child.Run(t.Context(), f.candidate, &Plan{Files: []string{"forged/unowned.go"}})
	if err != nil || !r.Passed() {
		t.Fatalf("queue replaced captured authority or lost real evidence: %v\n%s", err, r.Summary())
	}
	repositoryResult582(t, r, original.Tests[0], "passed", "real completion 582")
	repositoryRoundTrip582(t, r)
	// A queued infrastructure error must still return the completed record in
	// the response, even though CheckClient also reports the error.
	f.runner.damage = func(_ string, out RepositoryProcessOutput) (RepositoryProcessOutput, error) {
		return out, errors.New("queued transport veto 582")
	}
	r, err = client.run(t.Context(), f.candidate)
	if err == nil || !strings.Contains(err.Error(), "queued transport veto 582") || r.Passed() {
		t.Fatalf("queue dropped transport veto: %v\n%s", err, r.Summary())
	}
	repositoryResult582(t, r, original.Tests[0], "passed", "real completion 582")
	repositoryRoundTrip582(t, r)
	build.finish()
	for _, base := range []bool{true, false} {
		g := newRepositoryFixture582(t, cache, []repositoryCase582{repositoryControl582()}, false, nil, nil)
		g.candidateValue(7)
		g.runner.before = func(ctx context.Context, stage, part string, argv []string) error {
			if stage == "close" {
				return errors.New("cleanup veto 582")
			}
			return nil
		}
		r, problem, err := g.run(t.Context(), base)
		if err == nil || r.Passed() || base && problem == "" || !strings.Contains(r.Summary(), "cleanup veto 582") {
			t.Fatal("cleanup error did not veto completed real evidence")
		}
		status := "passed"
		if base {
			status = "failed"
		}
		repositoryResult582(t, r, g.plan.Tests[0], status, "")
	}
	// Cleanup of a part must finish before its staging is removed and before
	// another part starts; a failing close prevents that later part entirely.
	one := repositoryControl582()
	two := one
	two.part = "web"
	g := newRepositoryFixture582(t, cache, []repositoryCase582{one, two}, false, nil, nil)
	g.candidateValue(7)
	g.runner.before = func(ctx context.Context, stage, part string, argv []string) error {
		if stage == "close" && part == "api" {
			return errors.New("first part cleanup failed 582")
		}
		return nil
	}
	r, _, err = g.run(t.Context(), false)
	if err == nil || r.Passed() || len(g.runner.envs) != 1 {
		t.Fatal("cleanup failure allowed the next part")
	}
	repositoryResult582(t, r, g.plan.Tests[0], "passed", "")
	repositoryResult582(t, r, g.plan.Tests[1], "not_run", "")

	first := newRepositoryFixture582(t, cache, []repositoryCase582{repositoryControl582()}, false, nil, nil)
	second := newRepositoryFixture582(t, cache, []repositoryCase582{repositoryControl582()}, false, nil, nil)
	first.candidateValue(7)
	second.candidateValue(7)
	cleaning, release, waiting, settled, opened := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var cleanOnce, waitOnce, settledOnce, openOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	first.runner.before = func(ctx context.Context, stage, part string, argv []string) error {
		if stage == "close" {
			cleanOnce.Do(func() { close(cleaning) })
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	second.runner.before = func(ctx context.Context, stage, part string, argv []string) error {
		if stage == "open" {
			openOnce.Do(func() { close(opened) })
		}
		return nil
	}
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	firstBuild, err := queue.register(firstCtx, first.sandbox, first.plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { firstBuild.finish() })
	secondCtx := WithCheckStatus(t.Context(), func(w bool) {
		if w {
			waitOnce.Do(func() { close(waiting) })
		} else {
			settledOnce.Do(func() { close(settled) })
		}
	})
	secondBuild, err := queue.register(secondCtx, second.sandbox, second.plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { secondBuild.finish() })
	type answer struct {
		result Result
		err    error
	}
	doneFirst, doneSecond := make(chan answer, 1), make(chan answer, 1)
	go func() { r, e := firstBuild.run(firstCtx, first.candidate); doneFirst <- answer{r, e} }()
	repositoryAwait582(t, cleaning, "first real check cleanup")
	cancelFirst()
	// An actual IPC request canceled while waiting must never reach OpenChecks.
	waitCtx, cancelWait := context.WithCancel(t.Context())
	secondClient := &CheckClient{Address: queue.listener.Addr().String(), Build: secondBuild.id}
	go func() { r, e := secondClient.run(waitCtx, second.candidate); doneSecond <- answer{r, e} }()
	repositoryAwait582(t, waiting, "queued client contention")
	cancelWait()
	select {
	case a := <-doneSecond:
		if !errors.Is(a.err, context.Canceled) {
			t.Fatalf("queued cancellation lost cause: %v", a.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued client did not cancel")
	}
	repositoryAwait582(t, settled, "server removing canceled queue waiter")
	select {
	case <-opened:
		t.Fatal("canceled queued call opened an environment")
	default:
	}
	// A different live call still cannot acquire the slot during uncanceled
	// cleanup of the canceled first run.
	go func() { r, e := secondBuild.run(secondCtx, second.candidate); doneSecond <- answer{r, e} }()
	select {
	case <-opened:
		t.Fatal("queue released slot before cleanup")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case a := <-doneFirst:
		if a.result.Passed() || !a.result.Interrupted {
			t.Fatalf("cleanup cancellation lost veto: %v\n%s", a.err, a.result.Summary())
		}
		repositoryResult582(t, a.result, first.plan.Tests[0], "passed", "real completion 582")
		for _, env := range first.runner.envs {
			if _, err := os.Stat(env.request.SourceDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("queue released before staging removal")
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first check did not finish cleanup")
	}
	select {
	case a := <-doneSecond:
		if a.err != nil || !a.result.Passed() {
			t.Fatalf("next queued real check failed: %v\n%s", a.err, a.result.Summary())
		}
		repositoryResult582(t, a.result, second.plan.Tests[0], "passed", "real completion 582")
	case <-time.After(45 * time.Second):
		t.Fatal("next queued check was not admitted")
	}
}
