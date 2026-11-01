package github

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// A request GitHub failed for a moment can be made again, and one it
// refused can't, so the factory tries the first again next poll and says
// the second on the issue (#369's review). Since #376, a client's timeout,
// a name that didn't resolve, a rate limit, an HTTP/2 stream's internal
// error, a TLS alert and gh killed by a signal count as GitHub failing for a
// moment too. The status gh's error carries decides first, so a 422, a 405,
// a 409 or any other 4xx but a 408, a 429 and a rate limit's 403 stays a
// refusal, whatever else its message says, as do not found and no
// permission.
func TestATransientFailureIsntARefusal(t *testing.T) {
	for _, c := range []struct {
		kind      string
		err       error
		transient bool
		limited   bool // GitHub's primary rate limit
	}{
		{"a 5xx", errors.New("POST graphql: exit status 1: gh: HTTP 502: Bad Gateway (https://api.github.com/graphql)"), true, false},
		{"a 5xx", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Server Error (HTTP 503)"), true, false},
		{"a 5xx", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: HTTP 504"), true, false},
		{"a dropped connection", errors.New(`Post "https://api.github.com/graphql": read tcp: connection reset by peer`), true, false},
		// gh's own words for a connection closed before any answer.
		{"a dropped connection", errors.New(`POST graphql: exit status 1: Post "https://api.github.com/graphql": EOF`), true, false},
		{"a dropped connection", errors.New(`PUT repos/o/r/pulls/7/merge: exit status 1: Put "https://api.github.com/repos/o/r/pulls/7/merge": write tcp 192.0.2.7:52341->140.82.112.6:443: write: broken pipe`), true, false},
		{"a dropped connection", errors.New(`POST graphql: exit status 1: Post "https://api.github.com/graphql": http2: client connection lost`), true, false},
		{"a dropped connection", errors.New(`PUT repos/o/r/pulls/7/merge: exit status 1: Put "https://api.github.com/repos/o/r/pulls/7/merge": dial tcp 140.82.112.6:443: connect: network is unreachable`), true, false},
		{"a client's timeout", errors.New(`PUT repos/o/r/pulls/7/merge: exit status 1: Put "https://api.github.com/repos/o/r/pulls/7/merge": net/http: request canceled (Client.Timeout exceeded while awaiting headers)`), true, false},
		{"a client's timeout", errors.New(`POST graphql: exit status 1: Post "https://api.github.com/graphql": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`), true, false},
		{"a client's timeout", errors.New(`POST graphql: exit status 1: Post "https://api.github.com/graphql": net/http: timeout awaiting response headers`), true, false},
		// What gh says when its dial times out, as it did against an address
		// that never answers.
		{"a client's timeout", errors.New(`GET repos/o/r/pulls/7: exit status 1: Get "https://api.github.com/repos/o/r/pulls/7": dial tcp 140.82.112.6:443: i/o timeout`), true, false},
		{"a client's timeout", errors.New(`GET repos/o/r/pulls/7: exit status 1: Get "https://api.github.com/repos/o/r/pulls/7": net/http: TLS handshake timeout`), true, false},
		{"a client's timeout", errors.New(`PUT repos/o/r/pulls/7/merge: exit status 1: dial tcp 140.82.112.6:443: connect: operation timed out`), true, false},
		{"a client's timeout", errors.New(`PUT repos/o/r/pulls/7/merge: exit status 1: dial tcp 140.82.112.6:443: connect: connection timed out`), true, false},
		{"a client's timeout", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Request Timeout (HTTP 408)"), true, false},
		// GitHub's GraphQL timing out on its side answers with this.
		{"GitHub's own timeout", errors.New("POST graphql: exit status 1: gh: Something went wrong while executing your query. This may be the result of a timeout, or it could be a GitHub bug. Please include `ABCD:1234:5678` when reporting this issue."), true, false},
		{"GitHub's own timeout", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: We couldn't respond to your request in time. Sorry about that. Please try resubmitting your request and contact us if the problem persists."), true, false},
		// gh says only that it couldn't connect when a name doesn't resolve,
		// as it did for a host that doesn't exist.
		{"a name that didn't resolve", errors.New("POST graphql: exit status 1: error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com"), true, false},
		{"a name that didn't resolve", errors.New(`PUT repos/o/r/pulls/7/merge: exit status 1: dial tcp: lookup api.github.com: no such host`), true, false},
		{"a name that didn't resolve", errors.New(`PUT repos/o/r/pulls/7/merge: exit status 1: dial tcp: lookup api.github.com on 192.0.2.1:53: server misbehaving`), true, false},
		{"a name that didn't resolve", errors.New(`PUT repos/o/r/pulls/7/merge: exit status 1: dial tcp: lookup api.github.com: Temporary failure in name resolution`), true, false},
		{"a secondary rate limit", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: You have exceeded a secondary rate limit. Please wait a few minutes before you try again. If you reach out to GitHub Support for help, please include the request ID ABCD:1234. (HTTP 403)"), true, false},
		{"a secondary rate limit", errors.New("POST graphql: exit status 1: gh: You have triggered an abuse detection mechanism. Please wait a few minutes before you try again. (HTTP 403)"), true, false},
		{"a secondary rate limit", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Too Many Requests (HTTP 429)"), true, false},
		{"a primary rate limit", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: API rate limit exceeded for installation ID 1. (HTTP 403)"), true, true},
		{"a primary rate limit", errors.New("POST graphql: exit status 1: gh: API rate limit already exceeded for user ID 7275925."), true, true},
		{"a primary rate limit", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: API rate limit exceeded for user ID 7275925. (HTTP 429)"), true, true},
		{"an HTTP/2 stream's internal error", errors.New(`POST graphql: exit status 1: Post "https://api.github.com/graphql": stream error: stream ID 3; INTERNAL_ERROR; received from peer`), true, false},
		{"an HTTP/2 stream's internal error", errors.New(`PUT repos/o/r/pulls/7/merge: exit status 1: Put "https://api.github.com/repos/o/r/pulls/7/merge": stream error: stream ID 1; INTERNAL_ERROR`), true, false},
		{"a TLS alert", errors.New(`POST graphql: exit status 1: Post "https://api.github.com/graphql": remote error: tls: internal error`), true, false},
		{"a TLS alert", errors.New(`PUT repos/o/r/pulls/7/merge: exit status 1: Put "https://api.github.com/repos/o/r/pulls/7/merge": local error: tls: bad record MAC`), true, false},
		// A certificate that doesn't verify is no alert, and no moment's failure.
		{"a refusal", errors.New(`PUT repos/o/r/pulls/7/merge: exit status 1: Put "https://api.github.com/repos/o/r/pulls/7/merge": tls: failed to verify certificate: x509: certificate signed by unknown authority`), false, false},
		{"gh killed by a signal", errors.New("PUT repos/o/r/pulls/7/merge: signal: killed: "), true, false},
		{"gh killed by a signal", errors.New("POST graphql: signal: terminated: "), true, false},
		// The status decides before the words: GitHub's refusal stays one,
		// whatever its message says, and a rate limit's 403 is no refusal.
		{"a refusal's status", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Pull Request is not mergeable: a required check ended with an i/o timeout (HTTP 405)"), false, false},
		{"a refusal's status", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Head branch was modified after a connection reset. Review and try the merge again. (HTTP 409)"), false, false},
		{"a refusal's status", errors.New("POST graphql: exit status 1: gh: Validation Failed: the status says bad gateway, service unavailable (HTTP 422)"), false, false},
		{"a refusal's status", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: HTTP 422: Unprocessable Entity (https://api.github.com/repos/o/r/pulls/7/merge): EOF"), false, false},
		{"a refusal's status", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Must have admin rights to Repository, though a gateway timeout came first (HTTP 403)"), false, false},
		{"a refusal's status", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Bad credentials (HTTP 401)"), false, false},
		{"a refusal's status", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Too many open pull requests (HTTP 422)"), false, false},
		{"a refusal's status", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: API rate limit exceeded is no excuse to skip review (HTTP 422)"), false, false},
		{"a refusal", errors.New("POST graphql: exit status 1: gh: Pull request could not be marked ready for review (HTTP 422)"), false, false},
		{"a refusal", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Pull Request is not mergeable (HTTP 405)"), false, false},
		{"a refusal", errors.New("PUT repos/o/r/pulls/7/merge: exit status 1: gh: Head branch was modified. Review and try the merge again. (HTTP 409)"), false, false},
		{"a refusal", errors.New("POST graphql: exit status 1: gh: Could not resolve to a PullRequest with the global id of 'PR_kwDOAbc7'."), false, false},
		{"a refusal", fmt.Errorf("GET repos/o/r/pulls/7: %w", ErrNotFound), false, false},
		{"a refusal", fmt.Errorf("%w: HTTP 503", ErrNoPermission), false, false},
		{"a refusal", fmt.Errorf("%w: API rate limit exceeded, i/o timeout", ErrNoPermission), false, false},
		{"no error", nil, false, false},
	} {
		if got := Transient(c.err); got != c.transient {
			t.Errorf("%s: Transient(%v) = %v; want %v", c.kind, c.err, got, c.transient)
		}
		if got := RateLimited(c.err); got != c.limited {
			t.Errorf("%s: RateLimited(%v) = %v; want %v", c.kind, c.err, got, c.limited)
		}
	}
}

// What gh writes on its standard error decides, through the client's own
// errors, as gh writes it: a name that didn't resolve over two lines, and a
// connection closed before any answer, both GitHub failing for a moment,
// beside a 405 GitHub answered and a 404, which stay refusals (#376).
func TestGhsOwnWordsAreClassified(t *testing.T) {
	for _, c := range []struct {
		stderr    string
		transient bool
	}{
		{`error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com`, true},
		{`Put "https://api.github.com/repos/o/r/pulls/7/merge": EOF`, true},
		{`gh: You have exceeded a secondary rate limit. Please wait a few minutes before you try again. (HTTP 403)`, true},
		{`Post "https://api.github.com/graphql": stream error: stream ID 3; INTERNAL_ERROR; received from peer`, true},
		{`Post "https://api.github.com/graphql": remote error: tls: internal error`, true},
		{`gh: Pull Request is not mergeable (HTTP 405)`, false},
		{`gh: Pull Request is not mergeable: i/o timeout (HTTP 405)`, false},
		{`gh: Not Found (HTTP 404)`, false},
		{`gh: Resource not accessible by integration (HTTP 403)`, false},
	} {
		gh := fakeGH(t, `printf '`+c.stderr+`\n' >&2
exit 1
`)
		_, err := Client{Repo: "o/r", GH: gh}.Merge(context.Background(), 7, "abc123", "merge")
		if err == nil || Transient(err) != c.transient {
			t.Errorf("gh said %q, and Merge = %v, transient %v; want transient %v", c.stderr, err, Transient(err), c.transient)
		}
	}
}

// A gh killed by a signal, while the watcher's own context lasts, never
// heard GitHub's answer, so the request may well succeed if made again
// (#376's review): the client's error says how gh ended, and it's
// transient.
func TestAKilledGhIsntARefusal(t *testing.T) {
	for _, sig := range []string{"KILL", "TERM"} {
		gh := fakeGH(t, "kill -"+sig+" $$\n")
		_, err := Client{Repo: "o/r", GH: gh}.Merge(context.Background(), 7, "abc123", "merge")
		if err == nil || !Transient(err) || RateLimited(err) {
			t.Errorf("gh killed by SIG%s: Merge = %v, transient %v; want a transient error", sig, err, Transient(err))
		}
	}
}
