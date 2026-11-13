package formalize

import (
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

// A draft's check is told the language its project is written in, by the
// caller and over the service the drafting agent's checker reads (#910):
// the request's own, TypeScript for code checked as it is, and for an
// amendment the language of the project it changes, as the ratification an
// earlier draft of it captured has it, whatever else the request names.
func TestADraftsCheckKnowsItsProjectsLanguage910(t *testing.T) {
	python := current(t)
	python.Manifest.Language = "python"
	since := current(t)
	since.Manifest.Language = "typescript"
	code := &Existing{Paths: []string{"hooks/use-buffer.ts"}}
	for _, tc := range []struct {
		name string
		req  Request
		want string
	}{
		{"a new project in Go", Request{Repo: "o/r", Issue: 5, Title: "t", Language: "go"}, "go"},
		{"a new project with no language named", Request{Repo: "o/r", Issue: 5, Title: "t"}, ""},
		{"a new project in TypeScript", Request{Repo: "o/r", Issue: 5, Title: "t", Language: "typescript"}, "typescript"},
		{"code checked as it is", Request{Repo: "o/r", Issue: 5, Title: "t", Existing: code}, "typescript"},
		{"code checked as it is, in a request that names Go", Request{Repo: "o/r", Issue: 5, Title: "t", Language: "go", Existing: code}, "typescript"},
		{"an amendment", Request{Repo: "o/r", Issue: 5, Title: "t", Language: "go", Current: python}, "python"},
		{"an amendment that names code", Request{Repo: "o/r", Issue: 5, Title: "t", Language: "typescript", Existing: code, Current: python}, "python"},
		{"an amendment of a project that changed after an earlier draft captured it",
			Request{Repo: "o/r", Issue: 5, Title: "t", Language: "typescript", Current: since, Previous: &Proposal{Target: &Target{Dir: python.Dir, Baseline: python}}}, "python"},
	} {
		authority := tc.req.CheckContext()
		if authority.Language != tc.want {
			t.Errorf("%s: the check is told %q; want %q", tc.name, authority.Language, tc.want)
		}
		capture, release, err := stageCheckContext(t.TempDir(), authority)
		if err != nil {
			t.Fatal(err)
		}
		reference, closeContext, err := serveCheckContext(t.Context(), capture)
		if err != nil {
			t.Fatal(err)
		}
		fetched, err := LoadCheckContext(t.Context(), reference, 0)
		closeContext()
		release()
		if err != nil || fetched.Language != tc.want {
			t.Errorf("%s: the agent's checker is told %q, %v; want %q", tc.name, fetched.Language, err, tc.want)
		}
	}
}

// The check stages a draft in the language its caller names, whatever the
// draft says of itself: a draft that names a language, or code to check as
// it is, is staged as the caller's project all the same, and no code is
// checked as it is. The factory gives a draft both after the check, from
// the issue, so a draft that kept its own would be checked as one project
// and built as another. Staging changes nothing about the draft itself.
func TestADraftIsStagedInTheLanguageItsCallerNames910(t *testing.T) {
	code := []string{"hooks/use-buffer.ts"}
	for _, own := range []Draft{{}, {Language: "go"}, {Language: "typescript"}, {Language: "python"}, {Existing: code}, {Language: "go", Existing: code}} {
		for caller, want := range map[string]string{"typescript": "typescript", "python": "python", "go": "go", "": "go", "rust": "go"} {
			p := &Proposal{Draft: own, ModuleText: "---- MODULE Buffer ----\n====\n"}
			p.Name, p.Module, p.Package = "buffer", "Buffer", "buffer"
			m := p.stagedIn(caller).Manifest()
			if m.Language != want || len(m.Existing) != 0 {
				t.Errorf("a draft that says %q and names the code %v, in a project its caller says is %q, is checked as %q with the code %v; want %q and none",
					own.Language, own.Existing, caller, m.Language, m.Existing, want)
			}
			if p.Language != own.Language || !slices.Equal(p.Existing, own.Existing) {
				t.Errorf("staging a draft for %q left the draft itself saying %q and naming %v; it said %q and named %v", caller, p.Language, p.Existing, own.Language, own.Existing)
			}
		}
	}
}

// A draft is refused while a current bug's replay path can't be prepared,
// with the check's reason and what that reason asks of the drafter. A path
// that's prepared, a bug the gate doesn't replay, and a bug that breaks a
// property, whose counterexample never ends, each let it stand.
func TestADraftWithAnUnpreparedReplayIsRefused910(t *testing.T) {
	report := func(bugs ...verify.Bug) *verify.Report {
		return &verify.Report{ModelOnly: true, Passed: true, Bugs: append([]verify.Bug{
			{Name: "Old", Caught: true, Outcome: "violated"}, {Name: "Maybe", CodeBehavior: "hypothetical", Caught: true, Outcome: "violated"}}, bugs...)}
	}
	bug := func(name, outcome string, e *verify.BugReplayEvidence) verify.Bug {
		return verify.Bug{Name: name, CodeBehavior: "current", Caught: true, Outcome: outcome, Replay: e}
	}
	unprepared := func(message string) *verify.BugReplayEvidence {
		e := &verify.BugReplayEvidence{Required: true, Status: "replay-unavailable", Message: message}
		if strings.HasPrefix(message, "replay-unsupported-temporal") {
			e.Status = "replay-unsupported-temporal"
		}
		return e
	}
	const endless = "replay-unsupported-temporal: the retained infinite behavior cannot be checked by executable replay version 1"
	for name, b := range map[string]verify.Bug{
		"a prepared path":              bug("Now", "violated", &verify.BugReplayEvidence{Required: true, Status: "pending", Request: &verify.BugReplayRequest{}}),
		"a bug Go's own checks hold":   bug("Now", "violated", &verify.BugReplayEvidence{Status: "not-replayed", Message: "Go uses its existing proof, agreement and exploration checks."}),
		"a bug that breaks a property": bug("Now", "property violated", unprepared(endless)),
		"no replay evidence":           bug("Now", "violated", nil),
	} {
		if err := replayPrepared(report(b)); err != nil {
			t.Errorf("a draft with %s is refused: %v", name, err)
		}
	}

	// What each reason asks for, and nothing another reason asks for.
	const rewrite, rename, respell, again = `as in Bug == \E x \in S : BugStep(x)`, "Change what that names in the model",
		`Write the specification as Spec == Init /\ [][Next]_vars`, "the check's own failure, not a shape of the draft: check again"
	for name, tc := range map[string]struct{ message, advice string }{
		"a hidden choice":                             {"replay-unsupported-action: step 1 of the counterexample, from its state 1 to its state 2, is taken by an operation that could have reached another state with the same arguments", rewrite},
		"a step with two readings":                    {"replay-ambiguous-edge: step 1 of the counterexample, from its state 1 to its state 2, can be taken 2 ways, Put(m1) and PutAnyway(m1)", rewrite},
		"a bug that names no operation":               {`replay-unsupported-action: Now must name its parameterized operations: an \E binds "m"; want "x \in S"`, rewrite},
		"a shadowed standard operator":                {"replay-unsupported-model: the model defines Len, which the path's check takes from the standard modules", rename},
		"a specification the path's check can't read": {`replay-unsupported-temporal: specification is not Init /\ [][Next]_vars`, respell},
		"TLC stalling in the path's check":            {"replay-check-failed: Invariant_ReplayPath: error TLC stopped making progress: no output for 2m0s", again},
		"the path's check not running":                {"docker: Cannot connect to the Docker daemon", again},
	} {
		err := replayPrepared(report(bug("Now", "violated", unprepared(tc.message))))
		if err == nil || !strings.Contains(err.Error(), "bug Now is current") || !strings.Contains(err.Error(), tc.message+". ") || !strings.Contains(err.Error(), tc.advice) {
			t.Errorf("a draft with %s: %v; want it refused, by the bug's name, with the check's reason and %q", name, err, tc.advice)
			continue
		}
		for _, other := range []string{rewrite, rename, respell, again} {
			if other != tc.advice && strings.Contains(err.Error(), other) {
				t.Errorf("a draft with %s is told %q, which another reason asks for: %v", name, other, err)
			}
		}
	}

	// Every unprepared bug is named, each with its own reason: a drafter
	// that fixed the first and checked again would only then learn of the
	// second.
	err := replayPrepared(report(
		bug("First", "violated", unprepared("replay-unsupported-action: First must name its parameterized operations: an empty disjunct")),
		bug("Fine", "violated", &verify.BugReplayEvidence{Required: true, Status: "pending", Request: &verify.BugReplayRequest{}}),
		bug("Never", "property violated", unprepared(endless)),
		bug("Second", "violated", unprepared(`replay-unsupported-temporal: specification is not Init /\ [][Next]_vars`))))
	if err == nil {
		t.Fatal("a draft with two unprepared bugs stands")
	}
	lines := strings.Split(err.Error(), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "bug First is current") || !strings.Contains(lines[0], rewrite) ||
		!strings.HasPrefix(lines[1], "bug Second is current") || !strings.Contains(lines[1], respell) || strings.Contains(err.Error(), "Fine") || strings.Contains(err.Error(), "Never") {
		t.Errorf("a draft with two unprepared bugs, one prepared and one that breaks a property, is told:\n%v", err)
	}
}

// The prompt tells the drafter how to write a current bug that can be
// replayed, since a draft that can't be is refused: each choice a named
// parameter, in every action Next names, and the bug's operation enabled
// only where no action of Next takes the same step. Its own example writes
// its bug that way, since a drafter writes what the example shows.
func TestThePromptSaysHowToWriteABugThatCanBeReplayed910(t *testing.T) {
	text := Prompt(Request{Repo: "o/r", Issue: 5, Title: "t"}, 4)
	for _, want := range []string{
		`write a current bug, and every action Next names, with every choice as a parameter named in Next or in the bug's own definition, as in Bug == \E x \in S : BugStep(x), never as an \E inside an action's body`,
		"give no bound variable the name of a constant or a model value",
		"guard a current bug's operation to be enabled only where no action of Next takes the same step",
		`PutWhenFull(m) == Len(buf) >= Cap /\ buf' = Append(buf, m)`,
		"Fairness goes in fairness statements, never in `Spec`.",
		"EnterWhilePrinting(p) ==\n    /\\ pc[p] = \"waiting\"\n    /\\ \\E q \\in Procs : pc[q] = \"printing\"\n    /\\ pc' = [pc EXCEPT ![p] = \"printing\"]\n\nEnterUnchecked == \\E p \\in Procs : EnterWhilePrinting(p)\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the prompt doesn't say %q", want)
		}
	}
	if strings.Contains(text, `EnterUnchecked == \E p \in Procs : pc[p]`) {
		t.Error("the prompt's example still writes its bug as a choice inside the bug's own body, which is refused once the bug is current")
	}
}
