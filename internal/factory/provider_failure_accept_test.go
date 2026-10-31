package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

type providerFailedBuilder struct {
	*fakeBuilder
	err error
}

func (b providerFailedBuilder) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	r, err := b.fakeBuilder.Build(ctx, dir, out, amend)
	if err != nil {
		return r, err
	}
	return r, b.err
}

func TestProviderFailureIsPostedAfterSavingTheBuild(t *testing.T) {
	// A failed gate produces a saved result; publish reconstructs the error
	// from its text, so a post cannot rely on the original error's Go type.
	const last = "Selected model is at capacity. Please try a different model. (last attempt)"
	r := newRig(t)
	r.build.pass = false
	r.f.Builder = providerFailedBuilder{fakeBuilder: r.build,
		err: errors.New("codex provider unavailable after 4 attempts: " + last)}
	ratified(t, r)
	p := r.expect(1, KindFailed, LabelHumanReview)
	for _, want := range []string{"provider unavailable", last, "/invariant retry", "build again"} {
		if !strings.Contains(p.Comment.Body, want) {
			t.Errorf("the failure post lost %q:\n%s", want, p.Comment.Body)
		}
	}
	if len(r.build.built) != 1 {
		t.Errorf("factory repeated the whole build %d times instead of retrying only the agent run", len(r.build.built))
	}
	providerFailureHasNoPR(t, r, p)
}

func TestProviderExhaustionCannotPublishAPassingBuild(t *testing.T) {
	// Use a real backend error, including its classification, without a live
	// service. Both agent and review errors must survive saving and loading
	// the build even when the leftover code passes the final gate.
	backend, calls := providerFactoryCLI(t, "review", true)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, runErr := backend.Run(ctx, synth.Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second})
	if runErr == nil {
		t.Fatal("the exhausted run unexpectedly succeeded")
	}
	if n := providerFactoryAttempts(t, calls); n != 4 {
		t.Errorf("provider attempts = %d; want four before exhaustion", n)
	}
	for _, stage := range []string{"agent", "review"} {
		t.Run(stage, func(t *testing.T) {
			r := newRig(t)
			r.f.Builder = providerFailedBuilder{fakeBuilder: r.build, err: fmt.Errorf("the %s's run failed: %w", stage, runErr)}
			ratified(t, r)
			p := r.expect(1, KindFailed, LabelHumanReview)
			for _, want := range []string{"provider", "unavailable", providerFactoryLastError("review"), "/invariant retry", "build again"} {
				if !strings.Contains(p.Comment.Body, want) {
					t.Errorf("the post lost %q:\n%s", want, p.Comment.Body)
				}
			}
			providerFailureHasNoPR(t, r, p)
		})
	}
}

func providerFailureHasNoPR(t *testing.T, r *rig, p Post) {
	t.Helper()
	if p.Marker.PR != 0 || len(r.gh.prs) != 0 {
		t.Errorf("an unfinished provider run published a pull request: marker PR=%d, pull requests=%v", p.Marker.PR, r.gh.prs)
	}
	if len(r.gh.merged) != 0 {
		t.Errorf("the unfinished run merged unreviewed work: %v", r.gh.merged)
	}
}

// The scripts stand in for the CLI and the sandbox only. The real backend,
// plumbing.Builder, saved build record, and factory retry protocol run here.
// The failed attempts deliberately leave code that passes the checks.
func TestProviderExhaustionRetryRebuildsAndReviewsTheRatifiedPlan(t *testing.T) {
	for _, stage := range []string{"agent", "review"} {
		t.Run(stage, func(t *testing.T) {
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(providerFactoryDocker), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			r, _ := plumbingRig(t, "page/page.go")
			agent, builds := providerFactoryCLI(t, "agent", stage == "agent")
			reviewer, reviews := providerFactoryCLI(t, "review", stage == "review")
			r.f.Plumbing = plumbing.Builder{Backend: agent, Reviewer: reviewer, Binary: "invariant", TestRuns: 4,
				Timeout: 5 * time.Second, Sandbox: plumbing.Sandbox{Image: "fixture", ModCache: t.TempDir()}}
			r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\n\n/invariant solve")
			r.poll()
			proposal := r.expect(1, KindProposal, LabelProposal)
			hash := proposal.Marker.Proposal.Hash
			r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(hash, "sha256:"))
			providerResumeFailedPost(t, r)
			failed := r.expect(1, KindFailed, LabelHumanReview)
			providerFailureHasNoPR(t, r, failed)
			for _, want := range []string{"provider", "unavailable", providerFactoryLastError(stage), "/invariant retry", "build again"} {
				if !strings.Contains(failed.Comment.Body, want) {
					t.Errorf("the saved %s failure lost %q:\n%s", stage, want, failed.Comment.Body)
				}
			}
			wantBuilds, wantReviews, failedCLI := 4, 0, builds
			if stage == "review" {
				wantBuilds, wantReviews, failedCLI = 1, 4, reviews
			}
			checkCalls := func() {
				t.Helper()
				if b, r := providerFactoryAttempts(t, builds), providerFactoryAttempts(t, reviews); b != wantBuilds || r != wantReviews {
					t.Errorf("agent attempts=%d, review attempts=%d; want %d and %d", b, r, wantBuilds, wantReviews)
				}
			}
			checkCalls()

			// CI success must never substitute for an unfinished review. If a
			// regression publishes a draft, make its CI green before retrying.
			for n := range r.gh.prs {
				r.gh.ci(n, "success")
			}
			before := len(r.gh.posts(1))
			r.poll()
			if len(r.gh.posts(1)) != before {
				t.Error("the failed build posted again without a writer's retry")
			}
			checkCalls()
			if err := os.Remove(filepath.Join(failedCLI, "fail")); err != nil {
				t.Fatal(err)
			}
			r.gh.say(1, "gitdek", "/invariant retry")
			r.poll()
			retrying := r.expect(1, KindRatified, LabelBuilding)
			if retrying.Marker.PR != 0 || retrying.Marker.Hash != hash {
				t.Fatalf("retry must rebuild the same ratified plan without a pull request: %+v", retrying.Marker)
			}
			r.poll()
			pr := r.expect(1, KindPR, LabelPR)
			wantBuilds++
			wantReviews++
			checkCalls()
			if len(r.gh.prs) != 1 || r.gh.prs[pr.Marker.PR].Draft || len(r.gh.merged) != 0 {
				t.Fatalf("a reviewed retry should open its first ready PR and wait for CI: PRs=%v, merged=%v", r.gh.prs, r.gh.merged)
			}
			seen, err := os.ReadFile(filepath.Join(reviews, fmt.Sprintf("reviewed.%d", wantReviews)))
			if err != nil || !strings.Contains(string(seen), fmt.Sprintf("// build attempt %d", wantBuilds)) {
				t.Fatalf("the retry's review must read the rebuilt code: %q, %v", seen, err)
			}
			if !strings.Contains(r.gh.prBody(pr.Marker.PR), "VERDICT: approve") {
				t.Error("the retry's pull request omitted its completed review")
			}
			r.gh.ci(pr.Marker.PR, "success")
			r.poll()
			r.expect(1, KindMerged, LabelMerged)
			if len(r.gh.merged) != 1 || r.gh.merged[0] != pr.Marker.PR {
				t.Errorf("only the rebuilt, reviewed PR may merge: %v", r.gh.merged)
			}
		})
	}
}

