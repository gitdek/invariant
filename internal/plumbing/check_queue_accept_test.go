package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

// These tests exercise the whole Builder path with independent checkouts and
// one shared queue. Docker is a controllable process, not a Docker dependency:
// a started directory proves that a sandbox actually began, and a release file
// lets it finish. Nothing relies on the order in which waiters are admitted.
const q458Docker = `#!/bin/sh
[ "$1" = run ] || exit 0
src=
for arg in "$@"; do
  case "$arg" in *:/src) src=${arg%:/src} ;; esac
done
id=$(cat "$src/check-id") || exit 91
n=1
while ! mkdir "$Q458_CONTROL/$id-$n.started" 2>/dev/null; do n=$((n+1)); done
base="$Q458_CONTROL/$id-$n"
trap ': > "$base.ended"' 0
while [ ! -e "$base.release" ] && [ ! -e "$Q458_CONTROL/shutdown" ]; do sleep 0.02; done
echo '@@invariant vet=0'
if [ -e "$base.fail" ]; then
  echo '@@invariant test=1'
  echo '--- FAIL: TestPageSaysHello (0.00s)'
  echo '@@invariant accept=1'
else
  echo '@@invariant test=0'
  echo '--- PASS: TestPageSaysHello (0.00s)'
  echo '@@invariant accept=0'
fi
`

type q458Agent struct {
	runs chan int
	n    int
}

func (*q458Agent) Name() string { return "queue acceptance agent" }

func (a *q458Agent) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	a.n++
	a.runs <- a.n
	source := fmt.Sprintf("package page\n\n// Hello says hello, revision %d.\nfunc Hello() string { return \"hello\" }\n", a.n)
	if err := os.MkdirAll(filepath.Join(job.Workspace, "page"), 0o755); err != nil {
		return synth.Usage{}, err
	}
	if err := os.WriteFile(filepath.Join(job.Workspace, "page", "page.go"), []byte(source), 0o644); err != nil {
		return synth.Usage{}, err
	}
	return synth.Usage{Outcome: "success", Summary: "The candidate is written."}, nil
}

type q458Reviewer struct {
	run func(context.Context) (string, error)
}

func (*q458Reviewer) Name() string { return "queue acceptance reviewer" }

func (r *q458Reviewer) Run(ctx context.Context, _ synth.Job) (synth.Usage, error) {
	if r.run != nil {
		summary, err := r.run(ctx)
		return synth.Usage{Outcome: "success", Summary: summary}, err
	}
	return synth.Usage{Outcome: "success", Summary: "VERDICT: approve"}, nil
}

type q458Fixture struct {
	t       *testing.T
	control string
	mod     string
	queue   *CheckQueue
	ctx     context.Context
}

func q458New(t *testing.T, limit int) *q458Fixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in docker is a shell script")
	}
	f := &q458Fixture{t: t, control: t.TempDir(), mod: t.TempDir()}
	bin := t.TempDir()
	q458Write(t, filepath.Join(bin, "docker"), q458Docker, 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("Q458_CONTROL", f.control)
	var err error
	f.queue, err = NewCheckQueue(limit)
	if err != nil {
		t.Fatalf("NewCheckQueue(%d): %v", limit, err)
	}
	t.Cleanup(func() {
		if err := f.queue.Close(); err != nil {
			t.Errorf("closing the check queue: %v", err)
		}
	})
	// A watchdog provides a failsafe without supplying a Deadline. Builder's
	// execution budget has Done/Err but often no Deadline of its own, which
	// is important to the build-timeout case below.
	ctx, cancel := context.WithCancel(context.Background())
	f.ctx = ctx
	watchdog := time.AfterFunc(30*time.Second, cancel)
	t.Cleanup(func() { watchdog.Stop(); cancel() })
	return f
}

