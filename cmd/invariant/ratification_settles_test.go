package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// The ratification check reads a ratifying comment for every lock and plan,
// on every change. When GitHub fails one read for a moment, as it did with a
// 504 on a change that touched no lock, the check asks again, twice at
// most. A refusal, and an answer that doesn't ratify, are final.
func TestTheRatificationCheckAsksAgainWhenGitHubFailsForAMoment(t *testing.T) {
	wait := ratificationWait
	ratificationWait = time.Millisecond
	defer func() { ratificationWait = wait }()
	late := fmt.Errorf("the ratifying comment can't be read: %w", errors.New(`GET repos/o/r/issues/comments/1: {"message": "We couldn't respond to your request in time."} (HTTP 504)`))
	if !github.Transient(late) {
		t.Fatal("a 504 isn't read as GitHub failing for a moment")
	}
	for _, c := range []struct {
		name    string
		answers []error
		calls   int
		fails   bool
	}{
		{"ratified at once", []error{nil}, 1, false},
		{"one 504, then ratified", []error{late, nil}, 2, false},
		{"two 504s, then ratified", []error{late, late, nil}, 3, false},
		{"three 504s", []error{late, late, late, nil}, 3, true},
		{"not ratified", []error{errors.New("the comment doesn't ratify proposal sha256:abc"), nil}, 1, true},
		{"a comment that's gone", []error{fmt.Errorf("the ratifying comment can't be read: %w", github.ErrNotFound), nil}, 1, true},
	} {
		calls := 0
		err := settled(t.Context(), func() error {
			calls++
			return c.answers[calls-1]
		})
		if calls != c.calls || (err != nil) != c.fails {
			t.Errorf("%s: asked %d times and ended with %v; want %d times, failing %v", c.name, calls, err, c.calls, c.fails)
		}
	}
	// A cancelled check stops waiting.
	ratificationWait = time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan error, 1)
	go func() { done <- settled(ctx, func() error { return late }) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a cancelled check reported a ratification")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled check kept waiting to ask again")
	}
}
