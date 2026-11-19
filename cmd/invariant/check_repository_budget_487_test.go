package main

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
)

// budgetRunner487 is #736's gate fixture runner, recording the deadline
// each declared command runs under.
type budgetRunner487 struct {
	*gateRunner736
	mu        sync.Mutex
	deadlines map[string]time.Time
}

func (r *budgetRunner487) OpenChecks(ctx context.Context, q plumbing.DependencyRequest) (plumbing.RepositoryCheckEnvironment, error) {
	env, err := r.gateRunner736.OpenChecks(ctx, q)
	if err != nil {
		return env, err
	}
	return &budgetEnvironment487{RepositoryCheckEnvironment: env, r: r}, nil
}

type budgetEnvironment487 struct {
	plumbing.RepositoryCheckEnvironment
	r *budgetRunner487
}

func (e *budgetEnvironment487) Run(ctx context.Context, argv []string) ([]byte, error) {
	if d, ok := ctx.Deadline(); ok && len(argv) > 1 {
		e.r.mu.Lock()
		e.r.deadlines[argv[1]] = d
		e.r.mu.Unlock()
	}
	return e.RepositoryCheckEnvironment.Run(ctx, argv)
}

// #487: check-repository bounds its check from its own start. A part the
// base's contract gives a longer test budget lengthens that bound, so the
// gate's check gets the budget the factory's does; one that declares none
// keeps twelve minutes.
func TestCheckRepositoryGivesADeclaredBudgetItsTime487(t *testing.T) {
	for _, tc := range []struct {
		minutes    int
		run, tests time.Duration
	}{
		{0, 12 * time.Minute, 10 * time.Minute},
		{30, 32 * time.Minute, 30 * time.Minute},
	} {
		f := gateFixtureFor736(t, "go")
		if tc.minutes > 0 {
			f.config.Parts[0].TestMinutes = tc.minutes
			gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
			f.base = gateCommit736(t, f.root)
		}
		r := &budgetRunner487{gateRunner736: f.runner(), deadlines: map[string]time.Time{}}
		// No injected limit: the production budgets apply.
		sb := plumbing.Sandbox{Repository: &plumbing.RepositoryChecks{Dependencies: plumbing.DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: r}}}
		var out bytes.Buffer
		started := time.Now()
		if code := runRepositoryCheck(t.Context(), []string{"-C", f.root, "-repo", gateRepo736}, sb, f.comments, &out); code != 0 {
			t.Fatalf("%d minutes: check-repository = %d\n%s", tc.minutes, code, out.String())
		}
		lint, test := r.deadlines["lint"], r.deadlines["test"]
		if lint.IsZero() || test.IsZero() {
			t.Fatalf("%d minutes: commands ran with deadlines %v\n%s", tc.minutes, r.deadlines, out.String())
		}
		// Lint runs before the test phase, under the whole run.
		if got := lint.Sub(started); got < tc.run-30*time.Second || got > tc.run+time.Second {
			t.Errorf("%d minutes: lint had %s of the whole run, want %s", tc.minutes, got, tc.run)
		}
		if got := test.Sub(started); got < tc.tests || got > tc.tests+30*time.Second {
			t.Errorf("%d minutes: the tests had %s, want the %s budget", tc.minutes, got, tc.tests)
		}
		if want := fmt.Sprintf("The checks ran under a test budget of %d minutes", tc.tests/time.Minute); !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Errorf("%d minutes: the summary doesn't say %q:\n%s", tc.minutes, want, out.String())
		}
	}
}
