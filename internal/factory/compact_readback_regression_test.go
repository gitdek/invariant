package factory

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type compactReadFailure struct {
	Repo
	reads, failAt int
}

func (r *compactReadFailure) Show(ctx context.Context, commit, path string) ([]byte, error) {
	if path == "result.json" {
		r.reads++
		if r.reads == r.failAt {
			return nil, errors.New("saved result temporarily unavailable")
		}
	}
	return r.Repo.Show(ctx, commit, path)
}

func TestCompactReadbackFailureKeepsCompletedDraftPending(t *testing.T) {
	result := saved790Result(t, true)
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("read=%d", failAt), func(t *testing.T) {
			r := newRig(t)
			const base = "https://dashboard.example/install"
			r.f.DashboardURL = base
			form := &markerCapacityFormalizer{result: result}
			r.f.Formalizer = form
			r.gh.open(911, "gitdek", "Publish durable evidence", "/invariant solve")
			repo := r.f.Repo
			r.f.Repo = &compactReadFailure{Repo: repo, failAt: failAt}
			if err := r.f.Poll(t.Context()); err == nil {
				t.Fatal("unavailable completed evidence must leave the command pending")
			}
			state, commit, err := repo.Run(t.Context(), 911, "draft-issue")
			if err != nil || state != RunDone || len(r.gh.comments[911]) != 0 || form.calls != 1 {
				t.Fatalf("read failure consumed the command or lost the completed run: %v", err)
			}
			r.f.Repo = repo
			saved790Fresh(t, r)
			r.f.DashboardURL = base
			r.poll()
			post := r.expect(911, KindProposal, LabelProposal)
			_, saved, err := post.Marker.ResolveDraft(t.Context(), r.f.Repository, 911, repo.Run, repo.Show)
			if err != nil || saved == nil || !draftEqual(saved.Proposal, result.Proposal) || form.calls != 1 {
				t.Fatalf("retry did not recover the same completed evidence: %v", err)
			}
			_, again, err := repo.Run(t.Context(), 911, "draft-issue")
			if err != nil || again != commit {
				t.Fatal("retry replaced the completed record")
			}
		})
	}
}
