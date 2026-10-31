package factory

import (
	"strings"
	"testing"
)

// A draft its agent's provider never let finish says so (#261): the provider
// was unavailable, the last error, and that drafting again is all it needs,
// naming what it was drafting. A writer's revise then drafts again.
func TestADraftItsProviderStoppedSaysSo(t *testing.T) {
	const last = "codex stopped: Selected model is at capacity. Please try a different model. (attempt 4)"
	const problem = "the agent's run didn't finish, so nothing it left is proposed: codex provider unavailable after 4 attempts: " + last
	for _, kind := range []string{"statements", "plan"} {
		t.Run(kind, func(t *testing.T) {
			r := newRig(t)
			body := "A buffer.\n\n/invariant solve"
			if kind == "plan" {
				r, _ = plumbingRig(t, "page/page.go")
				body = "A page.\n\nKind: plumbing\n\n/invariant solve"
			}
			r.form.forks, r.form.fail = nil, problem
			r.gh.open(1, "gitdek", "Add a thing", body)
			r.poll()
			stuck := r.expect(1, KindStuck, LabelHumanReview)
			for _, want := range []string{"draft the " + kind + " for this issue", "provider", "unavailable", last, "`/invariant revise`"} {
				if !strings.Contains(stuck.Comment.Body, want) {
					t.Errorf("the post lacks %q:\n%s", want, stuck.Comment.Body)
				}
			}
			for _, not := range []string{"that check out", "Say what you'd like"} {
				if strings.Contains(stuck.Comment.Body, not) {
					t.Errorf("the post says %q, as if the issue needed changing:\n%s", not, stuck.Comment.Body)
				}
			}
			if kind == "plan" && strings.Contains(stuck.Comment.Body, "statements") {
				t.Errorf("a plan's post calls it statements:\n%s", stuck.Comment.Body)
			}
			r.form.fail = ""
			r.gh.say(1, "gitdek", "/invariant revise")
			r.poll()
			r.expect(1, KindProposal, LabelProposal)
		})
	}
}
