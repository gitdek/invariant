//go:build integration

package formalize

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
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

	// The caller names the project's language. What the draft says of it,
	// or of code to check as it is, counts for nothing: the factory gives a
	// draft both after the check, from the issue, so a revision copied from
	// an earlier proposal says what that one was given.
	says := func(field string) func(string) string {
		return func(s string) string {
			return strings.Replace(current(s), `"name": "bounded buffer",`, `"name": "bounded buffer", `+field+`,`, 1)
		}
	}
	for _, field := range []string{`"language": "go"`, `"existing": ["hooks/use-buffer.ts"], "language": "go"`} {
		if _, err = check("typescript", map[string]func(string) string{"proposal.json": says(field)}); err == nil || !strings.Contains(err.Error(), "replay-unsupported-action") {
			t.Errorf("a TypeScript project's draft that says %s, with a current bug that hides its choice: %v", field, err)
		}
	}
	for _, field := range []string{`"language": "typescript"`, `"existing": ["hooks/use-buffer.ts"]`} {
		r, err = check("go", map[string]func(string) string{"proposal.json": says(field)})
		if err != nil || r.Bugs[0].Replay == nil || r.Bugs[0].Replay.Required {
			t.Errorf("a Go project's draft that says %s is held to a replay Go never makes: %v, %+v", field, err, r.Bugs[0].Replay)
		}
	}

	// A specification the path's check can't read is the drafter's to fix,
	// in one line, and the draft is told which line: it isn't told to
	// rewrite a bug that's written as it should be.
	listed := func(s string) string {
		return strings.Replace(named(s), "Spec == Init /\\ [][Next]_vars", "Spec ==\n    /\\ Init\n    /\\ [][Next]_vars", 1)
	}
	r, err = check("typescript", map[string]func(string) string{"proposal.json": current, "BoundedBuffer.tla": listed})
	if err == nil || !strings.Contains(err.Error(), "specification is not Init /\\ [][Next]_vars. Write the specification as Spec == Init /\\ [][Next]_vars") || strings.Contains(err.Error(), "BugStep(x)") {
		t.Errorf("a current bug in a model whose specification is written as a list: %v", err)
	}
	if r == nil || !r.Passed || r.Bugs[0].Replay == nil || r.Bugs[0].Replay.Request != nil {
		t.Errorf("its receipt: %+v", r)
	}
	if _, err = check("go", map[string]func(string) string{"proposal.json": current, "BoundedBuffer.tla": listed}); err != nil {
		t.Errorf("the same model in Go: %v", err)
	}

	// One check says everything it finds. A draft with few checks left
	// learns of its view in the check that refuses its bug's path.
	unread := replace(`"when":true,"label":"Buffer"`, `"when":"Len(nothing) = 0","label":"Buffer"`)
	p, _, err := CheckWithContext(ctx, workspace(t, map[string]func(string) string{"proposal.json": current, "view.json": unread}), tc, CheckContext{Language: "typescript"})
	if err == nil || !strings.Contains(err.Error(), "bug PutWhenFull is current") || !strings.Contains(err.Error(), "replay-unsupported-action") || !strings.Contains(err.Error(), "invalid_view") {
		t.Errorf("a draft whose bug hides its choice, and whose view the reader can't take, is told:\n%v", err)
	}
	if p == nil || p.Reader != nil {
		t.Errorf("a draft whose view is refused keeps a reader: %+v", p)
	}
	if _, _, alone := CheckWithContext(ctx, workspace(t, map[string]func(string) string{"view.json": unread}), tc, CheckContext{Language: "typescript"}); alone == nil ||
		strings.Contains(alone.Error(), "is current") || !strings.Contains(err.Error(), alone.Error()) {
		t.Errorf("the same view alone is refused with %v, which the draft with both wasn't told in full", alone)
	}
}

