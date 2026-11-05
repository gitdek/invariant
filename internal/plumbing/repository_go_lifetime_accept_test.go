package plumbing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

type repositoryObservedAdapter582 struct {
	GoRepositoryAcceptanceAdapter
	runner *repositoryRunner582
}

func (a repositoryObservedAdapter582) Select(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
	if err := a.runner.visit(ctx, "select", part.Name, []string{test.Selector}); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	return a.GoRepositoryAcceptanceAdapter.Select(ctx, part, test)
}

func TestRepositoryGoSharedLifetime582(t *testing.T) {
	cache := t.TempDir()
	repositoryRequireControl582(t, cache)
	// Observe actual context deadlines, not elapsed-time guesses or test flags.
	// Both ways of starting the phase must include later part startup and work.
	for _, base := range []bool{true, false} {
		for _, suite := range []bool{true, false} {
			t.Run(fmt.Sprintf("base=%t/suite=%t", base, suite), func(t *testing.T) {
				c := repositoryControl582()
				later := c
				later.part = "web"
				f := newRepositoryFixture582(t, cache, []repositoryCase582{c, later}, suite, nil, nil)
				f.candidateValue(7)
				f.sandbox.Repository.Adapters["go"] = repositoryObservedAdapter582{runner: f.runner}
				// Larger overrides must be clamped to the ordinary twelve/ten limits.
				f.sandbox.Timeout = 24 * time.Minute
				f.sandbox.ShardTimeout = 20 * time.Minute
				f.sandbox.Repository.Dependencies.Timeout = 24 * time.Minute
				started := time.Now()
				r, problem, err := f.run(t.Context(), base)
				if err != nil || base && problem != "" || !base && !r.Passed() {
					t.Fatalf("real shared lifecycle failed: %v %s\n%s", err, problem, r.Summary())
				}
				var whole, phase time.Time
				starts := 0
				for _, o := range f.runner.observed {
					if o.stage == "close" {
						continue
					}
					if whole.IsZero() {
						whole = o.end
					}
					if o.end.After(started.Add(SandboxTimeout + time.Second)) {
						t.Fatalf("%s renewed/exceeded whole run: %v", o.stage, o.end)
					}
					if phase.IsZero() && (suite && o.stage == "suite" || !suite && o.stage == "read") {
						phase = o.end
						if phase.After(o.at.Add(SandboxShardTimeout)) || phase.After(whole) || o.at.Add(SandboxShardTimeout).Sub(phase) > 2*time.Second {
							t.Fatal("first suite/discovery did not start one ten-minute phase")
						}
					}
					if phase.IsZero() {
						if !o.end.Equal(whole) {
							t.Fatalf("setup renewed whole run at %s", o.stage)
						}
					} else if !o.end.Equal(phase) {
						t.Fatalf("%s of %s reset shared phase", o.stage, o.part)
					}
					if o.stage == "start" {
						starts++
						if !strings.Contains(strings.Join(o.argv, " "), "-test.timeout=0") {
							t.Fatal("captured timeout=0 was replaced rather than bounded by host context")
						}
					}
				}
				if phase.IsZero() || starts != 2 {
					t.Fatalf("did not exercise both real selectors: starts=%d", starts)
				}
			})
		}
	}
	for _, base := range []bool{true, false} {
		for _, capper := range []string{"caller", "sandbox", "preparer"} {
			t.Run(fmt.Sprintf("base=%t/cap=%s", base, capper), func(t *testing.T) {
				f := newRepositoryFixture582(t, cache, []repositoryCase582{repositoryControl582()}, false, nil, nil)
				ctx := t.Context()
				limit := time.Second
				switch capper {
				case "caller":
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, limit)
					defer cancel()
				case "sandbox":
					f.sandbox.Timeout = limit
				case "preparer":
					f.sandbox.Repository.Dependencies.Timeout = limit
				}
				blocked := false
				f.runner.before = func(ctx context.Context, stage, part string, argv []string) error {
					if stage == "install" {
						blocked = true
						<-ctx.Done()
						return ctx.Err()
					}
					return nil
				}
				started := time.Now()
				r, problem, _ := f.run(ctx, base)
				if !blocked || time.Since(started) > 3*time.Second || r.Passed() || base && problem == "" {
					t.Fatal("short inherited limit did not stop preparation")
				}
				if capper == "caller" && !r.Interrupted || capper == "sandbox" && !r.DeadlineExceeded || capper == "preparer" && !r.TimedOut {
					t.Fatalf("lost deadline owner: %+v", r)
				}
				if len(f.runner.envs) > 0 {
					t.Fatal("expired preparation opened runtime")
				}
			})
		}
	}
	// A phase deadline must stop real runtime despite the captured -timeout=0.
	for _, suite := range []bool{true, false} {
		t.Run(fmt.Sprintf("runtime phase expiry/suite=%t", suite), func(t *testing.T) {
			c := repositoryControl582()
			c.source = repositorySource582(`t.Log("waiting for phase");time.Sleep(20*time.Second)`, "")
			f := newRepositoryFixture582(t, cache, []repositoryCase582{c}, false, nil, func(config *repository.Config) {
				if suite {
					config.Parts[0].Commands["test"] = repository.Command{Argv: []string{"go", "version"}}
				}
			})
			f.sandbox.ShardTimeout = 3 * time.Second
			began := time.Now()
			r, _, _ := f.run(t.Context(), false)
			if !r.TimedOut || r.Passed() || time.Since(began) > 10*time.Second {
				t.Fatalf("test_one timeout=0 escaped phase: %s", r.Summary())
			}
			for _, record := range r.TestResults {
				if record.Status == "passed" || record.Status == "failed" {
					t.Fatal("phase expiry invented terminal")
				}
			}
		})
	}
	for _, base := range []bool{true, false} {
		for _, stage := range []string{"select", "install", "open", "suite", "read", "discovery", "compile", "stage", "helper", "instrument", "prepare", "start", "wait", "close"} {
			t.Run(fmt.Sprintf("base=%t/cancel=%s", base, stage), func(t *testing.T) {
				c := repositoryControl582()
				if stage == "wait" {
					c.source = repositorySource582(`t.Log("runtime entered 582");time.Sleep(20*time.Second)`, "")
				}
				f := newRepositoryFixture582(t, cache, []repositoryCase582{c}, stage == "suite", nil, nil)
				f.candidateValue(7)
				f.sandbox.Repository.Adapters["go"] = repositoryObservedAdapter582{runner: f.runner}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				seen := false
				f.runner.before = func(call context.Context, at, part string, argv []string) error {
					if at == stage {
						seen = true
						cancel()
						if at != "close" {
							return context.Canceled
						}
					}
					return nil
				}
				if stage == "wait" {
					f.runner.duringWait = func(ctx context.Context, p RepositoryProcess) {
						end := time.Now().Add(5 * time.Second)
						for time.Now().Before(end) && ctx.Err() == nil {
							if strings.Contains(string(p.Snapshot().Events), "runtime entered 582") {
								return
							}
							time.Sleep(5 * time.Millisecond)
						}
						t.Error("real runtime did not start before cancellation")
					}
				}
				r, problem, _ := f.run(ctx, base)
				if !seen || !r.Interrupted || r.Passed() || base && problem == "" {
					t.Fatalf("cancellation missed %s: %s", stage, r.Summary())
				}
				if stage == "close" {
					status := "passed"
					if base {
						status = "failed"
					}
					repositoryResult582(t, r, f.plan.Tests[0], status, "")
				} else if stage == "wait" {
					repositoryResult582(t, r, f.plan.Tests[0], "running", "runtime entered 582")
				} else {
					r := result515(t, r, f.plan.Tests[0])
					if r.Status == "passed" || r.Status == "failed" {
						t.Fatal("canceled setup supplied a terminal")
					}
				}
				repositoryRoundTrip582(t, r)
			})
		}
	}
	// A late cancellation is a veto, and cannot discard an authentic terminal.
	for _, base := range []bool{true, false} {
		f := newRepositoryFixture582(t, cache, []repositoryCase582{repositoryControl582()}, false, nil, nil)
		f.candidateValue(7)
		ctx, cancel := context.WithCancel(t.Context())
		f.runner.damage = func(_ string, out RepositoryProcessOutput) (RepositoryProcessOutput, error) {
			cancel()
			return out, context.Canceled
		}
		r, problem, _ := f.run(ctx, base)
		cancel()
		if !r.Interrupted || r.Passed() || base && problem == "" {
			t.Fatal("late cancellation did not veto")
		}
		status := "passed"
		if base {
			status = "failed"
		}
		repositoryResult582(t, r, f.plan.Tests[0], status, "")
	}
}
