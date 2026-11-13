//go:build integration

package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

const replayIntegrationModel = `---- MODULE ReplayRegression ----
EXTENDS Naturals, Sequences, ReplayLocal
CONSTANT People, Limit
VARIABLE n, history, fault, data
vars == <<n, history, fault, data>>
Init == n = 0 /\ history = <<>> /\ fault = 0
        /\ data = [members |-> People, text |-> "p1", sequence |-> <<1, 2>>, mapping |-> [p \in People |-> p]]
Put(p) == n < Limit /\ n' = n + 1 /\ history' = Append(history,p) /\ UNCHANGED <<fault, data>>
Finished == n = Limit /\ fault = 0 /\ UNCHANGED vars
Reveal == fault > 0 /\ n' = Limit + fault /\ UNCHANGED <<history, fault, data>>
Next == (\E p \in People : Put(p)) \/ Finished \/ Reveal
Spec == Init /\ [][Next]_vars
Safe == n <= Limit /\ Helper
BreakA(p) == n = Limit /\ fault = 0 /\ fault' = 1 /\ history' = Append(history,p) /\ UNCHANGED <<n, data>>
BreakB(p) == n = Limit /\ fault = 0 /\ fault' = 2 /\ history' = Append(history,p) /\ UNCHANGED <<n, data>>
BadA == \E p \in People : BreakA(p)
BadB == \E p \in People : BreakB(p)
====
`

func writeReplayIntegration(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPinnedTLCBugReplayExtraction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	dir := t.TempDir()
	local := "---- MODULE ReplayLocal ----\nHelper == TRUE\n====\n"
	writeReplayIntegration(t, dir, "ReplayRegression.tla", replayIntegrationModel)
	writeReplayIntegration(t, dir, "ReplayLocal.tla", local)
	cfg := tlc.Config{Specification: "Spec", Constants: map[string]string{"People": "{p1, p2}", "Limit": "2"}, Invariants: []string{"Safe"}}
	var first tlc.ReplayPath
	var original tlc.Result
	for _, bug := range []string{"BadA", "BadB"} {
		checkDir := t.TempDir()
		writeReplayIntegration(t, checkDir, "ReplayRegression.tla", replayIntegrationModel)
		writeReplayIntegration(t, checkDir, "ReplayLocal.tla", local)
		writeReplayIntegration(t, checkDir, "ReplayBug.tla", fmt.Sprintf("---- MODULE ReplayBug ----\nEXTENDS ReplayRegression\nBugSpec == Init /\\ [][Next \\/ %s]_vars\n====\n", bug))
		bugCfg := cfg
		bugCfg.Specification = "BugSpec"
		trace, err := runner.Check(ctx, checkDir, "ReplayBug", bugCfg)
		if err != nil || trace.Outcome != tlc.Violated || trace.Invariant != "Safe" || len(trace.Trace) != 5 {
			t.Fatalf("counterexample: %+v %v", trace, err)
		}
		for i := range trace.Trace {
			trace.Trace[i].Action = "MisleadingDisplayLabel"
		}
		spec := tlc.ReplaySpec{Module: "ReplayRegression", Bug: bug, Expect: "Safe", ExpectKind: project.Invariant, Config: cfg}
		path, err := runner.ExtractReplay(ctx, dir, spec, trace)
		if err != nil {
			t.Fatal(err)
		}
		if len(path.States) != 5 || len(path.Steps) != 4 || path.Offending != 2 || path.Steps[3].Action != "Reveal" || path.Steps[3].Bug {
			t.Fatalf("lost delayed violation: %+v", path)
		}
		for i := 0; i < 3; i++ {
			step := path.Steps[i]
			want := "Put"
			if i == 2 {
				want = strings.Replace(bug, "Bad", "Break", 1)
			}
			history := path.States[i+1]["history"]
			if step.Action != want || step.Bug != (i == 2) || len(step.Args) != 1 || step.Args[0].Kind != "model" || !reflect.DeepEqual(step.Args[0], history.Elements[len(history.Elements)-1]) {
				t.Fatalf("edge %d: %+v", i, step)
			}
		}
		data := path.States[0]["data"]
		if data.Kind != "record" || len(data.Entries) != 4 {
			t.Fatalf("typed state lost: %+v", data)
		}
		if path.States[0]["n"].Atom != "0" || path.Bounds["Limit"] != "2" || path.ModelSHA256 == "" || path.IncludesSHA256["ReplayLocal.tla"] == "" || path.Tool.Image != tc.JavaImage || path.Tool.JarSHA256 != toolchain.TLCJarSHA256 {
			t.Fatalf("unbound path: %+v", path)
		}
		again, err := runner.ExtractReplay(ctx, dir, spec, trace)
		if err != nil || path.ID != again.ID {
			t.Fatalf("unstable extraction: %v", err)
		}
		if first.ID == "" {
			first, original = path, copyReplay(trace)
		} else if first.ID == path.ID || !reflect.DeepEqual(first.States[:3], path.States[:3]) {
			t.Fatal("bug paths were conflated")
		}
	}
	spec := tlc.ReplaySpec{Module: "ReplayRegression", Bug: "BadA", Expect: "Safe", ExpectKind: project.Invariant, Config: cfg}
	for _, index := range []int{0, 1} {
		bad := copyReplay(original)
		for i := range bad.Trace[index].Vars {
			if bad.Trace[index].Vars[i].Name == "n" {
				bad.Trace[index].Vars[i].Value = "9"
			}
		}
		if _, err := runner.ExtractReplay(ctx, dir, spec, bad); err == nil {
			t.Fatalf("accepted wrong Init/edge at %d", index)
		}
	}
	ambiguous := strings.Replace(replayIntegrationModel, "Next ==", "Alias(p) == Put(p)\nNext == (\\E p \\in People : Alias(p)) \\/", 1)
	writeReplayIntegration(t, dir, "ReplayRegression.tla", ambiguous)
	if _, err := runner.ExtractReplay(ctx, dir, spec, original); err == nil {
		t.Fatal("accepted ambiguous semantic actions")
	}
	writeReplayIntegration(t, dir, "ReplayRegression.tla", replayIntegrationModel)
	changed := cfg
	changed.Constants = map[string]string{"People": "{p1, p2}", "Limit": "3"}
	spec.Config = changed
	if _, err := runner.ExtractReplay(ctx, dir, spec, original); err == nil {
		t.Fatal("accepted a path under substituted bounds")
	}
	before := first.States[first.Steps[first.Offending].From]
	if err := runner.CheckReplayAlternative(ctx, dir, first, before, before, true); err != nil {
		t.Fatalf("checked normal refusal: %v", err)
	}
	writeReplayIntegration(t, dir, "ReplayLocal.tla", strings.Replace(local, "TRUE", "FALSE", 1))
	if err := runner.CheckReplayAlternative(ctx, dir, first, before, before, true); err == nil {
		t.Fatal("accepted substituted sibling bytes")
	}
	writeReplayIntegration(t, dir, "ReplayLocal.tla", local)
	otherTool := runner
	otherTool.Image = "substituted-java-image"
	if err := otherTool.CheckReplayAlternative(ctx, dir, first, before, before, true); err == nil {
		t.Fatal("accepted substituted tools")
	}
	writeReplayIntegration(t, dir, "ReplayRegression.tla", replayIntegrationModel+"\n")
	if err := runner.CheckReplayAlternative(ctx, dir, first, before, before, true); err == nil {
		t.Fatal("accepted substituted model bytes")
	}
}

