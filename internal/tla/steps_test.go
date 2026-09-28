package tla

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// render writes steps compactly, such as `\E r \in RM : TMRcvPrepared(r)`.
func render(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		var b strings.Builder
		for _, bd := range s.Binders {
			b.WriteString(`\E ` + bd.Var + ` \in ` + bd.Domain + ` : `)
		}
		b.WriteString(s.Name)
		if len(s.Args) > 0 {
			b.WriteString("(" + strings.Join(s.Args, ", ") + ")")
		}
		out[i] = b.String()
	}
	return out
}

// Steps reads the models in this repository as they were written, each in
// one of the shapes it handles. They're copies in testdata, since the
// factory changes a model's Next when it rebuilds a project, as #79 did.
func TestStepsOfTheRepositorysModels(t *testing.T) {
	root := "testdata"
	for file, want := range map[string][]string{
		"TwoPhase.tla": {
			"TMCommit", "TMAbort",
			`\E r \in RM : TMRcvPrepared(r)`, `\E r \in RM : RMPrepare(r)`, `\E r \in RM : RMChooseToAbort(r)`,
			`\E r \in RM : RMRcvCommitMsg(r)`, `\E r \in RM : RMRcvAbortMsg(r)`,
		},
		"LogBuffer.tla": {
			`\E p \in Producers : Write(p)`, "Ship", "ShipFail", "Done",
		},
		"RateLimiter.tla": {
			`\E a \in Apis : MakeCall(a)`, "Tick", "Done",
		},
		"ConnectionPool.tla": {
			`\E c \in Clients : Acquire(c)`, `\E c \in Clients : Refuse(c)`, `\E c \in Clients : Release(c)`,
		},
		"IssueProtocol.tla": {
			`\E a \in Actors : Solve(a)`, `\E a \in Actors : Revise(a)`, `\E a \in Actors : Retry(a)`,
			`\E a \in Actors : \E q \in Questions : Choose(a, q)`, `\E a \in Actors : \E p \in Proposals : Ratify(a, p)`,
			"Build", "StopBuild", "RefuseBuild", "BuildFailsGate",
			`\E h \in Heads : \E l \in Locks : \E s \in { "one" , "many" } : Push(h, l, s)`,
			"CIGate", "NoticeFail", "NoticeUnmergeable", "Merge", "OthersMerge", "OthersClose",
			`\E l \in Locks : BaseMoves(l)`, "Finished",
		},
		"WatcherRecovery.tla": {
			`\E c \in Cmds : Give(c)`, "CIPass", "Expire",
			`\E w \in Watchers : Acquire(w)`, `\E w \in Watchers : Crash(w)`, `\E w \in Watchers : Stall(w)`, `\E w \in Watchers : Restart(w)`,
			`\E w \in Watchers : Act(w)`, `\E w \in Watchers : FinishRun(w)`, `\E w \in Watchers : DropRun(w)`,
			"Rest",
		},
	} {
		src, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		steps, err := Steps(string(src))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if got := render(steps); !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", file, got, want)
		}
	}
}

// Every model in the repository, as it is now, writes Next in a shape Steps
// reads, and every step it finds is an operator the model defines.
func TestStepsOfEveryModel(t *testing.T) {
	var files []string
	for _, dir := range []string{"examples", "factory"} {
		found, err := filepath.Glob(filepath.Join("..", "..", dir, "*", ".invariant", "specs", "*.tla"))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, found...)
	}
	if len(files) < 10 {
		t.Fatalf("found only %d models", len(files))
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		steps, err := Steps(string(src))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if len(steps) == 0 {
			t.Errorf("%s: no steps", file)
		}
		for _, st := range steps {
			if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(st.Name) + `\s*(\(|==)`).MatchString(string(src)) {
				t.Errorf("%s: %s isn't an operator the model defines", file, st.Name)
			}
		}
	}
}

func TestStepsReadsLayoutAndOperators(t *testing.T) {
	src := `---- MODULE M ----
Next ==
    \/ \E c \in Cmds : Give(c)  \* this item ends at the next bullet
    \/ (* a block comment *) CIPass
    \/ \E h \in 1..MaxHeads, k \in {<<1, 2>>} :
        \/ Move(h, k[1] + 1)
        \/ Hold(h) \/ Drop(h)
    \/ Rest
====`
	steps, err := Steps(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`\E c \in Cmds : Give(c)`, "CIPass",
		`\E h \in 1 .. MaxHeads : \E k \in { << 1 , 2 >> } : Move(h, k [ 1 ] + 1)`,
		`\E h \in 1 .. MaxHeads : \E k \in { << 1 , 2 >> } : Hold(h)`,
		`\E h \in 1 .. MaxHeads : \E k \in { << 1 , 2 >> } : Drop(h)`,
		"Rest"}
	if got := render(steps); !reflect.DeepEqual(got, want) {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// A Next that isn't a disjunction of named steps can't name a driver's steps.
func TestStepsRefusesOtherShapes(t *testing.T) {
	for _, next := range []string{
		"Next == /\\ x' = x + 1 /\\ y' = y",
		"Next == A \\/ (x' = 1 /\\ UNCHANGED y)",
		"Next == IF x > 0 THEN A ELSE B",
		"Next == A \\/ UNCHANGED vars",
		"Next == \\E a : A(a)",
	} {
		if steps, err := Steps("---- MODULE M ----\n" + next + "\n===="); err == nil {
			t.Errorf("Steps(%q) = %v; want an error", next, render(steps))
		}
	}
}
