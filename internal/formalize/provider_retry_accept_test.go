package formalize

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

func TestEveryDraftReportsProviderExhaustionDespiteLeftoverFiles(t *testing.T) {
	const last = "Selected model is at capacity. Please try a different model."
	for _, kind := range []string{"statements", "plumbing plan", "plan of issues"} {
		t.Run(kind, func(t *testing.T) {
			// A usable proposal left before the terminal provider failure must
			// not be mistaken for completion after the retry budget expires.
			bin := filepath.Join(t.TempDir(), "codex")
			script := `#!/bin/sh
printf '%s\n' '{"unsupported":"A usable draft left by the interrupted attempt."}' > proposal.json
printf '%s\n' '{"type":"turn.failed","error":{"message":"Selected model is at capacity. Please try a different model."}}'
exit 1
`
			if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			req := Request{Repo: "gitdek/invariant", Issue: 261, Title: "Retry temporary provider failures", Body: "A fixture.", Author: "gitdek"}
			switch kind {
			case "plumbing plan":
				req.Plumbing = t.TempDir()
			case "plan of issues":
				req.PRD = t.TempDir()
			}
			f := Formalizer{Backend: synth.Codex{Binary: bin, Home: t.TempDir()}, Binary: "invariant", CheckRuns: 4, Timeout: 300 * time.Millisecond}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			r, err := f.Formalize(ctx, req, filepath.Join(t.TempDir(), "out"))
			if err != nil || r == nil {
				t.Fatalf("draft result = %+v, error = %v; want a recorded failed draft", r, err)
			}
			if r.Proposal != nil {
				t.Error("provider exhaustion proposed an interrupted draft")
			}
			for _, want := range []string{"provider", "unavailable", last} {
				if !strings.Contains(r.Problem, want) {
					t.Errorf("draft failure lost %q: %s", want, r.Problem)
				}
			}
		})
	}
}