func TestPinnedTLCReplayRetainsTemporalCounterexamples(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	for _, loop := range []bool{false, true} {
		dir := t.TempDir()
		action, fair := "UNCHANGED n", ""
		if loop {
			action, fair = "n' = 1 - n", " /\\ WF_n(Next)"
		}
		body := fmt.Sprintf("---- MODULE ReplayTemporal ----\nEXTENDS Integers\nVARIABLE n\nInit == n = 0\nNext == %s\nSpec == Init /\\ [][Next]_n%s\nEventually == <> (n = 3)\n====\n", action, fair)
		writeReplayIntegration(t, dir, "ReplayTemporal.tla", body)
		cfg := tlc.Config{Specification: "Spec", Properties: []string{"Eventually"}}
		trace, err := runner.Check(ctx, dir, "ReplayTemporal", cfg)
		if err != nil || trace.Outcome != tlc.PropertyViolated || len(trace.Trace) == 0 || loop && trace.Loop == 0 || !loop && !trace.Stutters {
			t.Fatalf("temporal evidence: %+v %v", trace, err)
		}
		path, err := runner.ExtractReplay(ctx, dir, tlc.ReplaySpec{Module: "ReplayTemporal", Bug: "Next", Expect: "Eventually", ExpectKind: project.Property, Config: cfg}, trace)
		if err == nil || !strings.Contains(err.Error(), "replay-unsupported-temporal") || path.ID != "" || path.Loop != trace.Loop || path.Stutters != trace.Stutters {
			t.Fatalf("infinite behavior lost: %+v %v", path, err)
		}
		p := &project.Project{Manifest: project.Manifest{Module: "ReplayTemporal.tla", Language: "typescript"}}
		bug := project.Statement{Name: "Next", Kind: project.Bug, Expect: "Eventually", CodeBehavior: "current"}
		var evidence Bug
		prepareBugReplay(ctx, &evidence, bug, p, dir, runner, cfg, trace, project.Property)
		if evidence.Replay == nil || evidence.Replay.Status != "replay-unsupported-temporal" || evidence.Replay.Request != nil || evidence.Replay.Counterexample == nil || !reflect.DeepEqual(*evidence.Replay.Counterexample, trace) {
			t.Fatalf("gate lost its temporal obligation: %+v", evidence)
		}
		trace.Trace[0].Vars[0].Value = "changed"
		if evidence.Replay.Counterexample.Trace[0].Vars[0].Value == "changed" {
			t.Fatal("retained temporal evidence aliases the caller")
		}
	}
}
