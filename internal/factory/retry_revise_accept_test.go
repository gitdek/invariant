package factory

import (
	"context"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The refused-revise note must use both sources of known trusted-base
// evidence. Older failure markers can lack a proposal, and a recorded
// FailTrusted remains evidence even when a proposal lists no trusted files.
// A trusted plan's draft whose review asked for changes is one a retry
// builds again, so its note offers that, and still leaves merging the new
// pull request to a person (#271).
func TestRefusedReviseKeepsKnownTrustedBaseEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, file, failure string
		withoutProposal     bool
	}{
		{"legacy trusted failure without a proposal", "internal/factory/page.go", FailTrusted, true},
		{"trusted failure with an empty trusted list", "page/page.go", FailTrusted, false},
		{"trusted proposal after a rejected review", "internal/factory/page.go", FailGate, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, b, failed := retry263Start(t, "review changes", tc.file)
			pr := failed.Marker.PR
			if pr == 0 || failed.Marker.Kind != KindFailed || !r.gh.prs[pr].Draft {
				t.Fatalf("fixture must leave an open failed draft: %+v", failed.Marker)
			}

			// Represent a persisted failure from an older watcher. Change only
			// its marker's available evidence; retain the original comment's
			// author, ID, answered commands, ratification, branch and PR.
			m := failed.Marker
			m.Failure = tc.failure
			if tc.withoutProposal {
				m.Proposal = nil
			}
			for i := range r.gh.comments[1] {
				if r.gh.comments[1][i].ID == failed.Comment.ID {
					r.gh.comments[1][i].Body = post("needs a person", "This failed draft is still open.", m)
				}
			}
			stored := r.expect(1, KindFailed, LabelHumanReview)
			if !reflect.DeepEqual(stored.Marker, m) {
				t.Fatal("fixture did not retain the intended trusted-base evidence")
			}
			if tc.withoutProposal {
				if stored.Marker.Proposal != nil {
					t.Fatal("legacy failure fixture must have no proposal")
				}
			} else if got := len(stored.Marker.Proposal.Plan.Trusted) > 0; got != (tc.failure == FailGate) {
				t.Fatal("fixture must exercise each source of trusted-base evidence independently")
			}

			before := *r.gh.prs[pr]
			prBody := r.gh.prBody(pr)
			builds, drafts, posts := b.calls, len(r.form.requests), len(r.gh.posts(1))
			g := &draftGitHub{fakeGitHub: r.gh}
			r.f.GitHub = g
			command := r.gh.say(1, "gitdek", "/invariant revise")
			r.poll()
			note := r.expect(1, KindNote, LabelHumanReview)
			if !slices.Equal(note.Marker.ReplyTo, []int64{command.ID}) || len(r.gh.posts(1)) != posts+1 {
				t.Fatal("revise must be answered exactly once with a note")
			}
			rebuilds := tc.failure == FailGate
			if rebuilds {
				retry271RebuildWords(t, note.Comment.Body, pr, true)
			} else {
				retry263WatchWords(t, note.Comment.Body, pr, true)
				retry263MergeWords(t, note.Comment.Body, pr, true, true)
			}
			said := strings.NewReplacer("`", "", "’", "'").Replace(retry263Visible(note.Comment.Body))
			// mergePromise alone misses the reported promise: "I'll mark it
			// ready and merge it". Known trusted-base changes get neither.
			readyPromise := regexp.MustCompile(`\b(?:i(?:'ll| will)?|(?:the )?factory(?: will)?) (?:marks?|makes?)[^.!?\n]*\bready\b`)
			factoryMerge := regexp.MustCompile(`\b(?:the )?factory(?: will)? merges?\b`)
			if readyPromise.MatchString(said) || factoryMerge.MatchString(said) {
				t.Errorf("refused revise promises the factory will ready or merge a known trusted-base change:\n%s", note.Comment.Body)
			}
			switch {
			case rebuilds:
				retry271ReviseWords(t, note.Comment.Body, pr)
			case !tc.withoutProposal:
				retry263RedraftWords(t, note.Comment.Body, pr)
			}
			if b.calls != builds || len(r.form.requests) != drafts || len(r.gh.prs) != 1 || len(g.calls) != 0 || len(r.gh.merged) != 0 || !reflect.DeepEqual(*r.gh.prs[pr], before) || r.gh.prBody(pr) != prBody {
				t.Fatal("refusing revise must not build, draft, replace, ready, merge or change the open PR")
			}
			thread, err := r.f.read(context.Background(), *r.gh.issues[1])
			if err != nil {
				t.Fatal(err)
			}
			state, ok := thread.State()
			if !ok || state.Comment.ID != failed.Comment.ID || !reflect.DeepEqual(state.Marker, m) || len(thread.Pending()) != 0 {
				t.Fatalf("the note must answer revise without replacing or hydrating the failed state: %+v", state.Marker)
			}
		})
	}
}
