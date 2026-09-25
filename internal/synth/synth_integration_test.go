//go:build integration

package synth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// scripted stands in for a coding agent. It writes the hand-built model and
// code into the workspace, and also tries to weaken the lock.
type scripted struct{}

func (scripted) Name() string { return "scripted" }

func (scripted) Run(ctx context.Context, job Job) (Usage, error) {
	for _, rel := range []string{".invariant/specs/TwoPhase.tla", "twophase/twophase.go", "twophase/explore.go", "twophase/twophase_test.go"} {
		if err := copyFile(filepath.Join(example, rel), filepath.Join(job.Workspace, rel)); err != nil {
			return Usage{}, err
		}
	}
	err := os.WriteFile(filepath.Join(job.Workspace, ".invariant", "ratified.lock"), []byte(`{"decision":"none","statements":[]}`), 0o644)
	return Usage{Model: "none", Outcome: "success"}, err
}

// The pipeline end to end, without a model: skeleton, workspace, assembly
// from the original lock, the final gate, and the logs.
func TestSynthesizeWithAScriptedAgent(t *testing.T) {
	ctx := context.Background()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	r, err := Synthesize(ctx, Options{
		Project: example, Out: out, Backend: scripted{}, Binary: "unused", GateRuns: 4,
		Timeout: 10 * time.Minute, Toolchain: tc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Final.Passed {
		t.Fatalf("the final gate failed on the hand-built answer")
	}
	if len(r.Tampered) != 1 || r.Tampered[0] != ".invariant/ratified.lock" {
		t.Errorf("tampered = %v; want the lock reported", r.Tampered)
	}
	for _, f := range []string{"synthesis.json", "transcript.jsonl", "result/.invariant/ratified.lock", "result/twophase/twophase.go"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
}
