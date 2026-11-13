package tlc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ExtractReplay checks an existing fresh counterexample, never searches for a
// shorter substitute. ReplaySpec has Module, Bug, Expect, ExpectKind and Config.
// ReplayPath has Version, ID, Module, Bug, BugPin, Expect, ExpectKind,
// Specification, ModelSHA256, IncludesSHA256, Bounds, Tool,
// States ([]ReplayState, a map[string]ReplayValue), Steps ([]ReplayStep with
// From/To zero-based state indexes, Action, Args and Bug), Offending (zero-based
// first bug step), Loop (TLC's one-based target), and Stutters.
// The tool wrapper proves Init and every exact edge under Next OR the named
// bug and establishes the expected violation; it emits one checked candidate
// set per edge as <<"invariant-replay-v1", edgeIndex, {<<action, args, bug>>}>>.
// Exactly one semantic action/argument interpretation is required. Human
// labels are deliberately wrong below. Missing/duplicate/ambiguous evidence,
// malformed values/states, wrong violations and unsupported temporal paths
// return named errors, with no partial executable request.

const model857 = `---- MODULE ReplayFixture ----
EXTENDS Naturals, Sequences, Local
CONSTANT People
VARIABLE n, history
vars == <<n, history>>
Init == n = 0 /\ history = <<>>
Put(p) == n < 3 /\ n' = n + 1 /\ history' = Append(history,p)
Done == n = 3 /\ UNCHANGED vars
Next == (\E p \in People : Put(p)) \/ Done
Spec == Init /\ [][Next]_vars
Safe == n <= 3 /\ Helper
Eventually == <> (n = 3)
BadDrop(p) == n = 3 /\ n' = 4 /\ history' = Append(history,p)
BadReset(p) == n = 3 /\ n' = 5 /\ history' = Append(history,p)
Bad == \E p \in People : BadDrop(p)
OtherBad == \E p \in People : BadReset(p)
====
`

func init() {
	if os.Getenv("INVARIANT_REPLAY857_DOUBLE") == "1" {
		os.Exit(replayDocker857(os.Args[1:]))
	}
}

func replayDocker857(args []string) int {
	if len(args) == 0 {
		return 90
	}
	if args[0] == "rm" || args[0] == "image" {
		return 0
	}
	if args[0] != "run" {
		return 91
	}
	work := ""
	all := strings.Join(args, " ")
	for i, a := range args {
		if a == "-v" && i+1 < len(args) {
			p := strings.Split(args[i+1], ":")
			if len(p) > 1 && p[1] == "/work" {
				work = p[0]
			}
		}
	}
	if work == "" || !strings.Contains(all, "--network none") || !strings.Contains(all, "-workers 1") || !strings.Contains(all, "replay857-java") {
		return 92
	}
	model, err := os.ReadFile(filepath.Join(work, "ReplayFixture.tla"))
	if err != nil || string(model) != model857 {
		return 93
	}
	inc, err := os.ReadFile(filepath.Join(work, "Local.tla"))
	if err != nil || !strings.Contains(string(inc), "Helper == TRUE") {
		return 94
	}
	cfg, err := os.ReadFile(filepath.Join(work, "Invariant.cfg"))
	if err != nil || !strings.Contains(string(cfg), "CONSTANT People = {p1, p2}") {
		return 95
	}
	module := args[len(args)-1]
	body, err := os.ReadFile(filepath.Join(work, module))
	if err != nil {
		return 96
	}
	log := os.Getenv("INVARIANT_REPLAY857_LOG")
	f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return 97
	}
	encoded, _ := json.Marshal(map[string]any{"args": args, "body": string(body), "config": string(cfg)})
	f.Write(append(encoded, '\n'))
	f.Close()
	msg := func(code int, body string) {
		fmt.Printf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
	}
	mode := os.Getenv("INVARIANT_REPLAY857_MODE")
	if mode == "tool-error" {
		msg(1000, "replay check failed")
		return 150
	}
	if mode == "wrong-edge" {
		msg(2110, "Invariant ReplayEdge is violated.")
		return 12
	}
	last := "BadDrop"
	if strings.Contains(string(body), "OtherBad") {
		last = "BadReset"
	}
	for i, row := range []string{`{<<"Put", <<p1>>, FALSE>>}`, `{<<"Put", <<p2>>, FALSE>>}`, `{<<"Put", <<p1>>, FALSE>>}`, `{<<"` + last + `", <<p2>>, TRUE>>}`} {
		if mode == "missing" && i == 1 {
			continue
		}
		if mode == "ambiguous" && i == 1 {
			row = `{<<"Put", <<p1>>, FALSE>>, <<"Put", <<p2>>, FALSE>>}`
		}
		if mode == "malformed" && i == 1 {
			row = `{<<"Put", <<p2>> >>}`
		}
		fmt.Printf("<<\"invariant-replay-v1\", %d, %s>>\n", i, row)
		if mode == "duplicate" && i == 1 {
			fmt.Printf("<<\"invariant-replay-v1\", %d, %s>>\n", i, row)
		}
	}
	msg(2193, "Model checking completed. No error has been found.")
	msg(2199, "5 states generated, 5 distinct states found, 0 states left on queue.")
	msg(2186, "Finished.")
	return 0
}