// A current bug that breaks a property stands, though no path is prepared
// for it: TLC's counterexample never ends, so no rewrite of the model gives
// the gate finitely many steps to replay, and the bug's proposal says a
// build can't pass yet. The code the path's check gives it is the one a
// specification it can't read gets, which is refused: the two are told
// apart by what the bug's own run broke.
func TestACurrentBugThatBreaksAPropertyStands910(t *testing.T) {
	tc, err := toolchain.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	for name, text := range map[string]string{"Ticker.tla": ticker910, "proposal.json": tickerDraft910,
		"view.json": `{"version":1,"state":[{"id":"all","when":true,"label":"Ticker"}],"step":[{"id":"all","when":true,"label":"Tick"}]}`} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, language := range []string{"typescript", "python"} {
		p, r, err := CheckWithContext(t.Context(), ws, tc, CheckContext{Language: language})
		if err != nil || r == nil || !r.Passed || p.Reader == nil {
			t.Fatalf("a current bug that breaks a property, in %s: %v\n%s", language, err, verify.Feedback(r))
		}
		b := r.Bugs[0]
		if e := b.Replay; b.Outcome != "property violated" || e == nil || !e.Required || e.Request != nil || e.Status != "replay-unsupported-temporal" {
			t.Errorf("its receipt in %s: outcome %q, replay %+v", language, b.Outcome, e)
		}
	}
}

const ticker910 = `---- MODULE Ticker ----
EXTENDS Naturals
CONSTANTS Max
VARIABLES n, done

vars == <<n, done>>

TypeOK == n \in 0..Max /\ done \in BOOLEAN

CanFinish == done

Init == n = 0 /\ done = FALSE

Step == n < Max /\ n' = n + 1 /\ done' = done

Finish == n = Max /\ done' = TRUE /\ n' = n

Next == Step \/ Finish

FairStep == WF_vars(Step)

FairFinish == WF_vars(Finish)

EventuallyDone == <>done

\* A known bug: the counter starts over.
Reset == n > 0 /\ ~done /\ n' = 0 /\ done' = done

Spec == Init /\ [][Next]_vars
====
`

const tickerDraft910 = `{
  "name": "ticker", "slug": "ticker", "module": "Ticker", "package": "ticker",
  "bounds": {"Max": "1"},
  "statements": [
    {"name": "Spec", "kind": "spec", "says": "The system starts in Init, and every step is a Next step."},
    {"name": "TypeOK", "kind": "invariant", "says": "The counter is a number and done is a flag."},
    {"name": "CanFinish", "kind": "witness", "says": "It can finish."},
    {"name": "EventuallyDone", "kind": "property", "says": "It eventually finishes."},
    {"name": "FairStep", "kind": "fairness", "says": "A possible step eventually happens."},
    {"name": "FairFinish", "kind": "fairness", "says": "A possible finish eventually happens."},
    {"name": "Reset", "kind": "bug", "says": "The counter starts over.", "expect": "EventuallyDone", "code_behavior": "current"}
  ],
  "forks": []
}`