var errProviderFailurePostInterrupted = errors.New("the watcher stopped before posting the saved provider failure")

type providerFailurePostInterrupter struct{ GitHub }

func (g providerFailurePostInterrupter) PostComment(ctx context.Context, issue int, body string) (github.Comment, error) {
	if marker, ok := DecodeMarker(body); ok && marker.Kind == KindFailed {
		return github.Comment{}, errProviderFailurePostInterrupted
	}
	return g.GitHub.PostComment(ctx, issue, body)
}

func providerResumeFailedPost(t *testing.T, r *rig) {
	t.Helper()
	hub := r.f.GitHub
	r.f.GitHub = providerFailurePostInterrupter{GitHub: hub}
	if err := r.f.Poll(t.Context()); !errors.Is(err, errProviderFailurePostInterrupted) {
		t.Fatalf("expected the watcher to stop before posting the saved failure, got %v", err)
	}
	// This fresh watcher has no copy of the builder's error. It must recover
	// the saved failure without another agent run or a draft pull request.
	restarted := freshWatcher(r, hub)
	restarted.Plumbing = r.f.Plumbing
	r.f = restarted
	r.poll()
}

func providerFactoryLastError(stage string) string {
	return fmt.Sprintf("Selected model is at capacity. Please try a different model. (%s attempt 4)", stage)
}

func providerFactoryAttempts(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "count"))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	var n int
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscan(string(b), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func providerFactoryCLI(t *testing.T, stage string, failing bool) (synth.Backend, string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
n=0
[ ! -f "$here/count" ] || n=$(cat "$here/count")
n=$((n + 1))
printf '%s\n' "$n" > "$here/count"
if [ STAGE = agent ]; then
  mkdir -p page
  printf '%s\n' 'package page' 'func Hello() string { return "hello" }' "// build attempt $n" > page/page.go
elif [ -f repo/page/page.go ]; then
  cp repo/page/page.go "$here/reviewed.$n"
fi
if [ -f "$here/fail" ]; then
  printf '%s\n' "{\"type\":\"turn.failed\",\"error\":{\"message\":\"Selected model is at capacity. Please try a different model. (STAGE attempt $n)\"}}"
  exit 1
fi
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"VERDICT: approve"}}' '{"type":"turn.completed"}'
`
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte(strings.ReplaceAll(script, "STAGE", stage)), 0o700); err != nil {
		t.Fatal(err)
	}
	if failing {
		if err := os.WriteFile(filepath.Join(dir, "fail"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var backend synth.Backend = synth.Codex{Binary: bin, Home: t.TempDir(), MaxTurns: 20}
	// The optional interface keeps the test compilable on the base branch,
	// where missing retry behavior must fail the behavioral assertions.
	if configurable, ok := backend.(interface {
		WithProviderWait(func(context.Context, time.Duration) error) synth.Backend
	}); ok {
		backend = configurable.WithProviderWait(func(ctx context.Context, _ time.Duration) error { return ctx.Err() })
	}
	return backend, dir
}

const providerFactoryDocker = `#!/bin/sh
[ "$1" = run ] || exit 0
src=
for a in "$@"; do
  case "$a" in
    *:/src) src=${a%:/src} ;;
  esac
done
echo '@@invariant vet=0'
echo '@@invariant test=0'
if grep -q 'func Hello' "$src/page/page.go" 2>/dev/null; then
  echo '--- PASS: TestPageSaysHello (0.00s)'
  echo '@@invariant accept=0'
else
  echo '--- FAIL: TestPageSaysHello (0.00s)'
  echo '@@invariant accept=1'
fi
`
