package plumbing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestConfiguredGoPlanningQueueOwnsCleanupAndDeadline610(t *testing.T) {
	cache := t.TempDir()
	eligible610(t, configured610(t, cache, []repositoryCase582{repositoryControl582()}, false))
	q, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	first := configured610(t, cache, []repositoryCase582{repositoryControl582()}, false)
	second := configured610(t, cache, []repositoryCase582{repositoryControl582()}, false)
	cleaning, release, waiting := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var closeOnce, waitOnce, releaseOnce sync.Once
	first.runner.before = func(ctx context.Context, stage, part string, argv []string) error {
		if stage == "close" {
			if ctx.Err() != nil {
				t.Error("planning cleanup inherited cancellation")
			}
			if d, ok := ctx.Deadline(); !ok || time.Until(d) > SandboxCleanupAllowance+time.Second {
				t.Error("cleanup lacks its bounded reserve")
			}
			closeOnce.Do(func() { close(cleaning) })
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	a, finishA, err := q.RegisterPlan(t.Context(), first.sandbox, first.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finishA)
	b, finishB, err := q.RegisterPlan(WithCheckStatus(t.Context(), func(wait bool) {
		if wait {
			waitOnce.Do(func() { close(waiting) })
		}
	}), second.sandbox, second.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finishB)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	firstWS, secondWS := n503Draft(t, first.plan), n503Draft(t, second.plan)
	type outcome struct {
		c   *Checked
		err error
	}
	firstDone, secondDone := make(chan outcome, 1), make(chan outcome, 1)
	go func() { c, err := a.check(t.Context(), firstWS); firstDone <- outcome{c, err} }()
	repositoryAwait582(t, cleaning, "planning cleanup")
	ctx, cancel := context.WithCancel(t.Context())
	go func() { c, err := b.check(ctx, secondWS); secondDone <- outcome{c, err} }()
	repositoryAwait582(t, waiting, "second planner waiting for cleanup")
	cancel()
	select {
	case got := <-secondDone:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("waiting cancellation: %v", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled planner remained blocked")
	}
	finishB() // Join the canceled request before inspecting its runner.
	if len(second.runner.observed) != 0 {
		t.Fatal("waiting request prepared work before cleanup released the slot")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case got := <-firstDone:
		if got.err != nil || got.c == nil || got.c.Problem != "" {
			t.Fatalf("cleanup lost completed base evidence: %v %+v", got.err, got.c)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("cleanup did not finish")
	}
	// Two real selectors consume one phase allowance; time does not restart
	// at discovery, compilation, another selector, or cleanup.
	source := repositorySource582(`time.Sleep(150*time.Millisecond);t.Fatal("completed first")`, "")
	g := configured610(t, cache, []repositoryCase582{
		{"api", "one", "TestReady", source},
		{"api", "two", "TestReady", repositorySource582(`t.Fatal("completed second")`, "")},
	}, false)
	eligible610(t, g)
	var phaseEnd time.Time
	for _, seen := range g.runner.observed {
		if seen.stage == "read" && phaseEnd.IsZero() {
			phaseEnd = seen.end
		}
		if !phaseEnd.IsZero() && seen.stage != "close" && !seen.end.Equal(phaseEnd) {
			t.Fatalf("later work renewed deadline: %+v", seen)
		}
	}
	if phaseEnd.IsZero() {
		t.Fatal("no discovery observed")
	}
	h := configured610(t, cache, []repositoryCase582{repositoryControl582()}, false)
	h.sandbox.Timeout = 100 * time.Millisecond
	h.runner.before = func(ctx context.Context, stage, part string, argv []string) error {
		if stage == "install" {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	c, err := check610(t, h)
	if c == nil || c.Problem == "" || !c.Run.DeadlineExceeded || c.Run.Passed() {
		t.Fatalf("actual run deadline did not veto: %v %+v", err, c)
	}
}
