package github

import (
	"errors"
	"fmt"
	"testing"
)

// A request GitHub failed for a moment can be made again, and one it
// refused can't, so the factory tries the first again next poll and says
// the second on the issue (#369's review).
func TestATransientFailureIsntARefusal(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{errors.New("POST graphql: exit status 1: gh: HTTP 502: Bad Gateway (https://api.github.com/graphql)"), true},
		{errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Server Error (HTTP 503)"), true},
		{errors.New(`Post "https://api.github.com/graphql": read tcp: connection reset by peer`), true},
		{errors.New("POST graphql: exit status 1: gh: Pull request could not be marked ready for review (HTTP 422)"), false},
		{errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Pull Request is not mergeable (HTTP 405)"), false},
		{fmt.Errorf("GET repos/o/r/pulls/7: %w", ErrNotFound), false},
		{fmt.Errorf("%w: HTTP 503", ErrNoPermission), false},
		{nil, false},
	} {
		if got := Transient(c.err); got != c.want {
			t.Errorf("Transient(%v) = %v; want %v", c.err, got, c.want)
		}
	}
}
