package formalize

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

// cacheDrafter stands in for a plan's drafting agent. Its run fills the Go
// build cache its check tool is given, part of it read-only as Go can
// leave it, as the tool's sandbox runs do, then leaves draft in its
// workspace, and fails with err.
type cacheDrafter struct {
	t      *testing.T
	draft  map[string]string
	err    error
	filled string
}

func (*cacheDrafter) Name() string { return "scripted" }

func (a *cacheDrafter) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	for i, arg := range job.GateServer {
		if arg != "-cache" || i+1 == len(job.GateServer) {
			continue
		}
		a.filled = job.GateServer[i+1]
		ro := filepath.Join(a.filled, "ro")
		if err := os.MkdirAll(ro, 0o755); err != nil {
			return synth.Usage{}, err
		}
		if err := os.WriteFile(filepath.Join(ro, "entry-d"), []byte("compiled"), 0o444); err != nil {
			return synth.Usage{}, err
		}
		a.t.Cleanup(func() { os.Chmod(ro, 0o755) })
		if err := os.Chmod(ro, 0o555); err != nil {
			return synth.Usage{}, err
		}
	}
	for name, text := range a.draft {
		if err := os.WriteFile(filepath.Join(job.Workspace, name), []byte(text), 0o644); err != nil {
			return synth.Usage{}, err
		}
	}
	return synth.Usage{CostUSD: 0.20}, a.err
}

// A plan's draft removes its check tool's Go build cache when it ends,
// whatever it left: a plan of issues, forks to ask, nothing usable, or a
// run its provider never let finish. Its records stay (#366).
func TestAPlanDraftRemovesItsCacheWhateverItLeft(t *testing.T) {
	forks := `{"forks": [{"id": "F1", "question": "Does the graph show superseded decisions?", "options": [{"id": "A", "says": "Yes, greyed out."}, {"id": "B", "says": "No, only the ones in force."}]}]}`
	for _, tc := range []struct {
		name  string
		draft map[string]string
		err   error
	}{
		{name: "a plan of issues", draft: map[string]string{"issues.json": issuesDraft}},
		{name: "forks", draft: map[string]string{"proposal.json": forks}},
		{name: "nothing usable"},
		{name: "a failed run", err: errors.New("the agent's run failed")},
		{name: "an unavailable provider", err: fmt.Errorf("Selected model is at capacity: %w", synth.ErrProviderUnavailable)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := &cacheDrafter{t: t, draft: tc.draft, err: tc.err}
			f := Formalizer{Backend: agent, Binary: "invariant", CheckRuns: 2, Timeout: time.Minute}
			out := t.TempDir()
			if _, err := f.Formalize(context.Background(), wholeIssueRequest(t), out); err != nil {
				t.Fatal(err)
			}
			if agent.filled != filepath.Join(out, "gocache") {
				t.Fatalf("the check tool was given the cache %q, not the draft's", agent.filled)
			}
			if _, err := os.Lstat(agent.filled); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the draft left its cache behind: %v", err)
			}
			for _, name := range []string{"formalization.json", "transcript.jsonl"} {
				if _, err := os.Stat(filepath.Join(out, name)); err != nil {
					t.Errorf("the draft lost its record %s: %v", name, err)
				}
			}
		})
	}
}
