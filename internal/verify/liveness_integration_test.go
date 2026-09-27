//go:build integration

package verify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// answering is a watcher that answers the commands people ask, under weak
// fairness: the gate's liveness checks on a model alone. The test builds the
// project from testdata/Answering.tla, so the repository holds no project
// for CI or the dashboard to find.
var answering = []project.Statement{
	{Name: "Spec", Kind: project.Spec, Says: "People ask commands, and the watcher answers them."},
	{Name: "TypeOK", Kind: project.Invariant, Says: "Every command is one of the commands."},
	{Name: "AnsweredOnce", Kind: project.Invariant, Says: "A command is never both pending and answered."},
	{Name: "SomeAnswered", Kind: project.Witness, Says: "The watcher can answer a command."},
	{Name: "EventuallyAnswered", Kind: project.Property, Says: "Every command that's asked is eventually answered."},
	{Name: "AnswersStay", Kind: project.Property, Says: "An answer is never taken back."},
	{Name: "WatcherIsFair", Kind: project.Fairness, Says: "The watcher, whenever it can answer, eventually does."},
	{Name: "Drop", Kind: project.Bug, Says: "The watcher drops a pending command without answering it.", Expect: "EventuallyAnswered"},
}

// answeringProject writes the project with each edit applied to the module,
// leaving out the statements named in drop, and pins it as a person
// ratifying it would.
func answeringProject(t *testing.T, drop string, edits ...func(string) string) string {
	t.Helper()
	src, err := os.ReadFile("testdata/Answering.tla")
	if err != nil {
		t.Fatal(err)
	}
	module := string(src)
	for _, edit := range edits {
		module = edit(module)
	}
	dir := t.TempDir()
	specs := filepath.Join(dir, ".invariant", "specs")
	if err := os.MkdirAll(specs, 0o755); err != nil {
		t.Fatal(err)
	}
	var statements []project.Statement
	for _, st := range answering {
		if st.Name != drop {
			statements = append(statements, st)
		}
	}
	manifest := project.Manifest{Name: "answering", Module: ".invariant/specs/Answering.tla", Code: "answering", Language: "go"}
	lock := project.Lock{Decision: "a test", Bounds: map[string]string{"Commands": "{c1, c2}"}, Statements: statements}
	for file, v := range map[string]any{"invariant.json": manifest, "ratified.lock": lock} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".invariant", file), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(specs, "Answering.tla"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Pin(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runModel(t *testing.T, dir string) *Report {
	t.Helper()
	ctx := context.Background()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := RunModel(ctx, dir, tc)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLivenessHolds(t *testing.T) {
	r := runModel(t, answeringProject(t, ""))
	if !r.Passed {
		t.Fatalf("the answering model failed: %+v", r)
	}
	if len(r.Properties) != 2 || !r.Properties[0].Holds || !r.Properties[1].Holds {
		t.Errorf("properties = %+v; want both to hold", r.Properties)
	}
	if len(r.Fairness) != 1 || !r.Fairness[0].InNext || r.Fairness[0].Action != "Watch" {
		t.Errorf("fairness = %+v; want Watch, a Next step", r.Fairness)
	}
	if len(r.Bugs) != 1 || !r.Bugs[0].Caught || r.Bugs[0].Violated != "EventuallyAnswered" {
		t.Errorf("bugs = %+v; want Drop caught by EventuallyAnswered", r.Bugs)
	}
}

// Without fairness, the watcher may never answer, and the property fails.
func TestAPropertyWithoutItsFairnessFails(t *testing.T) {
	r := runModel(t, answeringProject(t, "WatcherIsFair"))
	if r.Passed {
		t.Fatal("a property that needs fairness passed without it")
	}
	p := r.Properties[0]
	if p.Name != "EventuallyAnswered" || p.Holds || !p.Stutters {
		t.Fatalf("EventuallyAnswered = %+v; want broken by a behavior that stops", p)
	}
}

// Fairness on a step Next can't take leaves no fair behavior from the states
// where it's enabled, so the property would hold vacuously. Here Next
// answers every pending command at once, and the fair action answers one.
func TestFairnessOnAStepOutsideNextFails(t *testing.T) {
	r := runModel(t, answeringProject(t, "", replace(t, `Next == (\E c \in Commands : Ask(c)) \/ Watch \/ Finished`,
		`AnswerAll == pending # {} /\ pending' = {} /\ answered' = answered \cup pending

Next == (\E c \in Commands : Ask(c)) \/ AnswerAll \/ Finished`)))
	if r.Passed {
		t.Fatal("fairness on a step outside Next passed")
	}
	if f := r.Fairness[0]; f.InNext || !strings.Contains(f.Message, "isn't a Next step") {
		t.Fatalf("fairness = %+v; want Watch found outside Next", f)
	}
}

// A spec that states its own fairness would leave it out of the bug checks,
// unchecked, so the gate requires fairness statements.
func TestFairnessInTheSpecFails(t *testing.T) {
	r := runModel(t, answeringProject(t, "", replace(t, `Spec == Init /\ [][Next]_vars`, `Spec == Init /\ [][Next]_vars /\ WF_vars(Watch)`)))
	if r.Passed {
		t.Fatal("a spec with its own fairness passed")
	}
	if p := r.Properties[0]; p.Holds || !strings.Contains(p.Message, "states fairness itself") {
		t.Fatalf("property = %+v; want the spec's own fairness refused", p)
	}
}

// A bug a property must catch, which it doesn't, fails the gate: here the
// bug answers the command, as the watcher does, which breaks nothing.
func TestAPropertyBugThatIsNotCaughtFails(t *testing.T) {
	r := runModel(t, answeringProject(t, "", replace(t, `Drop == \E c \in pending : pending' = pending \ {c} /\ UNCHANGED answered`,
		`Drop == \E c \in pending : pending' = pending \ {c} /\ answered' = answered \cup {c}`)))
	if r.Passed {
		t.Fatal("a bug the property doesn't catch passed")
	}
	if b := r.Bugs[0]; b.Caught || !strings.Contains(b.Message, "expected a behavior that breaks EventuallyAnswered") {
		t.Fatalf("bug = %+v; want not caught", b)
	}
}
