package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A request GitHub refuses the App is ErrNoPermission, which waiting won't
// fix. A rate limit is also a 403, but it isn't, since waiting does fix it
// (copythis-ad#38).
func TestNoPermissionIsItsOwnError(t *testing.T) {
	for _, c := range []struct {
		stderr string
		want   error
	}{
		{"gh: Resource not accessible by integration (HTTP 403)", ErrNoPermission},
		{"gh: Not Found (HTTP 404)", ErrNotFound},
		{"gh: API rate limit exceeded for installation ID 1. (HTTP 403)", nil},
	} {
		gh := filepath.Join(t.TempDir(), "gh")
		if err := os.WriteFile(gh, []byte("#!/bin/sh\necho '"+c.stderr+"' >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Client{Repo: "o/r", GH: gh}.Job(context.Background(), 1)
		switch {
		case err == nil:
			t.Errorf("%s: no error", c.stderr)
		case c.want != nil && !errors.Is(err, c.want):
			t.Errorf("%s: %v; want %v", c.stderr, err, c.want)
		case c.want == nil && (errors.Is(err, ErrNoPermission) || errors.Is(err, ErrNotFound)):
			t.Errorf("%s: %v; a rate limit is neither", c.stderr, err)
		}
	}
}
