package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

const run545ServerError = "remote: Internal Server Error (HTTP 500)"

type run545Reply struct {
	mode, message string
}

type run545Fixture struct {
	clone                                      Clone
	origin, ref, recorded, result, otherRecord string
	otherResult, control                       string
}

// Keep real local Git's objects and compare-and-swap behavior. Only a push
// to this test's run ref and its readback can have their transport fail.
// The shim forwards argv unchanged, including receive-pack and maintenance
// options, and records the actual force-with-lease expectation and commit.
func newRun545Fixture(t *testing.T, operation string, replies ...run545Reply) *run545Fixture {
	t.Helper()
	origin, clone := gitRepos(t)
	f := &run545Fixture{clone: clone, origin: origin, ref: runRef(545, "draft-push"), control: t.TempDir()}
	ctx := context.Background()
	if operation == "Finish" {
		if err := clone.Record(ctx, 545, "draft-push", "the draft's run"); err != nil {
			t.Fatal(err)
		}
		var err error
		_, f.recorded, err = clone.Run(ctx, 545, "draft-push")
		if err != nil || f.recorded == "" {
			t.Fatalf("read the initial record: %s, %v", f.recorded, err)
		}
	}
	resultDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(resultDir, "result.json"), []byte(`{"proposal":"saved work"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	f.result, err = clone.Save(ctx, resultDir, "the completed draft", "")
	if err != nil {
		t.Fatal(err)
	}
	tree := git(t, origin, "rev-parse", "main^{tree}")
	f.otherRecord = git(t, origin, "-c", "user.name=Other", "-c", "user.email=other@example.com", "commit-tree", tree, "-m", recordedMark+"another watcher's run\n\nanother nonce")
	f.otherResult = git(t, origin, "-c", "user.name=Other", "-c", "user.email=other@example.com", "commit-tree", tree, "-m", "another watcher's result")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for i, reply := range replies {
		for suffix, value := range map[string]string{"mode": reply.mode, "message": reply.message} {
			if err := os.WriteFile(filepath.Join(f.control, fmt.Sprintf("%d.%s", i+1, suffix)), []byte(value), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(run545Git), 0o755); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"RUN545_GIT": realGit, "RUN545_CONTROL": f.control, "RUN545_REF": f.ref,
		"RUN545_ORIGIN": origin, "RUN545_OTHER_RECORD": f.otherRecord, "RUN545_OTHER_RESULT": f.otherResult,
	} {
		t.Setenv(key, value)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

const run545Git = `#!/bin/sh
set -eu
operation=
target=
old=missing-lease-option
commit=
for arg in "$@"; do
  case "$arg" in
    push|ls-remote) operation=$arg ;;
    "--force-with-lease=$RUN545_REF:"*) old=${arg#--force-with-lease=$RUN545_REF:} ;;
    "$RUN545_REF") target=yes ;;
    *:"$RUN545_REF") target=yes; commit=${arg%:$RUN545_REF} ;;
  esac
done
if [ "$target" != yes ]; then exec "$RUN545_GIT" "$@"; fi
if [ "$operation" = ls-remote ]; then
  printf 'read\n' >> "$RUN545_CONTROL/trace"
  if [ -f "$RUN545_CONTROL/unreadable" ]; then
    printf 'readback unavailable (HTTP 503)\n' >&2
    exit 1
  fi
  exec "$RUN545_GIT" "$@"
fi
if [ "$operation" != push ]; then exec "$RUN545_GIT" "$@"; fi
count=0
if [ -f "$RUN545_CONTROL/count" ]; then count=$(cat "$RUN545_CONTROL/count"); fi
count=$((count + 1))
printf '%s\n' "$count" > "$RUN545_CONTROL/count"
printf 'push|%s|%s\n' "$old" "$commit" >> "$RUN545_CONTROL/trace"
mode=unexpected
if [ -f "$RUN545_CONTROL/$count.mode" ]; then mode=$(cat "$RUN545_CONTROL/$count.mode"); fi
case "$mode" in
  ok) exec "$RUN545_GIT" "$@" ;;
  fail) ;;
  land) "$RUN545_GIT" "$@" ;;
  move-record) "$RUN545_GIT" -C "$RUN545_ORIGIN" update-ref "$RUN545_REF" "$RUN545_OTHER_RECORD" ;;
  move-result) "$RUN545_GIT" -C "$RUN545_ORIGIN" update-ref "$RUN545_REF" "$RUN545_OTHER_RESULT" ;;
  delete) "$RUN545_GIT" -C "$RUN545_ORIGIN" update-ref -d "$RUN545_REF" ;;
  unreadable) : > "$RUN545_CONTROL/unreadable" ;;
  *) printf 'unexpected extra push\n' >&2; exit 1 ;;
