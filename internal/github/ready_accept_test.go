package github

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitHub's REST API can't take a pull request out of draft: only GraphQL's
// markPullRequestReadyForReview mutation does, given the pull request's node
// ID (#145). MarkReady runs that mutation through gh on the pull request's
// node ID, and when GitHub refuses, it's an error that says what GitHub said.
func TestMarkReadyRunsGraphQLsMutationOnThePullRequest(t *testing.T) {
	for _, refused := range []string{"", "Pull request could not be marked ready for review (HTTP 422)"} {
		calls := filepath.Join(t.TempDir(), "calls")
		answer := `echo '{"data": {"markPullRequestReadyForReview": {"pullRequest": {"number": 7, "isDraft": false}}}}'`
		if refused != "" {
			answer = `echo 'gh: ` + refused + `' >&2; exit 1`
		}
		// gh writes down each call on a line of its own, with what it read on
		// stdin, and answers for pull request #7 of acme/widgets, whose node ID
		// is PR_kwDOAbc7, over REST and GraphQL alike.
		gh := fakeGH(t, `input=
case "$*" in *--input*) input=$(cat) ;; esac
printf '%s %s' "$*" "$input" | tr '\n' ' ' >> '`+calls+`'
echo >> '`+calls+`'
case "$* $input" in
*markPullRequestReadyForReview*) `+answer+` ;;
*graphql*) echo '{"data": {"repository": {"pullRequest": {"id": "PR_kwDOAbc7", "number": 7, "isDraft": true}}}}' ;;
*repos/acme/widgets/pulls/7*) echo '{"number": 7, "node_id": "PR_kwDOAbc7", "draft": true, "state": "open", "html_url": "https://github.com/acme/widgets/pull/7"}' ;;
*) echo 'gh: Not Found (HTTP 404)' >&2; exit 1 ;;
esac
`)
		err := Client{Repo: "acme/widgets", GH: gh}.MarkReady(context.Background(), 7)
		b, _ := os.ReadFile(calls)
		marked := false
		for _, call := range strings.Split(string(b), "\n") {
			if strings.Contains(call, "markPullRequestReadyForReview") && strings.Contains(call, "PR_kwDOAbc7") {
				marked = true
			}
		}
		if !marked {
			t.Errorf("MarkReady(7) never ran GraphQL's markPullRequestReadyForReview on PR_kwDOAbc7, #7's node ID. gh ran with:\n%s", b)
		}
		switch {
		case refused == "" && err != nil:
			t.Errorf("MarkReady(7) = %v; want no error", err)
		case refused != "" && (err == nil || !strings.Contains(err.Error(), refused)):
			t.Errorf("GitHub refused to mark #7 ready, but MarkReady(7) = %v; want an error that says %q", err, refused)
		}
	}
}
