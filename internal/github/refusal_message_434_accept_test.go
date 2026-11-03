package github

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Refusals must keep GitHub's explanation for the factory's issue post,
// including a 404 which callers may also recognize through ErrNotFound.
func TestPermanentRefusalsKeepTheirMessage434(t *testing.T) {
	for _, tc := range []struct {
		message  string
		sentinel error
	}{
		{"gh: Repository unavailable to this installation (HTTP 404)", ErrNotFound},
		{"gh: Resource not accessible by integration (HTTP 403)", ErrNoPermission},
		{"gh: Validation Failed: invalid head, despite timeout wording (HTTP 422)", nil},
	} {
		t.Run(tc.message, func(t *testing.T) {
			gh := fakeGH(t, "cat >/dev/null\nprintf '%s\\n' '"+tc.message+"' >&2\nexit 1\n")
			_, err := (Client{Repo: "o/r", GH: gh}).CreatePullRequest(context.Background(), NewPullRequest{Title: "Change", Head: "work", Base: "main"})
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("the factory cannot quote GitHub's refusal: got %v, want %q", err, tc.message)
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Fatalf("preserving the message lost the existing error identity: %v", err)
			}
			if Transient(err) {
				t.Fatalf("a permanent refusal was classified as transient: %v", err)
			}
		})
	}
}
