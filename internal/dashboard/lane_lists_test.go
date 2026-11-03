package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// A closed issue with one event, its opening, has no span, and its lane
// encoded "spans": null. The page reads every issue's spans to size the
// lanes, so "Show more" threw on #116 and showed nothing (#430).
func TestALaneEncodesItsListsEvenWhenEmpty(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	issue := github.Issue{Number: 116, Title: "Make the plumbing sandbox fast enough to iterate in", State: "closed",
		CreatedAt: now.Add(-48 * time.Hour).Format(time.RFC3339), ClosedAt: now.Add(-47 * time.Hour).Format(time.RFC3339)}
	l := Lane(issue, nil, "", now)
	if len(l.Events) != 1 || len(l.Spans) != 0 {
		t.Fatalf("events %+v, spans %+v; want only the opening and no span", l.Events, l.Spans)
	}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"spans":[]`, `"events":[{`} {
		if !strings.Contains(string(b), field) {
			t.Errorf("the lane doesn't encode %s: %s", field, b)
		}
	}
}
