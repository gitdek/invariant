package factory

import (
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

// The factory's issues are the ones with its labels, or that open with
// /invariant solve, however they were asked.
func TestTakes(t *testing.T) {
	for _, c := range []struct {
		is   github.Issue
		want bool
	}{
		{github.Issue{Labels: []github.Label{{Name: LabelTrigger}}}, true},
		{github.Issue{Labels: []github.Label{{Name: LabelMerged}}}, true},
		{github.Issue{Body: "Add the failure steps.\n\n/invariant solve\n"}, true},
		{github.Issue{Body: "> /invariant solve\nquoted, so not a command", Labels: []github.Label{{Name: "bug"}}}, false},
		{github.Issue{Body: "Nothing for the factory."}, false},
	} {
		if got := Takes(c.is); got != c.want {
			t.Errorf("%+v: %v, want %v", c.is, got, c.want)
		}
	}
}
