package factory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

type checks458Agent struct {
	started chan struct{}
	proceed <-chan struct{}
}

func (*checks458Agent) Name() string { return "fake" }

func (a *checks458Agent) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	if job.ReadOnly {
		return synth.Usage{Summary: "VERDICT: approve"}, nil
	}
	if a.started != nil {
		close(a.started)
	}
	if a.proceed != nil {
		select {
		case <-a.proceed:
		case <-ctx.Done():
			return synth.Usage{}, ctx.Err()
		}
	}
	p := filepath.Join(job.Workspace, "page", "page.go")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return synth.Usage{}, err
	}
	err := os.WriteFile(p, []byte("package page\n\nfunc Hello() string { return \"hello\" }\n"), 0o644)
	return synth.Usage{Summary: "The page says hello."}, err
}

func checks458Until(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !ready() {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// The two rigs own separate GitHub fakes and clones. Only their real
// plumbing builders share the watcher's check queue, so observing Steps
// while their polls run needs no access to an unsynchronized fake.
func TestFactoryCheckWaitPreservesTheRunningBuild(t *testing.T) {
	queue, err := plumbing.NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	bin, signals := t.TempDir(), t.TempDir()
	const docker = `#!/bin/sh
[ "$1" = run ] || exit 0
which=
for arg in "$@"; do
  case "$arg" in
    checks-458-first) which=first ;;
    checks-458-second) which=second ;;
  esac
done
[ -n "$which" ] || exit 2
: > "$CHECKS458_SIGNALS/$which-started"
while [ ! -f "$CHECKS458_SIGNALS/$which-release" ]; do sleep 0.01; done
echo '@@invariant vet=0'
echo '@@invariant test=0'
echo '--- PASS: TestPageSaysHello (0.00s)'
echo '@@invariant accept=0'
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(docker), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CHECKS458_SIGNALS", signals)
	first, _ := plumbingRig(t, "page/page.go")
	second, _ := plumbingRig(t, "page/page.go")
	for _, r := range []*rig{first, second} {
		r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\n\n/invariant solve")
		r.poll()
		proposal := r.expect(1, KindProposal, LabelProposal)
		r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:"))
	}
	writerStarted, letWriterFinish := make(chan struct{}), make(chan struct{})
	firstAgent := &checks458Agent{}
	secondAgent := &checks458Agent{started: writerStarted, proceed: letWriterFinish}
	for i, r := range []*rig{first, second} {
		agent, image := firstAgent, "checks-458-first"
		if i == 1 {
			agent, image = secondAgent, "checks-458-second"
		}
		r.f.Plumbing = plumbing.Builder{Backend: agent, Reviewer: agent, Binary: "invariant", TestRuns: 2,
			Timeout: time.Minute, Checks: queue, Sandbox: plumbing.Sandbox{Image: image, ModCache: t.TempDir()}}
	}
	observer := finished269Observe(t, second.f)
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	type poll struct {
		done chan struct{}
		err  error
	}
	var polls []*poll
	start := func(r *rig) *poll {
		p := &poll{done: make(chan struct{})}
		polls = append(polls, p)
		go func() {
			p.err = r.f.Poll(ctx)
			close(p.done)
		}()
		return p
	}
	release := func(which string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(signals, which+"-release"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(signals, name))
		return err == nil
	}
	defer func() {
		// End every poll before its fake, clone and files are cleaned up,
		// including an assertion failing while either Docker stand-in waits.
		cancel()
		_ = os.WriteFile(filepath.Join(signals, "first-release"), nil, 0o600)
		_ = os.WriteFile(filepath.Join(signals, "second-release"), nil, 0o600)
		for _, p := range polls {
			<-p.done
		}
	}()
	start(first)
	checks458Until(t, "the first build's sandbox", func() bool { return exists("first-started") })
	start(second)
	checks458Until(t, "another build's agent during the first sandbox", func() bool {
		select {
		case <-writerStarted:
			return true
		default:
			return false
		}
	})
	before := second.f.Steps()
	if len(before) != 1 || before[0].Doing != "building" || before[0].Run == "" || before[0].Agent == "" {
		t.Fatalf("the writing agent has no original build identity: %+v", before)
	}
	original := before[0]
	close(letWriterFinish)
	checks458Until(t, "the factory's waiting-for-checks status", func() bool {
		steps := second.f.Steps()
		return len(steps) == 1 && steps[0].Doing == "waiting for checks"
	})
	assertIdentity := func(phase string) {
		t.Helper()
		steps := second.f.Steps()
		if len(steps) != 1 || steps[0].Issue != original.Issue || steps[0].Run != original.Run || steps[0].Agent != original.Agent || !steps[0].Since.Equal(original.Since) || steps[0].Ended != nil {
			t.Fatalf("%s changed the original build identity: before %+v, now %+v", phase, original, steps)
		}
		observer.mu.Lock()
		ends := len(observer.ended)
		observer.mu.Unlock()
		if ends != 0 {
			t.Fatalf("%s published %d premature build completions", phase, ends)
		}
	}
	assertIdentity("waiting")
	if exists("second-started") {
		t.Fatal("the second sandbox started while the first was held")
	}
	release("first")
	checks458Until(t, "the admitted second sandbox", func() bool { return exists("second-started") })
	checks458Until(t, "the resumed building status", func() bool {
		steps := second.f.Steps()
		return len(steps) == 1 && steps[0].Doing == "building"
	})
	assertIdentity("admission")
	release("second")
	for _, p := range polls {
		checks458Until(t, "a completed build poll", func() bool {
			select {
			case <-p.done:
				return true
			default:
				return false
			}
		})
		if p.err != nil {
			t.Fatal(p.err)
		}
	}
	first.expect(1, KindPR, LabelPR)
	second.expect(1, KindPR, LabelPR)
	observer.mu.Lock()
	finished := append([]finished269Step(nil), observer.ended...)
	observer.mu.Unlock()
	if len(finished) != 1 || finished[0].Run != original.Run || finished[0].Agent != original.Agent || !finished[0].Since.Equal(original.Since) || finished[0].Ended == nil || finished[0].Ended.Before(original.Since) {
		t.Fatalf("waiting and admission must end the original build exactly once: %+v", finished)
	}
	if steps := second.f.Steps(); len(steps) != 0 {
		t.Fatalf("completed build remains active: %+v", steps)
	}
}
