package plumbing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifyBudgetUsesOnlyCopiedGoFiles(t *testing.T) {
	record := v498Spy(t)
	for _, kind := range []string{"ignored", "deleted", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			s397Write(t, filepath.Join(root, "internal/code.go"), "package fixture\n", 0o644)
			s397Write(t, filepath.Join(root, "internal/verify/assets/driver.py"), "# fixture\n", 0o644)
			code := filepath.Join(root, "internal/verify/code.go")
			if kind == "symlink" {
				if err := os.Symlink("../code.go", code); err != nil {
					t.Fatal(err)
				}
			} else {
				if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
					t.Fatalf("git init: %v\n%s", err, out)
				}
				s397Write(t, code, "package fixture\n", 0o644)
				if kind == "ignored" {
					s397Write(t, filepath.Join(root, ".gitignore"), "internal/verify/code.go\n", 0o644)
				} else {
					if out, err := exec.Command("git", "-C", root, "add", "internal/verify/code.go").CombinedOutput(); err != nil {
						t.Fatalf("git add: %v\n%s", err, out)
					}
					if err := os.Remove(code); err != nil {
						t.Fatal(err)
					}
				}
			}
			started := time.Now()
			r, err := (Sandbox{Image: "fixture", ModCache: t.TempDir()}).Run(v498Watchdog(t), root,
				&Plan{Files: []string{"internal/verify/assets/driver.py"}})
			if err != nil || !r.Passed() {
				t.Fatalf("check with %s Go file: %+v, %v", kind, r, err)
			}
			all := v498Read(t, record)
			o := all[len(all)-1]
			v498Budgets(t, o, started, 12*time.Minute, 10*time.Minute)
			if len(o.Packages) != 1 || o.Packages[0] != "./internal" {
				t.Fatalf("asset ownership counted a %s Go file: %v", kind, o.Packages)
			}
		})
	}
}

func TestVerifyBudgetBoundsPreparation(t *testing.T) {
	record := v498Spy(t)
	bin := t.TempDir()
	// Hold the initial inventory operation, before any files can be copied.
	s397Write(t, filepath.Join(bin, "git"), "#!/usr/bin/env python3\nimport time\ntime.sleep(60)\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	s397Write(t, filepath.Join(root, "internal/verify/code.go"), "package fixture\n", 0o644)
	plan := &Plan{Files: []string{"internal/verify/code.go"},
		Tests: []Test{{Name: "TestPending", File: "internal/verify/pending_test.go"}}}
	for _, parent := range []bool{false, true} {
		t.Run(map[bool]string{false: "override", true: "parent"}[parent], func(t *testing.T) {
			ctx := v498Watchdog(t)
			s := Sandbox{Image: "fixture", ModCache: t.TempDir(), Timeout: 100 * time.Millisecond}
			if parent {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, s.Timeout)
				defer cancel()
				s.Timeout = time.Second
			}
			started := time.Now()
			r, err := s.Run(ctx, root, plan)
			if err != nil || r.Passed() || r.DeadlineExceeded == parent || r.Interrupted != parent {
				t.Fatalf("preparation must return failed partial evidence: %+v, %v", r, err)
			}
			if len(r.Failing()) != 1 || r.Failing()[0] != "internal/verify/pending_test.go:TestPending" {
				t.Fatalf("lost unstarted acceptance test: %s", r.Summary())
			}
			if elapsed := time.Since(started); elapsed > 3*time.Second {
				t.Fatalf("preparation outlived its budget: %s", elapsed)
			}
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatalf("Docker was reached after preparation expired: %v", err)
			}
		})
	}
}

func TestVerifyBudgetStartsOnQueueAdmission(t *testing.T) {
	record := v498Spy(t)
	root := t.TempDir()
	s397Write(t, filepath.Join(root, "internal/verify/code.go"), "package fixture\n", 0o644)
	s397Write(t, filepath.Join(root, "internal/verify/assets/driver.py"), "# fixture\n", 0o644)
	q, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	waiting := make(chan struct{}, 1)
	ctx := WithCheckStatus(v498Watchdog(t), func(wait bool) {
		if wait {
			waiting <- struct{}{}
		}
	})
	plan := &Plan{Files: []string{"internal/verify/assets/driver.py"}}
	b, err := q.register(ctx, Sandbox{Image: "fixture", ModCache: t.TempDir(), Timeout: time.Second}, plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.finish() })
	q.slots <- struct{}{}
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		s := Sandbox{Checks: &CheckClient{Address: q.listener.Addr().String(), Build: b.id}}
		r, err := s.Run(ctx, root, plan)
		done <- outcome{r, err}
	}()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal("check never queued")
	}
	// Queue waiting exceeds the selected override but leaves it intact.
	time.Sleep(1200 * time.Millisecond)
	admitted := time.Now()
	<-q.slots
	select {
	case out := <-done:
		if out.err != nil || !out.result.Passed() {
			t.Fatalf("admitted check: %+v, %v", out.result, out.err)
		}
	case <-ctx.Done():
		t.Fatal("admitted check did not finish")
	}
	observed := v498Read(t, record)
	if len(observed) != 1 {
		t.Fatalf("got %d checks, want one", len(observed))
	}
	v498Budgets(t, observed[0], admitted, time.Second, 55*time.Minute)
	deadline := time.Unix(0, int64(observed[0].Deadline*float64(time.Second)))
	if deadline.Before(admitted.Add(900 * time.Millisecond)) {
		t.Fatalf("queue wait consumed the sandbox budget: %s after admission", deadline.Sub(admitted))
	}
}
