package factory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

// A refused publication is still the same ratified work. A new watcher
// must hear the refusal too; restarting must not silently retry it.
type publication434GitHub struct {
	*fakeGitHub
	answer   error
	attempts int
}

func (g *publication434GitHub) CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (github.PullRequest, error) {
	g.attempts++
	if g.answer != nil {
		return github.PullRequest{}, g.answer
	}
	return g.fakeGitHub.CreatePullRequest(ctx, pr)
}

func publication434Proposal(t *testing.T, plumbing bool) (*rig, *publication434GitHub) {
	t.Helper()
	var r *rig
	body := "/invariant solve"
	if plumbing {
		r, _ = plumbingRig(t, "page/page.go")
		body = "Kind: plumbing\n\n" + body
	} else {
		r = newRig(t)
		r.form.forks = nil
	}
	g := &publication434GitHub{fakeGitHub: r.gh}
	r.f.GitHub = g
	r.gh.open(1, "gitdek", "Publish the ratified work", body)
	r.poll()
	p := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:")[:hashChars])
	return r, g
}

func publication434Poll(t *testing.T, r *rig) {
	t.Helper()
	// A handled refusal may also be returned for the operator's log. The
	// observable contract is the issue's message, label and lack of retries.
	if err := r.f.Poll(context.Background()); err != nil {
		t.Logf("poll: %v", err)
	}
}

func publication434Reports(g *fakeGitHub, message string) int {
	n := 0
	for _, c := range g.comments[1] {
		if c.User.Login == g.me && strings.Contains(c.Body, message) {
			n++
		}
	}
	return n
}

func publication434Restart(t *testing.T, r *rig) {
	t.Helper()
	clone := Clone{Dir: filepath.Join(t.TempDir(), "clone"), Remote: r.origin,
		Name: "Joseph", Email: "7275925+gitdek@users.noreply.github.com"}
	if err := clone.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := r.f
	r.f = &Factory{Repository: old.Repository, GitHub: old.GitHub, Repo: clone,
		Formalizer: old.Formalizer, Builder: old.Builder, Plumbing: old.Plumbing,
		Base: old.Base, Projects: old.Projects, Work: t.TempDir(), Check: old.Check,
		Agent: old.Agent, Agents: old.Agents, Self: old.Self, Log: t.Logf}
}

func TestPullRequestRefusalsWaitForAWriter434(t *testing.T) {
	for _, plumbing := range []bool{false, true} {
		t.Run(fmt.Sprintf("plumbing=%v", plumbing), func(t *testing.T) {
			r, g := publication434Proposal(t, plumbing)
			// A retry queued before this refusal is not consent to retry
			// the failure the writer has not seen yet.
			r.gh.say(1, "gitdek", "/invariant retry")
			const message = "Validation Failed: publication rejected (HTTP 422)"
			g.answer = fmt.Errorf("POST repos/o/r/pulls: exit status 1: gh: %s", message)
			publication434Poll(t, r)
			if g.attempts != 1 || publication434Reports(r.gh, message) != 1 || !r.gh.issues[1].HasLabel(LabelHumanReview) {
				t.Fatalf("a permanent PR refusal must be posted once and labeled for a person: attempts=%d reports=%d labels=%v", g.attempts, publication434Reports(r.gh, message), r.gh.labelsOf(1))
			}
			if !strings.Contains(r.gh.comments[1][len(r.gh.comments[1])-1].Body, "/invariant retry") {
				t.Fatal("the refusal must tell the writer how to retry")
			}
			for i := 0; i < 3; i++ {
				publication434Poll(t, r)
			}
			publication434Restart(t, r)
			publication434Poll(t, r)
			r.gh.say(1, "mallory", "/invariant retry")
			publication434Poll(t, r)
			if g.attempts != 1 || publication434Reports(r.gh, message) != 1 || len(r.gh.prs) != 0 {
				t.Fatalf("polls, another watcher and a reader's retry must leave publication stopped: attempts=%d reports=%d PRs=%d", g.attempts, publication434Reports(r.gh, message), len(r.gh.prs))
			}
			g.answer = nil
			r.gh.say(1, "gitdek", "/invariant retry")
			for i := 0; i < 3; i++ {
				publication434Poll(t, r)
			}
			if g.attempts != 2 || len(r.gh.prs) != 1 || r.gh.issues[1].HasLabel(LabelHumanReview) {
				t.Fatalf("a writer's retry must publish the work once: attempts=%d PRs=%d labels=%v", g.attempts, len(r.gh.prs), r.gh.labelsOf(1))
			}
			if publication434Reports(r.gh, message) != 1 {
				t.Fatal("resuming must not repeat the refusal report")
			}
		})
	}
	// These answers ask for another attempt. Words such as timeout in a
	// permanent HTTP 422 must not override its status, but these must not
	// strand a publication waiting for a writer.
	for _, answer := range []string{"HTTP 408: request timeout", "HTTP 429: too many requests", "HTTP 403: API rate limit exceeded", "HTTP 403: secondary rate limit", "HTTP 502: Bad Gateway", "cannot execute gh: local permission denied"} {
		t.Run(answer, func(t *testing.T) {
			r, g := publication434Proposal(t, true)
			g.answer = fmt.Errorf("POST repos/o/r/pulls: %s", answer)
			publication434Poll(t, r)
			if g.attempts != 1 || r.gh.issues[1].HasLabel(LabelHumanReview) || publication434Reports(r.gh, answer) != 0 {
				t.Fatalf("a transient answer must leave publication retryable: attempts=%d labels=%v", g.attempts, r.gh.labelsOf(1))
			}
			g.answer = nil
			publication434Poll(t, r)
			if g.attempts != 2 || len(r.gh.prs) != 1 {
				t.Fatalf("the next poll must retry a transient refusal: attempts=%d PRs=%d", g.attempts, len(r.gh.prs))
			}
		})
	}
}
