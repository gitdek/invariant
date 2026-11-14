package synth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

type replayReviewer913 struct {
	t        *testing.T
	original string
}

func (replayReviewer913) Name() string { return "reviewer" }
func (r replayReviewer913) Run(_ context.Context, job Job) (Usage, error) {
	r.t.Helper()
	if !job.ReadOnly || job.Workspace == r.original {
		r.t.Fatal("review is not an isolated read-only job")
	}
	for _, language := range []string{"typescript"} {
		name, want := verify.ReplayHarness(language)
		got, err := os.ReadFile(filepath.Join(job.Workspace, name))
		if err != nil || string(got) != string(want) {
			r.t.Fatalf("review lacks fresh replay harness: %v", err)
		}
	}
	for name, want := range map[string]string{"existing/src/delivery.ts": "export class DeliveryStore {}\n", "driver.ts": "import {DeliveryStore} from '../../src/delivery.ts';\n"} {
		got, err := os.ReadFile(filepath.Join(job.Workspace, name))
		if err != nil || string(got) != want {
			r.t.Fatalf("review source %s: %s %v", name, got, err)
		}
	}
	for _, want := range []string{"invariantReplay", "exact replay prefix", "read-back", "existing/", "change nothing", "Claim nothing you didn't read"} {
		if !strings.Contains(job.Prompt, want) {
			r.t.Errorf("review omitted %q", want)
		}
	}
	return Usage{Outcome: "success", Summary: "Read the real bindings."}, nil
}

func TestReviewStagesCommandReplayAndSelectedCode913(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "verification", "delivery")
	if err := copyDir("../../examples/04-api-rate-limiter", dir); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	p.Manifest.Existing, p.Manifest.Conformance = []string{"src/delivery.ts"}, "driver.ts"
	for i := range p.Lock.Statements {
		if p.Lock.Statements[i].Kind == project.Bug {
			p.Lock.Statements[i].CodeBehavior = "current"
		}
	}
	for name, v := range map[string]any{".invariant/invariant.json": p.Manifest, ".invariant/ratified.lock": p.Lock} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{
		"package.json":                                   `{"name":"review","version":"1.0.0"}`,
		"package-lock.json":                              `{"lockfileVersion":3}`,
		"src/delivery.ts":                                "export class DeliveryStore {}\n",
		"verification/delivery/driver.ts":                "import {DeliveryStore} from '../../src/delivery.ts';\n",
		"verification/delivery/invariant-replay.ts":      "untrusted harness copy",
		"verification/delivery/existing/src/delivery.ts": "stale application copy",
	} {
		if err := writeFile(filepath.Join(root, name), content); err != nil {
			t.Fatal(err)
		}
	}
	_, err = ReviewDriver(context.Background(), replayReviewer913{t, dir}, dir, filepath.Join(t.TempDir(), "review.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "src/delivery.ts"))
	if err != nil || string(b) != "export class DeliveryStore {}\n" {
		t.Fatal("review changed original application")
	}
}
