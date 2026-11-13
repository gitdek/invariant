//go:build integration

package formalize

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// With the pinned TLC, a draft's own check prepares a current bug's replay
// path when its project's code is checked by a driver, and refuses the
// draft while TLC can't say which action each step is (#910). The fixture's
// bug chooses its message inside its own body, so its path is refused until
// the choice is a parameter of an operation the bug names.
func TestADraftsCheckPreparesACurrentBugsReplay910(t *testing.T) {
	ctx := context.Background()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current := replace(`"code_behavior": "hypothetical"`, `"code_behavior": "current"`)
	// The bug as an operation it names. Unguarded, the operation takes the
	// steps Put takes too. Guarded, it takes only the step that overfills.
	bug := "PutWhenFull == \\E m \\in Msgs : buf' = Append(buf, m)"
	unguarded := replace(bug, "PutAnyway(m) == buf' = Append(buf, m)\n\nPutWhenFull == \\E m \\in Msgs : PutAnyway(m)")
	named := replace(bug, "PutAnyway(m) == Len(buf) >= Cap /\\ buf' = Append(buf, m)\n\nPutWhenFull == \\E m \\in Msgs : PutAnyway(m)")
	check := func(language string, edits map[string]func(string) string) (*verify.Report, error) {
		t.Helper()
		_, r, err := CheckWithContext(ctx, workspace(t, edits), tc, CheckContext{Language: language})
		return r, err
	}

	// Go keeps its own checks: nothing is prepared and nothing is refused.
	r, err := check("go", map[string]func(string) string{"proposal.json": current})
	if err != nil || !r.Passed || r.Bugs[0].Replay == nil || r.Bugs[0].Replay.Required {
		t.Fatalf("a current bug in Go: %v, %+v", err, r.Bugs[0].Replay)
	}
	// A hypothetical bug is never replayed, whatever the language.
	r, err = check("typescript", nil)
	if err != nil || !r.Passed || r.Bugs[0].Replay != nil {
		t.Fatalf("a hypothetical bug in TypeScript: %v, %+v", err, r.Bugs[0].Replay)
	}
	// A current bug that hides its choice is refused, with TLC's reason.
	for _, language := range []string{"typescript", "python"} {
		r, err = check(language, map[string]func(string) string{"proposal.json": current})
		if err == nil || !strings.Contains(err.Error(), "bug PutWhenFull is current") || !strings.Contains(err.Error(), "replay-unsupported-action") {
			t.Fatalf("a current bug in %s that hides its choice: %v", language, err)
		}
		if r == nil || !r.Passed || r.Bugs[0].Replay == nil || !r.Bugs[0].Replay.Required || r.Bugs[0].Replay.Request != nil {
			t.Fatalf("the refused draft's receipt in %s: %+v", language, r)
		}
	}
	// An operation that's enabled where Put is gives the first step two
	// actions, and TLC can't say which one took it: refused, by the step.
	_, err = check("typescript", map[string]func(string) string{"proposal.json": current, "BoundedBuffer.tla": unguarded})
	if err == nil || !strings.Contains(err.Error(), "replay-ambiguous-edge: step 1 of the counterexample, from its state 1 to its state 2, can be taken 2 ways, ") ||
		!strings.Contains(err.Error(), "Put(m1)") || !strings.Contains(err.Error(), "PutAnyway(m1)") || !strings.Contains(err.Error(), "guard the bug's operation") {
		t.Fatalf("a current bug whose operation takes a step Put takes too: %v", err)
	}
	// Named as a parameter and guarded, the choice is one TLC attributes:
	// the path is prepared, through the operation the bug names, with its
	// argument.
	r, err = check("typescript", map[string]func(string) string{"proposal.json": current, "BoundedBuffer.tla": named})
	if err != nil || !r.Passed {
		t.Fatalf("a current bug that names its operation: %v\n%s", err, verify.Feedback(r))
	}
	e := r.Bugs[0].Replay
	if e == nil || !e.Required || e.Status != "pending" || e.Request == nil {
		t.Fatalf("its replay isn't prepared: %+v", e)
	}
	path := e.Request.Path
	last := path.Steps[path.Offending]
	if !last.Bug || last.Action != "PutAnyway" || len(last.Args) != 1 || last.Args[0].Kind != "model" {
		t.Fatalf("the path's offending step is %+v; want PutAnyway with the message it put", last)
	}
	if label := r.Bugs[0].ReplayStatus().Label; label != "current; pending" {
		t.Errorf("the receipt reads %q", label)
	}
}

// An option's preview is held to the same rule as a draft: of two options,
// the one whose current bug hides its choice is refused with TLC's reason,
// and the one that names and guards its operation is checked as before.
func TestAnOptionsPreviewPreparesACurrentBugsReplay910(t *testing.T) {
	tc, err := toolchain.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current := replace(`"code_behavior": "hypothetical"`, `"code_behavior": "current"`)
	named := replace("PutWhenFull == \\E m \\in Msgs : buf' = Append(buf, m)", "PutAnyway(m) == Len(buf) >= Cap /\\ buf' = Append(buf, m)\n\nPutWhenFull == \\E m \\in Msgs : PutAnyway(m)")
	fork := Fork{ID: "F1", Question: "How is the overfilling put written?"}
	for id, edits := range map[string]map[string]func(string) string{
		"A": {"proposal.json": current},
		"B": {"proposal.json": current, "BoundedBuffer.tla": named},
	} {
		m, err := ReadAmending(workspace(t, edits), nil)
		if err != nil {
			t.Fatal(err)
		}
		m.Statements[1].Says = "Candidate " + id + " keeps its own capacity."
		fork.Options = append(fork.Options, Option{ID: id, Says: "Write it as option " + id + " does.", Preview: &Preview{Fork: fork.ID, Option: id, Model: m}})
	}
	if fork.Options[0].ID != "A" {
		fork.Options[0], fork.Options[1] = fork.Options[1], fork.Options[0]
	}
	ws := t.TempDir()
	b, err := json.Marshal(Draft{Forks: []Fork{fork}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "proposal.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	p, _, err := CheckWithContext(t.Context(), ws, tc, CheckContext{Language: "typescript"})
	if err == nil || !strings.Contains(err.Error(), "fork F1 option A: bug PutWhenFull is current") || !strings.Contains(err.Error(), "replay-unsupported-action") || strings.Contains(err.Error(), "option B") {
		t.Fatalf("a preview whose current bug hides its choice: %v", err)
	}
	a, good := p.Forks[0].Options[0].Preview, p.Forks[0].Options[1].Preview
	if a.Problem == "" || good.Problem != "" || good.Receipt == nil || good.Receipt.Bugs[0].Replay == nil || good.Receipt.Bugs[0].Replay.Request == nil {
		t.Fatalf("option A's problem is %q, option B's %q, and B's replay %+v", a.Problem, good.Problem, good.Receipt)
	}
	// In Go neither is replayed, and both stand.
	if _, _, err := CheckWithContext(t.Context(), ws, tc, CheckContext{Language: "go"}); err != nil {
		t.Fatalf("the same options in Go: %v", err)
	}
}
