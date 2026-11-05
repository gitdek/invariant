package github

import (
	"errors"
	"fmt"
	"testing"
)

func TestGitRunPush545FailureClassification(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    bool
	}{
		{"fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 500", true},
		{"error: RPC failed; HTTP 502 curl 22 The requested URL returned error: 502", true},
		{"fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 503", true},
		{"fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 504", true},
		{"remote: fatal error in commit_refs_rules\n ! [remote rejected] HEAD -> refs/invariant/runs/545/draft-push (failure)", true},
		{"fatal: unable to access 'https://github.com/o/r/': Operation timed out after 30000 milliseconds with 0 bytes received", true},
		{"error: RPC failed; curl 28 Operation too slow. Less than 1 bytes/sec transferred the last 30 seconds", true},
		{"fatal: the remote end hung up unexpectedly", true},
		{"error: RPC failed; curl 56 Recv failure: Connection reset by peer", true},
		{"error: RPC failed; curl 52 Empty reply from server", true},
		{"error: RPC failed; curl 18 transfer closed with outstanding read data remaining", true},
		{"fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 401\nremote: internal server error", false},
		{"fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 403\nremote: fatal error in commit_refs_rules", false},
		{"fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 404\nerror: connection reset by peer", false},
		{"fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 422\nerror: operation timed out", false},
		{"gh: Validation Failed (HTTP 422): fatal error in commit_refs_rules", false},
		{"fatal: Authentication failed for 'https://github.com/o/r/'", false},
		{"remote: Permission to o/r denied to watcher", false},
		{" ! [rejected] HEAD -> refs/invariant/runs/545/draft-push (stale info)", false},
		{" ! [rejected] HEAD -> main (non-fast-forward)", false},
		{"remote: GH013: Repository rule violations found\n ! [remote rejected] HEAD -> main (push declined due to repository rule violations)", false},
		{"fatal: unable to access 'https://github.com/o/r/': SSL certificate problem: certificate has expired", false},
	} {
		err := fmt.Errorf("git push: %w: %s", errors.New("exit status 128"), tc.message)
		if got := Transient(err); got != tc.want {
			t.Errorf("Transient(%q) = %v; want %v", tc.message, got, tc.want)
		}
	}
	for _, refusal := range []error{ErrNotFound, ErrNoPermission} {
		if Transient(fmt.Errorf("%w: fatal error in commit_refs_rules (HTTP 500)", refusal)) {
			t.Errorf("%v must remain a refusal", refusal)
		}
	}
}
