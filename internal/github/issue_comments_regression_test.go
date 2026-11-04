package github

import (
	"context"
	"fmt"
	"testing"
)

func TestIssueReadsPreserveCommentCount(t *testing.T) {
	ctx := context.Background()
	for _, key := range []string{"issue", "open", "closed"} {
		t.Run(key, func(t *testing.T) {
			g := newConditionalGH514(t)
			c := g.client()
			for _, count := range []int{2, 2, 3, 3} {
				body := fmt.Sprintf(`{"number":7,"comments":%d,"updated_at":"2026-11-03T12:00:00Z"}`, count)
				if key != "issue" {
					body = "[" + body + "]"
				}
				g.representation(t, key, fmt.Sprintf(`"comments-%d"`, count), body)
				var issues []Issue
				var err error
				switch key {
				case "issue":
					var issue Issue
					issue, err = c.Issue(ctx, 7)
					issues = []Issue{issue}
				case "open":
					issues, err = c.OpenIssues(ctx)
				case "closed":
					issues, err = c.ClosedIssues(ctx, "")
				}
				if err != nil || len(issues) != 1 || issues[0].Comments != count {
					t.Fatalf("want comment count %d with unchanged timestamp, got %+v, %v", count, issues, err)
				}
			}
		})
	}
}
