package plumbing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func docker591Await(t *testing.T, ctx context.Context, ready func() bool, why string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("did not reach %s: %v", why, ctx.Err())
		case <-ticker.C:
		}
	}
}
func docker591Exists(name string) bool { _, err := os.Stat(name); return err == nil }
func docker591Touch(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(name, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
}
func docker591WaitChannel(t *testing.T, ch <-chan struct{}, why string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(45 * time.Second):
		t.Fatal("did not reach " + why)
	}
}

func TestDockerGoLifetimeAndQueue591(t *testing.T) {
	b := newDocker591(t)
	docker591RequireRepository(t, b)
	one, two := docker591Control(), docker591Control()
	two.part = "web"
	f := newDocker591Repository(t, b, []docker591Case{one, two}, false)
	f.value(7)
	f.sandbox.Timeout = SandboxTimeout
	f.sandbox.ShardTimeout = SandboxShardTimeout
	start := time.Now()
	r, _, err := f.run(t.Context(), false)
	if err != nil || !r.Passed() {
		t.Fatalf("serial Docker parts: %v\n%s", err, r.Summary())
	}
	requests, deadlines := f.runner.requests()
	if len(requests) != 2 || len(deadlines) != 2 {
		t.Fatalf("wrong part count: %v", requests)
	}
	if deadlines[0].IsZero() || deadlines[0].After(start.Add(SandboxTimeout+time.Second)) || deadlines[1].After(start.Add(SandboxShardTimeout+5*time.Second)) || !deadlines[1].Before(deadlines[0]) {
		t.Fatalf("renewed run or phase: %v", deadlines)
	}
	// Every preceding part must finish removal before the next create begins.
	removed := false
	for _, c := range b.calls() {
		if len(c.Args) == 0 {
			continue
		}
		if c.Args[0] == "rm" && c.Done && strings.Contains(strings.Join(c.Args, " "), requests[0].Token) {
			removed = true
		}
		if c.Args[0] == "create" && !c.Done && strings.Contains(strings.Join(c.Args, " "), requests[1].Token) && !removed {
			t.Fatal("next part opened before container cleanup completed")
		}
	}
	b.clean()
	docker591CheckStaging(t, f.runner)
	// These are deliberately shorter contexts at the original entry, not a new
	// timeout allocated by the Docker transport when an operation starts.
	for _, mode := range []string{"caller", "sandbox", "preparer"} {
		t.Run(mode, func(t *testing.T) {
			g := newDocker591Repository(t, b, []docker591Case{docker591Control()}, false)
			g.value(7)
			ctx := t.Context()
			var done context.CancelFunc
			begin := time.Now()
			limit := 2 * time.Second
			switch mode {
			case "caller":
				ctx, done = context.WithTimeout(ctx, limit)
				defer done()
			case "sandbox":
				g.sandbox.Timeout = limit
			case "preparer":
				g.sandbox.Repository.Dependencies.Timeout = limit
			}
			seen := false
			g.runner.before = func(call context.Context, q DependencyRequest) error {
				seen = true
				deadline, ok := call.Deadline()
				if !ok || deadline.After(begin.Add(limit+50*time.Millisecond)) {
					t.Error("Docker opening renewed inherited deadline")
				}
				<-call.Done()
				return call.Err()
			}
			got, _, _ := g.run(ctx, false)
			if !seen || got.Passed() || time.Since(begin) > 3*time.Second {
				t.Fatalf("lost bound at Docker opening: %s", got.Summary())
			}
			if mode == "caller" && !got.Interrupted || mode == "sandbox" && !got.DeadlineExceeded || mode == "preparer" && !got.TimedOut {
				t.Fatalf("wrong timeout attribution for %s: %s", mode, got.Summary())
			}
			b.clean()
			docker591CheckStaging(t, g.runner)
		})
	}
	// Cancel after a real testing start, with an earlier selector already passed.
	busy := docker591Case{"api", "busy", "TestReady", docker591Source(`t.Log("running before cancellation 591");if err:=os.WriteFile("entered591",[]byte("ready"),0600);err!=nil{t.Fatal(err)};for{time.Sleep(time.Hour)}`)}
	g := newDocker591Repository(t, b, []docker591Case{one, busy}, false)
	g.value(7)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type answer struct {
		r   Result
		err error
	}
	finished := make(chan answer, 1)
	go func() { r, _, err := g.run(ctx, false); finished <- answer{r, err} }()
	ready, stopReady := context.WithTimeout(t.Context(), 45*time.Second)
	docker591Await(t, ready, func() bool {
		matches, _ := filepath.Glob(filepath.Join(b.root, "containers", "*", "invariant", "work", "api", "busy", "entered591"))
		return len(matches) > 0
	}, "real runtime start")
	stopReady()
	cancel()
	select {
	case a := <-finished:
		if !a.r.Interrupted || a.r.Passed() {
			t.Fatalf("runtime cancellation did not veto: %v\n%s", a.err, a.r.Summary())
		}
		docker591Result(t, a.r, g.plan.Tests[0], "passed", "completed seven 591")
		docker591Result(t, a.r, g.plan.Tests[1], "running", "running before cancellation 591")
		docker591RoundTrip(t, a.r)
	case <-time.After(SandboxCleanupAllowance + 5*time.Second):
		t.Fatal("cancellation did not join Docker processes and cleanup")
	}
	b.clean()
	docker591CheckStaging(t, g.runner)
	// Cleanup failure prevents another part from opening, but keeps the terminal.
	h := newDocker591Repository(t, b, []docker591Case{one, two}, false)
	h.value(7)
	b.fault("cleanup")
	r, _, err = h.run(t.Context(), false)
	b.fault("")
	opened, _ := h.runner.requests()
	if err == nil || r.Passed() || len(opened) != 1 {
		t.Fatal("failed cleanup admitted the next part")
	}
	docker591Result(t, r, h.plan.Tests[0], "passed", "completed seven 591")
	docker591Result(t, r, h.plan.Tests[1], "not_run", "")
	b.clean()
	docker591CheckStaging(t, h.runner)
	// An actual check-queue slot is held while Docker rm is blocked. A caller
	// cancel during that cleanup cannot cancel cleanup or release the slot early.
	first := newDocker591Repository(t, b, []docker591Case{one}, false)
	first.value(7)
	second := newDocker591Repository(t, b, []docker591Case{one}, false)
	second.value(7)
	queue, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	firstBuild, err := queue.register(firstCtx, first.sandbox, first.plan)
	if err != nil {
		t.Fatal(err)
	}
	defer firstBuild.finish()
	waiting, openedSecond := make(chan struct{}), make(chan struct{})
	var once, openOnce sync.Once
	secondCtx := WithCheckStatus(t.Context(), func(wait bool) {
		if wait {
			once.Do(func() { close(waiting) })
		}
	})
	second.runner.before = func(context.Context, DependencyRequest) error {
		openOnce.Do(func() { close(openedSecond) })
		return nil
	}
	secondBuild, err := queue.register(secondCtx, second.sandbox, second.plan)
	if err != nil {
		t.Fatal(err)
	}
	defer secondBuild.finish()
	hold := filepath.Join(b.root, "hold-cleanup")
	release := filepath.Join(b.root, "release-cleanup")
	docker591Touch(t, hold)
	defer func() { _ = os.WriteFile(release, []byte("release"), 0600) }()
	doneFirst, doneSecond := make(chan answer, 1), make(chan answer, 1)
	go func() { r, e := firstBuild.run(firstCtx, first.candidate); doneFirst <- answer{r, e} }()
	ready, stopReady = context.WithTimeout(t.Context(), 45*time.Second)
	docker591Await(t, ready, func() bool { return docker591Exists(filepath.Join(b.root, "cleanup-entered")) }, "Docker cleanup")
	stopReady()
	cancelFirst()
	go func() { r, e := secondBuild.run(secondCtx, second.candidate); doneSecond <- answer{r, e} }()
	docker591WaitChannel(t, waiting, "queue contention during cleanup")
	select {
	case <-openedSecond:
		t.Fatal("queue released before Docker cleanup")
	case <-time.After(50 * time.Millisecond):
	}
	docker591Touch(t, release)
	select {
	case a := <-doneFirst:
		if a.r.Passed() || !a.r.Interrupted {
			t.Fatalf("late cancellation disappeared: %v\n%s", a.err, a.r.Summary())
		}
		docker591Result(t, a.r, first.plan.Tests[0], "passed", "completed seven 591")
		docker591CheckStaging(t, first.runner)
	case <-time.After(SandboxCleanupAllowance + 5*time.Second):
		t.Fatal("canceled queue owner did not finish cleanup")
	}
	select {
	case a := <-doneSecond:
		if a.err != nil || !a.r.Passed() {
			t.Fatalf("next check failed: %v\n%s", a.err, a.r.Summary())
		}
	case <-time.After(45 * time.Second):
		t.Fatal("next queued check did not finish")
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	b.clean()
	docker591CheckStaging(t, second.runner)
	for _, q := range append(requests, opened...) {
		if _, err := os.Stat(q.SourceDir); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("released check retained staging")
		}
	}
}
