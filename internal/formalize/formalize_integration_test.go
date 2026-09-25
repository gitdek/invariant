//go:build integration

package formalize

import (
	"context"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// The factory checks a draft with the gate's model checks before asking
// anyone to ratify it.
func TestCheckRunsTheModelChecks(t *testing.T) {
	ctx := context.Background()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := Check(ctx, workspace(t, nil), tc)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed || !r.ModelOnly || r.Design.DistinctStates != 7 || !r.Witnesses[0].Reached || !r.Bugs[0].Caught {
		t.Fatalf("the draft should check out: %s", verify.Feedback(r))
	}

	// A model that lets a producer overfill the buffer breaks WithinCap.
	_, r, err = Check(ctx, workspace(t, map[string]func(string) string{"BoundedBuffer.tla": replace("Put(m) == Len(buf) < Cap /\\ ", "Put(m) == ")}), tc)
	if err != nil {
		t.Fatal(err)
	}
	if r.Passed || !strings.Contains(verify.Feedback(r), "Invariant WithinCap is violated") {
		t.Fatalf("an overfilling model must fail: %s", verify.Feedback(r))
	}
}