func replayFixture857(t *testing.T) (Runner, string, ReplaySpec, Result, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"ReplayFixture.tla": model857, "Local.tla": "---- MODULE Local ----\nHelper == TRUE\n====\n", "tools.jar": "fixture jar bytes"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	launcher := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' \"$@\"\n"
	if err = os.WriteFile(filepath.Join(bin, "docker"), []byte(launcher), 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "calls.jsonl")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INVARIANT_REPLAY857_DOUBLE", "1")
	t.Setenv("INVARIANT_REPLAY857_LOG", log)
	t.Setenv("INVARIANT_REPLAY857_MODE", "")
	r := Result{Outcome: Violated, Invariant: "Safe", Version: "TLC fixture"}
	for i, h := range []string{"<<>>", "<<p1>>", "<<p1,p2>>", "<<p1,p2,p1>>", "<<p1,p2,p1,p2>>"} {
		r.Trace = append(r.Trace, State{Index: i + 1, Action: "MisleadingAlias", Vars: []Var{{Name: "n", Value: fmt.Sprint(i)}, {Name: "history", Value: h}}})
	}
	s := ReplaySpec{Module: "ReplayFixture", Bug: "Bad", Expect: "Safe", ExpectKind: "invariant", Config: Config{Specification: "Spec", Constants: map[string]string{"People": "{p1, p2}"}, Invariants: []string{"Safe"}}}
	return Runner{Image: "replay857-java", Jar: filepath.Join(dir, "tools.jar")}, dir, s, r, log
}

func TestReplayPathsUseEveryCheckedParameterizedEdge857(t *testing.T) {
	runner, dir, s, trace, log := replayFixture857(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := runner.ExtractReplay(ctx, dir, s, trace)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 1 || p.ID == "" || len(p.States) != 5 || len(p.Steps) != 4 || p.Offending != 3 {
		t.Fatalf("incomplete path: %+v", p)
	}
	if p.Bug != s.Bug || p.Expect != s.Expect || p.ExpectKind != s.ExpectKind || p.Specification != s.Config.Specification || p.BugPin == "" {
		t.Fatalf("path does not bind its exact bug and expected violation: %+v", p)
	}
	for i, step := range p.Steps {
		want := "Put"
		if i == 3 {
			want = "BadDrop"
		}
		arg := "p1"
		if i == 1 || i == 3 {
			arg = "p2"
		}
		if step.From != i || step.To != i+1 || step.Action != want || len(step.Args) != 1 || step.Args[0].Kind != "model" || step.Args[0].Atom != arg || step.Bug != (i == 3) {
			t.Errorf("edge %d: %+v", i, step)
		}
	}
	if p.ModelSHA256 == "" || p.IncludesSHA256["Local.tla"] == "" || p.Tool.JarSHA256 == "" || p.Tool.Image != "replay857-java" || p.Bounds["People"] != "{p1, p2}" {
		t.Fatalf("path lacks exact source/tool/bounds binding: %+v", p)
	}
	again, err := runner.ExtractReplay(ctx, dir, s, trace)
	if err != nil || again.ID != p.ID {
		t.Fatalf("path identity is not deterministic: %v", err)
	}
	other := s
	other.Bug = "OtherBad"
	secondTrace := trace
	secondTrace.Trace = append([]State(nil), trace.Trace...)
	secondTrace.Trace[4].Vars = []Var{{Name: "n", Value: "5"}, {Name: "history", Value: "<<p1,p2,p1,p2>>"}}
	q, err := runner.ExtractReplay(ctx, dir, other, secondTrace)
	if err != nil {
		t.Fatal(err)
	}
	if q.ID == p.ID || q.Steps[3].Action != "BadReset" || !reflect.DeepEqual(q.States[:4], p.States[:4]) {
		t.Fatalf("two bugs share/rewrite a replay: %+v %+v", p, q)
	}
	trace.Trace[0].Vars[0].Value = "99"
	if p.States[0]["n"].Atom != "0" {
		t.Fatal("retained path aliases caller trace")
	}
	b, err := os.ReadFile(log)
	if err != nil || len(strings.Split(strings.TrimSpace(string(b)), "\n")) < 3 {
		t.Fatal("path action interpretations never reached TLC")
	}
}

func TestReplayPathsRefuseMalformedAmbiguousAndTemporalEvidence857(t *testing.T) {
	runner, dir, s, trace, _ := replayFixture857(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, mode := range []string{"missing", "duplicate", "ambiguous", "malformed", "wrong-edge", "tool-error"} {
		t.Setenv("INVARIANT_REPLAY857_MODE", mode)
		if _, err := runner.ExtractReplay(ctx, dir, s, trace); err == nil {
			t.Errorf("accepted %s action/edge evidence", mode)
		}
	}
	t.Setenv("INVARIANT_REPLAY857_MODE", "")
	for _, edit := range []func(*Result){
		func(r *Result) { r.Invariant = "Other" }, func(r *Result) { r.Trace = nil },
		func(r *Result) { r.Trace[2].Index = 4 },
		func(r *Result) { r.Trace[1].Vars = append(r.Trace[1].Vars, Var{Name: "n", Value: "3"}) },
		func(r *Result) { r.Trace[1].Vars = r.Trace[1].Vars[:1] },
	} {
		b, _ := json.Marshal(trace)
		var bad Result
		if err := json.Unmarshal(b, &bad); err != nil {
			t.Fatal(err)
		}
		edit(&bad)
		if _, err := runner.ExtractReplay(ctx, dir, s, bad); err == nil {
			t.Errorf("accepted malformed trace: %+v", bad)
		}
	}
	for _, stutter := range []bool{false, true} {
		temporal := trace
		temporal.Outcome = PropertyViolated
		temporal.Invariant = ""
		temporal.Property = "Eventually"
		temporal.Stutters = stutter
		if !stutter {
			temporal.Loop = 2
		}
		ts := s
		ts.Expect = "Eventually"
		ts.ExpectKind = "property"
		p, err := runner.ExtractReplay(ctx, dir, ts, temporal)
		if err == nil || !strings.Contains(err.Error(), "replay-unsupported-temporal") || p.Loop != temporal.Loop || p.Stutters != stutter {
			t.Errorf("temporal behavior reduced to a finite success: %+v %v", p, err)
		}
	}
}