esac
cat "$RUN545_CONTROL/$count.message" >&2
printf '\n' >&2
exit 1
`

func (f *run545Fixture) call(ctx context.Context, repo Repo, operation string) error {
	if operation == "Record" {
		return repo.Record(ctx, 545, "draft-push", "the draft's run")
	}
	return repo.Finish(ctx, 545, "draft-push", f.result)
}

func (f *run545Fixture) trace(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.control, "trace"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

func (f *run545Fixture) attempts(t *testing.T) [][2]string {
	t.Helper()
	var attempts [][2]string
	for _, line := range f.trace(t) {
		if fields := strings.Split(line, "|"); len(fields) == 3 && fields[0] == "push" {
			attempts = append(attempts, [2]string{fields[1], fields[2]})
		}
	}
	return attempts
}

func (f *run545Fixture) checkAttempts(t *testing.T, count, failed int) {
	t.Helper()
	attempts := f.attempts(t)
	if len(attempts) != count {
		t.Errorf("pushes = %d; want %d: %v", len(attempts), count, attempts)
	}
	for i, a := range attempts {
		if a[0] != f.recorded || a[1] == "" || (i > 0 && a[1] != attempts[0][1]) {
			t.Errorf("push %d changed its commit or expected ref: %v (first %v, expected ref %q)", i+1, a, attempts[0], f.recorded)
		}
	}
	// Each failed push must be reconciled before another push or return.
	pushes, reconciled := 0, 0
	read := false
	for _, line := range f.trace(t) {
		if strings.HasPrefix(line, "push|") {
			if pushes > 0 && !read {
				t.Error("retried a push without first reading the run ref")
			}
			pushes++
			read = false
		} else if line == "read" && pushes > 0 && !read {
			reconciled++
			read = true
		}
	}
	if reconciled < failed {
		t.Errorf("read back %d failed pushes; want %d", reconciled, failed)
	}
}

func (f *run545Fixture) remote(t *testing.T) string {
	t.Helper()
	return git(t, f.origin, "for-each-ref", "--format=%(objectname)", f.ref)
}

func (f *run545Fixture) checkPublished(t *testing.T, operation string) {
	t.Helper()
	attempts := f.attempts(t)
	if len(attempts) == 0 {
		t.Fatal("no push attempted")
	}
	if got := f.remote(t); got != attempts[0][1] {
		t.Errorf("remote ref = %s; want this call's commit %s", got, attempts[0][1])
	}
	reader := Clone{Dir: filepath.Join(t.TempDir(), "reader"), Remote: f.origin, Name: f.clone.Name, Email: f.clone.Email}
	if err := reader.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, sha, err := reader.Run(context.Background(), 545, "draft-push")
	wantState := RunRecorded
	if operation == "Finish" {
		wantState = RunDone
	}
	if err != nil || state != wantState || sha != attempts[0][1] {
		t.Fatalf("another watcher reads (%v, %s, %v); want (%v, %s, nil)", state, sha, err, wantState, attempts[0][1])
	}
	if operation == "Finish" {
		dir := t.TempDir()
		if err := reader.Load(context.Background(), sha, dir); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "result.json"))
		if err != nil || string(b) != `{"proposal":"saved work"}` {
			t.Fatalf("saved work = %s, %v", b, err)
		}
	}
}

func TestRunPush545ReconcilesLandedWrites(t *testing.T) {
	for _, operation := range []string{"Finish", "Record"} {
		for _, message := range []string{run545ServerError, "fatal: The requested URL returned error: 403"} {
			t.Run(operation+"/"+message, func(t *testing.T) {
				f := newRun545Fixture(t, operation, run545Reply{"land", message}, run545Reply{"ok", ""})
				synctest.Test(t, func(t *testing.T) {
					start := time.Now()
					if err := f.call(context.Background(), f.clone, operation); err != nil {
						t.Errorf("the failed push landed, but %s returned %v", operation, err)
					}
					if elapsed := time.Since(start); elapsed != 0 {
						t.Errorf("waited %v after the intended commit was visible", elapsed)
					}
					f.checkAttempts(t, 1, 1)
					f.checkPublished(t, operation)
					err := f.call(context.Background(), f.clone, operation)
					if operation == "Finish" {
						if err == nil || len(f.attempts(t)) != 1 {
							t.Errorf("a separate Finish on a completed run must be refused without a push: %v", err)
						}
					} else {
						attempts := f.attempts(t)
						if !errors.Is(err, ErrRecorded) || len(attempts) != 2 || attempts[0][1] == attempts[1][1] {
							t.Errorf("a separate Record must use a fresh nonce and return ErrRecorded: %v, %v", err, attempts)
						}
					}
				})
			})
		}
	}
}

func TestRunPush545RetriesWithBoundedBackoff(t *testing.T) {
	waits := []time.Duration{0, 15 * time.Second, 45 * time.Second, 105 * time.Second, 225 * time.Second}
	for _, operation := range []string{"Finish", "Record"} {
		for failures := 1; failures <= 4; failures++ {
			t.Run(fmt.Sprintf("%s/%d_failures", operation, failures), func(t *testing.T) {
				replies := make([]run545Reply, failures)
				for i := range replies {
					replies[i] = run545Reply{"fail", run545ServerError}
				}
				f := newRun545Fixture(t, operation, append(replies, run545Reply{"ok", ""})...)
				synctest.Test(t, func(t *testing.T) {
					start := time.Now()
					if err := f.call(context.Background(), f.clone, operation); err != nil {
						t.Errorf("%s did not survive %d temporary failures: %v", operation, failures, err)
					}
					if elapsed := time.Since(start); elapsed != waits[failures] {
						t.Errorf("waited %v; want %v", elapsed, waits[failures])
					}
					f.checkAttempts(t, failures+1, failures)
					f.checkPublished(t, operation)
				})
			})
		}
		for _, message := range []string{
			"fatal: The requested URL returned error: 502",
			"remote: fatal error in commit_refs_rules",
			"fatal: Operation timed out after 30000 milliseconds",
			"fatal: the remote end hung up unexpectedly",
		} {
			t.Run(operation+"/"+message, func(t *testing.T) {
				f := newRun545Fixture(t, operation, run545Reply{"fail", message}, run545Reply{"ok", ""})
				synctest.Test(t, func(t *testing.T) {
					start := time.Now()
					if err := f.call(context.Background(), f.clone, operation); err != nil {
						t.Errorf("%s failed instead of retrying: %v", operation, err)
					}
					if elapsed := time.Since(start); elapsed != 15*time.Second {
						t.Errorf("waited %v; want 15s", elapsed)
					}
					f.checkAttempts(t, 2, 1)
					f.checkPublished(t, operation)
				})
			})
		}
	}
}

func TestRunPush545ExhaustsWithLastError(t *testing.T) {
	for _, operation := range []string{"Finish", "Record"} {
		t.Run(operation, func(t *testing.T) {
			var replies []run545Reply
			for i := 1; i <= 5; i++ {
				replies = append(replies, run545Reply{"fail", fmt.Sprintf("%s attempt-%d", run545ServerError, i)})
			}
			f := newRun545Fixture(t, operation, replies...)
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				err := f.call(context.Background(), f.clone, operation)
				if err == nil || !strings.Contains(err.Error(), "attempt-5") || !strings.Contains(err.Error(), "HTTP 500") {
					t.Errorf("exhausted error = %v; want the fifth push's diagnostic", err)
				}
				if elapsed := time.Since(start); elapsed != 225*time.Second {
					t.Errorf("waited %v; want exactly four waits totaling 225s", elapsed)
				}
				f.checkAttempts(t, 5, 5)
				if got := f.remote(t); got != f.recorded {
					t.Errorf("failed writes changed ref to %s; want %s", got, f.recorded)
				}
			})
		})
	}
}

func TestRunPush545StopsOnRefusalOrChangedRef(t *testing.T) {
	for _, operation := range []string{"Finish", "Record"} {
		for _, earlierFailures := range []int{0, 1} {
			for _, mode := range []string{"refusal", "move-record", "move-result", "delete", "unreadable"} {
				if operation == "Record" && mode == "delete" {
					continue // An absent ref is Record's original expectation.
				}
				t.Run(fmt.Sprintf("%s/%s/after_%d_failures", operation, mode, earlierFailures), func(t *testing.T) {
					reply := run545Reply{mode, run545ServerError + " terminal-answer"}
					if mode == "refusal" {
						reply = run545Reply{"fail", "fatal: The requested URL returned error: 403\nremote: fatal error in commit_refs_rules; operation timed out"}
					}
					var replies []run545Reply
					if earlierFailures > 0 {
						replies = append(replies, run545Reply{"fail", run545ServerError})
					}
					f := newRun545Fixture(t, operation, append(replies, reply)...)
					synctest.Test(t, func(t *testing.T) {
						start := time.Now()
						err := f.call(context.Background(), f.clone, operation)
						if err == nil {
							t.Error("unsafe write returned success")
						}
						if operation == "Record" && strings.HasPrefix(mode, "move-") && !errors.Is(err, ErrRecorded) {
							t.Errorf("another writer's commit must give ErrRecorded, got %v", err)
						}
						if elapsed := time.Since(start); elapsed != time.Duration(earlierFailures)*15*time.Second {
							t.Errorf("waited %v; must stop immediately on %s", elapsed, mode)
						}
						f.checkAttempts(t, earlierFailures+1, earlierFailures+1)
						want := f.recorded
						switch mode {
						case "move-record":
							want = f.otherRecord
						case "move-result":
							want = f.otherResult
						case "delete":
							want = ""
						}
						if got := f.remote(t); got != want {
							t.Errorf("ref = %s; want %s, preserving the other writer's update", got, want)
						}
					})
				})
			}
		}
	}
}

func TestRunPush545HonorsContextAndLease(t *testing.T) {
	for _, operation := range []string{"Finish", "Record"} {
		for _, kind := range []string{"cancel", "deadline", "lease", "tenure", "no-step"} {
			for _, during := range []time.Duration{5 * time.Second, 25 * time.Second} {
				t.Run(fmt.Sprintf("%s/%s/%s", operation, kind, during), func(t *testing.T) {
					f := newRun545Fixture(t, operation, run545Reply{"fail", run545ServerError}, run545Reply{"fail", run545ServerError}, run545Reply{"ok", ""})
					synctest.Test(t, func(t *testing.T) {
						watcher := &Factory{Repo: f.clone, Holder: "watcher-545", LeaseFor: 5 * time.Minute, Now: time.Now}
						if held, err := watcher.hold(context.Background()); err != nil || !held {
							t.Fatalf("take lease: %v, %v", held, err)
						}
						var ctx context.Context
						var cancel context.CancelFunc
						wantErr := context.Canceled
						if kind == "deadline" {
							ctx, cancel = context.WithTimeout(context.Background(), during)
							wantErr = context.DeadlineExceeded
						} else {
							ctx, cancel = context.WithCancel(context.Background())
						}
						defer cancel()
						if kind != "no-step" {
							ctx = watcher.inTenure(ctx)
						}
						if kind == "lease" {
							wantErr = errLeaseLost
						} else if kind == "tenure" {
							wantErr = errStaleTenure
						} else if kind == "no-step" {
							wantErr = errNoStep
						}
						if kind != "deadline" && kind != "no-step" {
							timer := time.AfterFunc(during, func() {
								if kind == "cancel" {
									cancel()
									return
								}
								watcher.lease.mu.Lock()
								defer watcher.lease.mu.Unlock()
								if kind == "lease" {
									watcher.lease.until = time.Time{}
								} else {
									watcher.lease.tenure++
								}
							})
							defer timer.Stop()
						}
						start := time.Now()
						err := f.call(ctx, leasedRepo{Repo: f.clone, f: watcher}, operation)
						if !errors.Is(err, wantErr) {
							t.Errorf("%s returned %v; want %v", operation, err, wantErr)
						}
						count := 1
						if during > 15*time.Second {
							count = 2
						}
						if kind == "no-step" {
							count = 0
						}
						f.checkAttempts(t, count, count)
						elapsed := time.Since(start)
						if (kind == "cancel" || kind == "deadline") && elapsed != during {
							t.Errorf("context ended at %v but call returned at %v", during, elapsed)
						}
						if got := f.remote(t); got != f.recorded {
							t.Errorf("an unauthorized retry changed the ref to %s", got)
						}
					})
				})
			}
		}
	}
}

func TestRunPush545WaitAllowsLeaseRenewal(t *testing.T) {
	for _, operation := range []string{"Finish", "Record"} {
		t.Run(operation, func(t *testing.T) {
			f := newRun545Fixture(t, operation, run545Reply{"fail", run545ServerError}, run545Reply{"ok", ""})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- f.call(ctx, f.clone, operation) }()
			// This check deliberately uses a short wall-clock deadline: a mutex
			// held through backoff must not deadlock a virtual-time test.
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			seenPush, seenRead := false, false
			for !seenRead {
				seenPush = false
				for _, line := range f.trace(t) {
					if strings.HasPrefix(line, "push|") {
						seenPush = true
					} else if line == "read" && seenPush {
						seenRead = true
					}
				}
				if seenRead {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("%s gave up before entering backoff: %v", operation, err)
				case <-deadline.C:
					cancel()
					<-done
					t.Fatal("did not reach the first push's readback")
				case <-time.After(time.Millisecond):
				}
			}
			renewed := make(chan error, 1)
			go func() {
				sha, _, err := f.clone.Lease(ctx)
				if err == nil {
					_, err = f.clone.PushLease(ctx, sha, LeaseRecord{Holder: "watcher-545", Until: time.Now().Add(5 * time.Minute)})
				}
				renewed <- err
			}()
			select {
			case err := <-renewed:
				if err != nil {
					t.Errorf("renew lease during backoff: %v", err)
				}
			case <-deadline.C:
				cancel()
				<-renewed
				<-done
				t.Fatal("run backoff held the clone and blocked lease renewal")
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Errorf("canceling backoff returned %v; want context.Canceled", err)
			}
			f.checkAttempts(t, 1, 1)
			if got := f.remote(t); got != f.recorded {
				t.Errorf("canceled backoff changed the run ref to %s", got)
			}
		})
	}
}