// The prompt's own example is a draft a drafter copies the shape of. With
// its bug called current, in a project a driver checks, its path is
// prepared: through the operation the bug names, with the process that
// took the step.
func TestThePromptsExampleCanBeReplayed910(t *testing.T) {
	tc, err := toolchain.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	text := Prompt(Request{Repo: "o/r", Issue: 5, Title: "t"}, 4)
	block := func(fence string) string {
		m := regexp.MustCompile("(?s)# An example\n.*?```" + fence + "\n(.*?)```").FindStringSubmatch(text)
		if m == nil {
			t.Fatalf("the prompt's example has no %s block", fence)
		}
		return m[1]
	}
	for behavior, language := range map[string]string{"hypothetical": "go", "current": "typescript"} {
		ws := t.TempDir()
		for name, body := range map[string]string{"Mutex.tla": block("tla"), "proposal.json": strings.Replace(block("json"), `"code_behavior": "hypothetical"`, `"code_behavior": "`+behavior+`"`, 1),
			"view.json": `{"version":1,"state":[{"id":"all","when":true,"label":"Printer"}],"step":[{"id":"all","when":true,"label":"Step"}]}`} {
			if err := os.WriteFile(filepath.Join(ws, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		_, r, err := CheckWithContext(t.Context(), ws, tc, CheckContext{Language: language})
		if err != nil || r == nil || !r.Passed {
			t.Fatalf("the prompt's example, with its bug %s, in %s: %v\n%s", behavior, language, err, verify.Feedback(r))
		}
		if behavior != "current" {
			continue
		}
		e := r.Bugs[0].Replay
		if e == nil || !e.Required || e.Request == nil {
			t.Fatalf("its replay isn't prepared: %+v", e)
		}
		path := e.Request.Path
		if last := path.Steps[path.Offending]; !last.Bug || last.Action != "EnterWhilePrinting" || len(last.Args) != 1 {
			t.Errorf("the path's offending step is %+v; want EnterWhilePrinting with the process that entered", last)
		}
	}
}

// draft910 is a drafting agent that writes the buffer fixture, edited.
type draft910 struct {
	t     *testing.T
	edits map[string]func(string) string
}

func (draft910) Name() string { return "scripted" }

func (a draft910) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	src := workspace(a.t, a.edits)
	for _, name := range []string{"BoundedBuffer.tla", "proposal.json", "view.json"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return synth.Usage{}, err
		}
		if err := os.WriteFile(filepath.Join(job.Workspace, name), b, 0o644); err != nil {
			return synth.Usage{}, err
		}
	}
	return synth.Usage{}, nil
}

// The factory builds a draft as its check staged it. Through the whole
// drafting step, a draft that names a language of its own, or code to check
// as it is, is checked and then built as the project its issue names:
// refused in TypeScript for the replay it would owe there, and left a Go
// project with code of its own where the issue names Go.
func TestTheFactoryBuildsADraftAsItsCheckStagedIt910(t *testing.T) {
	tc, err := toolchain.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current := replace(`"code_behavior": "hypothetical"`, `"code_behavior": "current"`)
	says := func(field string) map[string]func(string) string {
		return map[string]func(string) string{"proposal.json": func(s string) string {
			return strings.Replace(current(s), `"name": "bounded buffer",`, `"name": "bounded buffer", `+field+`,`, 1)
		}}
	}
	draft := func(language string, edits map[string]func(string) string) *Result {
		t.Helper()
		f := Formalizer{Backend: draft910{t, edits}, CheckRuns: 1, Timeout: 10 * time.Minute, Toolchain: tc}
		r, err := f.Formalize(t.Context(), Request{Repo: "o/r", Issue: 5, Title: "t", Author: "a", Language: language}, t.TempDir())
		if err != nil || r.Proposal == nil || r.CheckContext == nil || r.CheckContext.Language != language {
			t.Fatalf("drafting in %q: %v, %+v", language, err, r)
		}
		return r
	}
	r := draft("typescript", says(`"language": "go"`))
	if m := r.Proposal.Manifest(); m.Language != "typescript" || !strings.Contains(r.Problem, "bug PutWhenFull is current") || !strings.Contains(r.Problem, "replay-unsupported-action") {
		t.Errorf("a TypeScript issue's draft that says go is built as %q, and its problem is %q; want it refused for the replay TypeScript owes", m.Language, r.Problem)
	}
	for _, field := range []string{`"existing": ["hooks/use-buffer.ts"]`, `"language": "typescript"`, `"existing": ["hooks/use-buffer.ts"], "language": "python"`} {
		r = draft("go", says(field))
		if m := r.Proposal.Manifest(); r.Problem != "" || m.Language != "go" || len(m.Existing) != 0 || len(r.Proposal.Existing) != 0 {
			t.Errorf("a Go issue's draft that says %s is built as %q, checking %v as it is, and its problem is %q; want a Go project that holds its own code", field, m.Language, m.Existing, r.Problem)
		}
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
		// A preview's model is staged as its project too, whatever it says.
		m.Language = "go"
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
	// The refused option keeps the receipt that says why, and the option
	// that stands is a whole preview on its own: its chart is on its own
	// model, not on the copy the check staged.
	if a.Receipt == nil || !a.Receipt.Passed || a.Receipt.Bugs[0].Replay == nil || !a.Receipt.Bugs[0].Replay.Required || a.Receipt.Bugs[0].Replay.Request != nil {
		t.Errorf("option A's receipt doesn't keep the path that couldn't be prepared: %+v", a.Receipt)
	}
	if err := p.Forks[0].ValidatePreview(p.Forks[0].Options[1]); err != nil || good.Model.Reader == nil || good.Model.Language != "go" {
		t.Errorf("option B doesn't stand on its own: %v, reader %v, language %q", err, good.Model.Reader != nil, good.Model.Language)
	}
	// In Go neither is replayed, and both stand.
	if _, _, err := CheckWithContext(t.Context(), ws, tc, CheckContext{Language: "go"}); err != nil {
		t.Fatalf("the same options in Go: %v", err)
	}
}