func q458Write(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

type q458Build struct {
	f       *q458Fixture
	id      string
	root    string
	out     string
	builder Builder
	agent   *q458Agent
	status  chan bool
	done    chan struct{}
	cancel  context.CancelFunc
	result  *BuildResult
	err     error
}

func (f *q458Fixture) build(id string) *q458Build {
	t := f.t
	t.Helper()
	root := t.TempDir()
	p := &Plan{
		Name: "a hello page", Summary: "Add a page that says hello.", Files: []string{"page/page.go"},
		Tests: []Test{{Name: "TestPageSaysHello", File: "page/queue_accept_test.go", Says: "The page says hello."}},
		Sources: map[string]string{"page/queue_accept_test.go": `package page
import "testing"
func TestPageSaysHello(t *testing.T) { if Hello() != "hello" { t.Fatal("missing hello") } }
`},
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	l := Lock{Ratified: Ratification{
		By: "gitdek", At: "2026-11-03T15:00:00Z", Issue: 458,
		Comment: "https://github.com/gitdek/invariant/issues/458#issuecomment-1", Proposal: p.Hash(),
	}, Plan: *p}
	if err := l.Write(root); err != nil {
		t.Fatal(err)
	}
	q458Write(t, filepath.Join(root, "check-id"), id+"\n", 0o644)
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "-A"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "-m", "Ratify the fixture plan"},
	} {
		ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.WaitDelay = time.Second
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	a := &q458Agent{runs: make(chan int, 8)}
	return &q458Build{
		f: f, id: id, root: root, out: t.TempDir(), agent: a,
		status: make(chan bool, 32), done: make(chan struct{}),
		builder: Builder{Backend: a, Reviewer: &q458Reviewer{}, Binary: "invariant", TestRuns: 4,
			Timeout: 20 * time.Second, Checks: f.queue,
			Sandbox: Sandbox{Image: "queue-fixture", ModCache: f.mod, Timeout: 10 * time.Second}},
	}
}

func (b *q458Build) start() {
	b.f.t.Helper()
	ctx, cancel := context.WithCancel(b.f.ctx)
	b.cancel = cancel
	ctx = WithCheckStatus(ctx, func(waiting bool) { b.status <- waiting })
	go func() {
		b.result, b.err = b.builder.Build(ctx, b.root, 458, b.out)
		close(b.done)
	}()
	b.f.t.Cleanup(func() {
		cancel()
		// Make every fake process releasable even if an assertion aborts a
		// test before its normal release. Wait before temporary files go away.
		os.WriteFile(filepath.Join(b.f.control, "shutdown"), nil, 0o644)
		select {
		case <-b.done:
		case <-time.After(7 * time.Second):
			b.f.t.Errorf("build %s did not stop during cleanup", b.id)
		}
	})
}

func (b *q458Build) awaitAgent(n int) {
	b.f.t.Helper()
	select {
	case got := <-b.agent.runs:
		if got != n {
			b.f.t.Fatalf("build %s agent invocation = %d, want %d", b.id, got, n)
		}
	case <-b.done:
		b.f.t.Fatalf("build %s ended before agent %d started: result=%+v, error=%v", b.id, n, b.result, b.err)
	case <-time.After(5 * time.Second):
		b.f.t.Fatalf("build %s agent %d did not start while another build held the checks", b.id, n)
	}
}

func (b *q458Build) awaitStatus(want bool) {
	b.f.t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case got := <-b.status:
			if got == want {
				return
			}
		case <-timer.C:
			b.f.t.Fatalf("build %s never reported waiting=%v", b.id, want)
		}
	}
}

func (b *q458Build) marker(n int, suffix string) string {
	return filepath.Join(b.f.control, fmt.Sprintf("%s-%d.%s", b.id, n, suffix))
}

func (b *q458Build) mark(n int, suffix string) {
	b.f.t.Helper()
	q458Write(b.f.t, b.marker(n, suffix), "", 0o644)
}

func (b *q458Build) awaitSandbox(n int) {
	b.f.t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(b.marker(n, "started")); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			b.f.t.Fatal(err)
		}
		select {
		case <-tick.C:
		case <-timer.C:
			b.f.t.Fatalf("build %s sandbox %d never started", b.id, n)
		}
	}
}

func (b *q458Build) sandboxAbsent(n int) {
	b.f.t.Helper()
	if _, err := os.Stat(b.marker(n, "started")); !errors.Is(err, os.ErrNotExist) {
		b.f.t.Fatalf("build %s sandbox %d started without a free check slot (stat error %v)", b.id, n, err)
	}
}

func (b *q458Build) finish() (*BuildResult, error) {
	b.f.t.Helper()
	select {
	case <-b.done:
		return b.result, b.err
	case <-time.After(5 * time.Second):
		b.f.t.Fatalf("build %s did not finish after its checks became runnable", b.id)
		return nil, nil
	}
}

func (b *q458Build) passed() {
	b.f.t.Helper()
	r, err := b.finish()
	if err != nil || r == nil || !r.Built() {
		b.f.t.Fatalf("build %s did not pass: result=%+v, error=%v", b.id, r, err)
	}
}

