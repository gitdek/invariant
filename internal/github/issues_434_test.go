package github

import (
	"context"
	"testing"
)

// Closed-issue reconciliation needs every page and both states. GitHub's
// issues endpoint also returns pull requests, which must stay out of it.
func TestIssues434ListsBothStatesAndEscapesLabels(t *testing.T) {
	for _, tc := range []struct {
		name, label, query string
	}{
		{"all issues", "", "issues?state=all&per_page=100"},
		{"one label", "invariant:human review&needed", "issues?state=all&per_page=100&labels=invariant%3Ahuman+review%26needed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh := fakeGH(t, `case "$*" in
"api --paginate -H Accept: application/vnd.github+json repos/o/r/`+tc.query+`")
  cat <<'JSON'
[{"number":1,"state":"open"},{"number":2,"state":"closed","pull_request":{"url":"https://api.github.com/repos/o/r/pulls/2"}}]
[{"number":3,"state":"closed"},{"number":4,"state":"open","pull_request":{"url":"https://api.github.com/repos/o/r/pulls/4"}}]
JSON
  ;;
*) printf 'unexpected arguments: %s\n' "$*" >&2; exit 1 ;;
esac
`)
			got, err := (Client{Repo: "o/r", GH: gh}).Issues(context.Background(), tc.label)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || got[0].Number != 1 || got[0].State != "open" || got[1].Number != 3 || got[1].State != "closed" {
				t.Fatalf("issues across both pages = %+v; want open #1 and closed #3 without pull requests", got)
			}
		})
	}
}
