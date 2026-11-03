package plumbing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifyBudgetUsesCopiedGoFiles(t *testing.T) {
	record := v498Spy(t)
	for _, kind := range []string{"deleted", "ignored", "linked", "ignored_child"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			s397Write(t, filepath.Join(root, "go.mod"), "module fixture\n\ngo 1.27.1\n", 0o644)
			s397Write(t, filepath.Join(root, "internal/plumbing/code.go"), "package plumbing\n", 0o644)
			s397Write(t, filepath.Join(root, "internal/verify/assets/driver.py"), "# asset\n", 0o644)
			code := filepath.Join(root, "internal/verify/code.go")
			s397Write(t, code, "package verify\n", 0o644)
			if kind == "ignored" {
				s397Write(t, filepath.Join(root, ".gitignore"), "internal/verify/code.go\n", 0o644)
			}
			if kind == "ignored_child" {
				s397Write(t, filepath.Join(root, "internal/verify/assets/code.go"), "package assets\n", 0o644)
				s397Write(t, filepath.Join(root, ".gitignore"), "internal/verify/assets/code.go\n", 0o644)
			}
			ctx := v498Watchdog(t)
			for _, args := range [][]string{{"init", "--quiet"}, {"add", "-A"}} {
				cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			if kind == "deleted" || kind == "linked" {
				if err := os.Remove(code); err != nil {
					t.Fatal(err)
				}
				if kind == "linked" {
					if err := os.Symlink("../plumbing/code.go", code); err != nil {
						t.Fatal(err)
					}
				}
			}
			started := time.Now()
			r, err := (Sandbox{Image: "fixture", ModCache: t.TempDir()}).Run(ctx, root,
				&Plan{Files: []string{"internal/verify/assets/driver.py"}})
			if err != nil || !r.Passed() {
				t.Fatalf("inventory check: %+v, %v", r, err)
			}
			all := v498Read(t, record)
			o := all[len(all)-1]
			run, tests := 12*time.Minute, 10*time.Minute
			if kind == "ignored_child" {
				run, tests = 25*time.Minute, 20*time.Minute
				if len(o.Packages) != 1 || o.Packages[0] != "./internal/verify" {
					t.Errorf("ignored child changed asset ownership: %v", o.Packages)
				}
			} else if len(o.Packages) != 0 {
				t.Errorf("uncopied Go file owns the asset: %v", o.Packages)
			}
			v498Budgets(t, o, started, run, tests)
		})
	}
}

func TestVerifyBudgetChargesPreparation(t *testing.T) {
	record := v498Spy(t)
	bin := t.TempDir()
	// Inventory is preparation too. Delay the first git probe, then use the
	// ordinary directory fallback so the container sees the real file list.
	s397Write(t, filepath.Join(bin, "git"), "#!/usr/bin/env python3\nimport sys, time\ntime.sleep(2)\nsys.exit(1)\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, dir := range []string{"internal/verify", "internal/plumbing"} {
		t.Run(dir, func(t *testing.T) {
			root := t.TempDir()
			s397Write(t, filepath.Join(root, dir, "code.go"), "package fixture\n", 0o644)
			started := time.Now()
			r, err := (Sandbox{Image: "fixture", ModCache: t.TempDir()}).Run(v498Watchdog(t), root,
				&Plan{Files: []string{dir + "/code.go"}})
			if err != nil || !r.Passed() {
				t.Fatalf("delayed preparation: %+v, %v", r, err)
			}
			all := v498Read(t, record)
			o := all[len(all)-1]
			if time.Unix(0, int64(o.Observed*1e9)).Sub(started) < 2*time.Second {
				t.Fatal("fixture did not delay preparation")
			}
			run, tests := 12*time.Minute, 10*time.Minute
			if dir == "internal/verify" {
				run, tests = 25*time.Minute, 20*time.Minute
			}
			v498Budgets(t, o, started, run, tests)
		})
	}
	for _, parent := range []bool{false, true} {
		name := "override"
		if parent {
			name = "parent_cancellation"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			s397Write(t, filepath.Join(root, "internal/verify/code.go"), "package verify\n", 0o644)
			ctx, cancel := context.WithCancel(v498Watchdog(t))
			defer cancel()
			s := Sandbox{Image: "fixture", ModCache: t.TempDir(), Timeout: 500 * time.Millisecond}
			if parent {
				s.Timeout = 0
				timer := time.AfterFunc(500*time.Millisecond, cancel)
				defer timer.Stop()
			}
			before := len(v498Read(t, record))
			r, err := s.Run(ctx, root, &Plan{Files: []string{"internal/verify/code.go"}})
			if err != nil || r.Passed() || r.DeadlineExceeded != !parent || r.Interrupted != parent {
				t.Fatalf("preparation must stop with a failed report: %+v, %v", r, err)
			}
			if len(v498Read(t, record)) != before {
				t.Fatal("interrupted preparation started the container")
			}
		})
	}
}

func TestQueuedVerifyBudgetStartsAtAdmission(t *testing.T) {
	record := v498Spy(t)
	q, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	root := t.TempDir()
	s397Write(t, filepath.Join(root, "internal/verify/code.go"), "package verify\n", 0o644)
	plan := &Plan{Files: []string{"internal/verify/code.go"}}
	type transition struct {
		waiting bool
		at      time.Time
	}
	status := make(chan transition, 2)
	ctx := WithCheckStatus(v498Watchdog(t), func(waiting bool) { status <- transition{waiting, time.Now()} })
	b, err := q.register(ctx, Sandbox{Image: "fixture", ModCache: t.TempDir()}, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer b.finish()
	q.slots <- struct{}{}
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := (Sandbox{Checks: &CheckClient{Address: q.listener.Addr().String(), Build: b.id}}).Run(ctx, root, plan)
		done <- outcome{r, err}
	}()
	select {
	case state := <-status:
		if !state.waiting {
			t.Fatal("check did not wait for admission")
		}
	case <-ctx.Done():
		t.Fatal("check never queued")
	}
	// Make queue time larger than the deadline assertion's tolerance.
	time.Sleep(2 * time.Second)
	<-q.slots
	var admitted time.Time
	select {
	case state := <-status:
		if state.waiting {
			t.Fatal("check did not leave the queue")
		}
		admitted = state.at
	case <-ctx.Done():
		t.Fatal("check never admitted")
	}
	select {
	case out := <-done:
		if out.err != nil || !out.result.Passed() {
			t.Fatalf("queued check: %+v, %v", out.result, out.err)
		}
	case <-ctx.Done():
		t.Fatal("admitted check never returned")
	}
	all := v498Read(t, record)
	if len(all) != 1 {
		t.Fatalf("got %d checks, want one", len(all))
	}
	v498Budgets(t, all[0], admitted, 25*time.Minute, 20*time.Minute)
}