func TestCheckQueueSerializesBuildChecksWithoutChargingSandboxDeadline(t *testing.T) {
	f := q458New(t, 1)
	first, second := f.build("first"), f.build("second")
	second.builder.Sandbox.Timeout = time.Second
	second.mark(1, "release")
	first.start()
	first.awaitSandbox(1)
	second.start()
	second.awaitAgent(1)
	second.awaitStatus(true)
	// The queued build's sandbox allowance is shorter than this known wait.
	// Its agent has nevertheless already run while the first sandbox runs.
	time.Sleep(1300 * time.Millisecond)
	second.sandboxAbsent(1)
	first.mark(1, "release")
	second.awaitSandbox(1)
	if _, err := os.Stat(first.marker(1, "ended")); err != nil {
		t.Fatalf("the second sandbox began before the first ended: %v", err)
	}
	second.awaitStatus(false)
	first.passed()
	second.passed()
}

func TestCheckQueueConfiguredCapacityAppliesAcrossBuilders(t *testing.T) {
	f := q458New(t, 2)
	first, second, third := f.build("first"), f.build("second"), f.build("third")
	first.start()
	first.awaitSandbox(1)
	second.start()
	second.awaitSandbox(1)
	third.mark(1, "release")
	third.start()
	third.awaitAgent(1)
	third.awaitStatus(true)
	third.sandboxAbsent(1)
	first.mark(1, "release")
	third.awaitSandbox(1)
	if _, err := os.Stat(second.marker(1, "ended")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the third build should run while the second still holds its slot: %v", err)
	}
	third.passed()
	first.passed()
	second.mark(1, "release")
	second.passed()
}

func TestCheckQueueWaitConsumesBuildTimeoutWithoutStartingSandbox(t *testing.T) {
	f := q458New(t, 1)
	first, expired, later := f.build("first"), f.build("expired"), f.build("later")
	expired.builder.Timeout = 1200 * time.Millisecond
	if _, ok := f.ctx.Deadline(); ok {
		t.Fatal("fixture must exercise Builder.Timeout without a parent deadline")
	}
	first.start()
	first.awaitSandbox(1)
	expired.start()
	expired.awaitAgent(1)
	expired.awaitStatus(true)
	r, err := expired.finish()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting past Builder.Timeout: error=%v, want context.DeadlineExceeded", err)
	}
	if r == nil || r.Built() || !strings.Contains(strings.ToLower(r.Summary), "waiting for checks") {
		t.Fatalf("timed-out build must fail and say it was waiting for checks: %+v", r)
	}
	expired.sandboxAbsent(1)
	later.mark(1, "release")
	later.start()
	later.awaitStatus(true)
	first.mark(1, "release")
	first.passed()
	later.passed()
	expired.sandboxAbsent(1)
}

func TestCheckQueueReleasesFailedAndCanceledChecks(t *testing.T) {
	for _, outcome := range []string{"failed", "canceled"} {
		t.Run(outcome, func(t *testing.T) {
			f := q458New(t, 1)
			first, later := f.build("first"), f.build("later")
			first.start()
			first.awaitSandbox(1)
			later.mark(1, "release")
			later.start()
			later.awaitStatus(true)
			later.sandboxAbsent(1)
			if outcome == "failed" {
				first.mark(1, "fail")
				first.mark(1, "release")
			} else {
				first.cancel()
			}
			r, err := first.finish()
			if r == nil || r.Built() {
				t.Fatalf("the %s build was accepted: result=%+v, error=%v", outcome, r, err)
			}
			if outcome == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled checks returned %v, want context.Canceled", err)
			}
			later.passed()
		})
	}
}

func TestCheckQueueAlsoCoversRepairedCandidates(t *testing.T) {
	f := q458New(t, 1)
	repairing, other := f.build("repairing"), f.build("other")
	reviewStarted, finishReview := make(chan struct{}), make(chan struct{})
	reviews := 0
	repairing.builder.Reviewer = &q458Reviewer{run: func(ctx context.Context) (string, error) {
		reviews++
		if reviews > 1 {
			return "VERDICT: approve", nil
		}
		close(reviewStarted)
		select {
		case <-finishReview:
			return "Revise the implementation.\nVERDICT: changes needed", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
	repairing.mark(2, "release")
	repairing.start()
	repairing.awaitAgent(1)
	repairing.awaitSandbox(1)
	other.start()
	other.awaitStatus(true)
	repairing.mark(1, "release")
	other.awaitSandbox(1)
	select {
	case <-reviewStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("review did not run while another build held the checks")
	}
	close(finishReview)
	repairing.awaitAgent(2)
	repairing.awaitStatus(true)
	repairing.sandboxAbsent(2)
	other.mark(1, "release")
	other.passed()
	repairing.passed()
	if reviews != 2 {
		t.Fatalf("repairing build had %d reviews, want two", reviews)
	}
}
