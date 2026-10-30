package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

type providerFailedBuilder struct {
	*fakeBuilder
	err error
}

func (b providerFailedBuilder) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	r, err := b.fakeBuilder.Build(ctx, dir, out, amend)
	if err != nil {
		return r, err
	}
	return r, b.err
}

func TestProviderFailureIsPostedAfterSavingTheBuild(t *testing.T) {
	// A failed gate produces a saved result; publish reconstructs the error
	// from its text, so a post cannot rely on the original error's Go type.
	const last = "Selected model is at capacity. Please try a different model. (last attempt)"
	r := newRig(t)
	r.build.pass = false
	r.f.Builder = providerFailedBuilder{fakeBuilder: r.build,
		err: errors.New("codex provider unavailable after 4 attempts: " + last)}
	ratified(t, r)
	p := r.expect(1, KindFailed, LabelHumanReview)
	for _, want := range []string{"provider unavailable", last, "/invariant retry"} {
		if !strings.Contains(p.Comment.Body, want) {
			t.Errorf("the failure post lost %q:\n%s", want, p.Comment.Body)
		}
	}
	if len(r.build.built) != 1 {
		t.Errorf("factory repeated the whole build %d times instead of retrying only the agent run", len(r.build.built))
	}
}

func TestProviderExhaustionCannotPublishAPassingBuild(t *testing.T) {
	// Use a real backend error, including its classification, without a live
	// service. Its job deadline expires during the first provider wait.
	const last = "Selected model is at capacity. Please try a different model."
	bin := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"turn.failed\",\"error\":{\"message\":\"" + last + "\"}}'\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	backend := synth.Codex{Binary: bin, Home: t.TempDir()}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, runErr := backend.Run(ctx, synth.Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 300 * time.Millisecond})
	if runErr == nil {
		t.Fatal("the exhausted run unexpectedly succeeded")
	}
	r := newRig(t)
	r.f.Builder = providerFailedBuilder{fakeBuilder: r.build, err: fmt.Errorf("the agent's run failed: %w", runErr)}
	ratified(t, r)
	p := r.expect(1, KindFailed, LabelHumanReview)
	for _, want := range []string{"provider", "unavailable", last, "/invariant retry"} {
		if !strings.Contains(p.Comment.Body, want) {
			t.Errorf("the post lost %q:\n%s", want, p.Comment.Body)
		}
	}
	if len(r.gh.merged) != 0 {
		t.Fatal("the exhausted run was merged")
	}
}
