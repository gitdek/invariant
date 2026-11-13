package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
	"github.com/gitdek/invariant/internal/verify/internal/pythongatetest"
)

// This is one complete plumbing step. No production replay adapter or public
// Run option may inject successful replay evidence in this change. Every
// current bug of a driver-based gate remains required and fails with the
// named replay-unavailable status; model-only checking prepares its request
// without executing code or changing model success. Go agreement/exploration
// retains its existing meaning. Legacy and hypothetical bugs never request
// replay; missing classification never triggers inference or amendment.
//
// The builder also adds internal/verify/bug_replay_integration_test.go under
// the existing integration tag/lane: real pinned TLC extracts two different
// bug paths, repeated parameterized actions (including an existential bug
// alias), typed values and an offending action before a later violating
// state; checks the exact prefix, Init, bounds/includes/tool bindings; refuses
// nonmatching/ambiguous edges and retains loop/stutter counterexamples with
// replay-unsupported-temporal. No finite lasso prefix proves liveness.
// Existing liveness, assurance and Python gate refusal suites remain intact.
// In particular, pythongatetest's unclassified legacy fixtures stay that way;
// only new-proposal fixtures are classified. The named dashboard assertion
// changes from merely Caught to Caught plus the unclassified replay label.

const gateModel857 = `---- MODULE BugGate ----
EXTENDS Naturals, Local
CONSTANT Limit, People
VARIABLE n
vars == <<n>>
Init == n = 0
Inc(p) == p \in People /\ n < Limit /\ n' = n + 1
Done == n = Limit /\ UNCHANGED n
Reset == n = Limit /\ n' = 0
Next == (\E p \in People : Inc(p)) \/ Done \/ Reset
Spec == Init /\ [][Next]_vars
Safe == n <= Limit /\ Helper
BreakA(p) == p \in People /\ n = Limit /\ n' = Limit + 1
BreakB(p) == p \in People /\ n = Limit /\ n' = Limit + 2
BadA == \E p \in People : BreakA(p)
BadB == \E p \in People : BreakB(p)
====
`

func init() {
	if os.Getenv("INVARIANT_GATE857_DOUBLE") == "1" {
		os.Exit(gateDocker857(os.Args[1:]))
	}
}

func gateDocker857(args []string) int {
	if len(args) == 0 {
		return 90
	}
	if args[0] == "rm" || args[0] == "image" {
		return 0
	}
	if args[0] == "info" {
		return 1
	}
	if args[0] != "run" {
		return 91
	}
	all := strings.Join(args, " ")
	imageArg := func(want string) bool {
		for _, arg := range args {
			if arg == want {
				return true
			}
		}
		return false
	}
	mounts := map[string]string{}
	for i, a := range args {
		if a == "-v" && i+1 < len(args) {
			p := strings.Split(args[i+1], ":")
			if len(p) > 1 {
				mounts[p[1]] = p[0]
			}
		}
	}
	if !strings.Contains(all, "--network none") {
		return 92
	}
	kind := ""
	body := ""
	if strings.Contains(all, "tlc2.TLC") {
		kind = "tlc"
		module := args[len(args)-1]
		b, e := os.ReadFile(filepath.Join(mounts["/work"], module))
		if e != nil {
			return 93
		}
		body = string(b)
	} else if imageArg("gate857-node") {
		kind = "driver"
	} else if imageArg("gate857-go") {
		kind = "go"
	} else if imageArg("gate857-gobra") {
		kind = "proof"
	} else {
		return 94
	}
	f, e := os.OpenFile(os.Getenv("INVARIANT_GATE857_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return 95
	}
	b, _ := json.Marshal(map[string]string{"kind": kind, "body": body, "args": all})
	f.Write(append(b, '\n'))
	f.Close()
	msg := func(code int, body string) {
		fmt.Printf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
	}
	switch kind {
	case "driver":
		if os.Getenv("INVARIANT_GATE857_NO_CODE") == "1" {
			return 96
		}
		recording := `{"traces":[[{"n":0},{"n":1}]]}`
		if os.Getenv("INVARIANT_GATE857_EXHAUSTIVE") == "1" {
			recording = `{"states":[{"n":0},{"n":1},{"n":2}],"init":[0],"attempts":[[0,"Inc",[{"$mv":"p1"}],1],[1,"Inc",[{"$mv":"p1"}],2],[2,"Inc",[{"$mv":"p1"}],2],[0,"Done",[],0],[1,"Done",[],1],[2,"Done",[],2],[0,"Reset",[],0],[1,"Reset",[],1],[2,"Reset",[],0]]}`
		}
		if e := os.WriteFile(filepath.Join(mounts["/out"], "traces.json"), []byte(recording), 0600); e != nil {
			return 97
		}
		fmt.Print("@@invariant test=0\n@@invariant conform=0\n")
		return 0
	case "go":
		fmt.Print("@@invariant vet=0\n@@invariant test=0\ninvariant-agreement states=3 depth=3\n@@invariant agree=0\n")
		return 0
	case "proof":
		fmt.Print("Gobra found 0 errors\n")
		return 0
	}
	module := args[len(args)-1]
	if module == "Invariant_ReplayResult.tla" && strings.Contains(body, "99") {
		msg(2110, "Invariant Invariant_ReplayAllowed is violated.")
		return 12
	}
	if strings.HasPrefix(module, "Bug_") && os.Getenv("INVARIANT_GATE857_MISS_BUG") != "1" {
		msg(2110, "Invariant Safe is violated.")
		for i, n := range []int{0, 1, 2, 3} {
			if strings.Contains(module, "BadB") && i == 3 {
				n = 4
			}
			msg(2217, fmt.Sprintf("%d: <OpaqueAlias line 1, col 1 to line 1, col 2 of module BugGate>\n/\\ n = %d", i+1, n))
		}
		msg(2186, "Finished.")
		return 12
	}
	if strings.Contains(body, "invariant-replay-v1") {
		last := "BreakA"
		if strings.Contains(body, "BadB") {
			last = "BreakB"
		}
		fmt.Print("<<\"invariant-replay-v1\", 0, {<<\"Inc\", <<p1>>, FALSE>>}>>\n<<\"invariant-replay-v1\", 1, {<<\"Inc\", <<p1>>, FALSE>>}>>\n")
		fmt.Printf("<<\"invariant-replay-v1\", 2, {<<\"%s\", <<p1>>, TRUE>>}>>\n", last)
	}
	msg(2193, "Model checking completed. No error has been found.")
	msg(2199, "4 states generated, 3 distinct states found, 0 states left on queue.")
	msg(2194, "The depth of the complete state graph search is 3.")
	msg(2186, "Finished.")
	return 0
}

func write857(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}

func gateFixture857(t *testing.T, language string, classes []string, exhaustive bool) (string, toolchain.Toolchain, string) {
	t.Helper()
	dir := t.TempDir()
	includes := map[string]string{"Local.tla": "---- MODULE Local ----\nHelper == TRUE\n====\n"}
	m, err := tla.Read(gateModel857, includes)
	if err != nil {
		t.Fatal(err)
	}
	ss := []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "The counter advances."}, {Name: "Safe", Kind: project.Invariant, Says: "The counter is bounded."}}
	for i, class := range classes {
		name := []string{"BadA", "BadB"}[i]
		raw := map[string]any{"name": name, "kind": "bug", "says": "The counter goes past the limit.", "expect": "Safe"}
		if class != "" {
			raw["code_behavior"] = class
		}
		b, _ := json.Marshal(raw)
		var s project.Statement
		if err = json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
		ss = append(ss, s)
	}
	for i := range ss {
		ss[i].SHA256, err = m.PinHash(ss[i].Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
	}
	lock := project.Lock{Decision: "test fixture", Bounds: map[string]string{"Limit": "2", "People": "{p1}"}, Statements: ss}
	lock.Decision = ""
	lock.Ratified = &project.Ratification{By: "fixture", Issue: 857, Proposal: project.ProposalHash(lock.Bounds, lock.Statements)}
	manifest := project.Manifest{Name: "bug replay gate", Module: ".invariant/specs/BugGate.tla", Code: "core", Language: language, Conformance: "driver.ts", Exhaustive: exhaustive}
	if language == "go" {
		manifest.Conformance = ""
		write857(t, dir, "go.mod", "module example.test/bugreplay\n\ngo 1.27.1\n")
		write857(t, dir, "core/core.go", "// +gobra\npackage core\n//@ ensures result >= 0\nfunc Safe() (result int) { return 0 }\n")
	} else {
		write857(t, dir, "core/core.ts", "export const n = 0;\n")
		write857(t, dir, "driver.ts", "import './core/core.ts';\n")
	}
	for name, v := range map[string]any{".invariant/invariant.json": manifest, ".invariant/ratified.lock": lock} {
		b, _ := json.Marshal(v)
		write857(t, dir, name, string(b))
	}
	write857(t, dir, manifest.Module, gateModel857)
	write857(t, dir, ".invariant/specs/Local.tla", includes["Local.tla"])
	write857(t, dir, "tools.jar", "test jar")
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	bin := t.TempDir()
	write857(t, bin, "docker", "#!/bin/sh\nexec '"+strings.ReplaceAll(exe, "'", "'\"'\"'")+"' \"$@\"\n")
	if e = os.Chmod(filepath.Join(bin, "docker"), 0700); e != nil {
		t.Fatal(e)
	}
	log := filepath.Join(dir, "commands.jsonl")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INVARIANT_GATE857_DOUBLE", "1")
	t.Setenv("INVARIANT_GATE857_LOG", log)
	t.Setenv("INVARIANT_GATE857_NO_CODE", "")
	t.Setenv("INVARIANT_GATE857_MISS_BUG", "")
	t.Setenv("INVARIANT_GATE857_EXHAUSTIVE", "")
	if exhaustive {
		t.Setenv("INVARIANT_GATE857_EXHAUSTIVE", "1")
	}
	return dir, toolchain.Toolchain{JavaImage: "gate857-java", NodeImage: "gate857-node", GoImage: "gate857-go", GobraImage: "gate857-gobra", TLCJar: filepath.Join(dir, "tools.jar")}, log
}

func run857(t *testing.T, dir string, tc toolchain.Toolchain, model bool) *verify.Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var r *verify.Report
	var err error
	if model {
		r, err = verify.RunModel(ctx, dir, tc)
	} else {
		r, err = verify.Run(ctx, dir, "", tc)
	}
	if err != nil || r == nil {
		t.Fatalf("gate didn't return evidence: %v %+v", err, r)
	}
	return r
}

func TestCurrentBugsBlockDriverGatesAndRetainIndependentPaths857(t *testing.T) {
	for _, exhaustive := range []bool{false, true} {
		dir, tc, _ := gateFixture857(t, "typescript", []string{"current", "current"}, exhaustive)
		r := run857(t, dir, tc, false)
		if r.Passed {
			t.Error("current bug obligations disappeared behind otherwise passing driver checks")
		}
		if !r.Design.Passed || !r.Build.Passed || r.Conformance == nil || !r.Conformance.Passed {
			t.Fatalf("independent successful evidence lost: %+v", r)
		}
		ids := map[string]bool{}
		for _, b := range r.Bugs {
			if !b.Caught || b.Replay == nil || !b.Replay.Required || b.Replay.Status != "replay-unavailable" || b.Replay.Passed {
				t.Fatalf("model detection treated as code replay: %+v", b)
			}
			if b.Replay.Counterexample == nil || len(b.Replay.Counterexample.Trace) != 4 {
				t.Fatalf("fresh TLC counterexample lost without outDir: %+v", b)
			}
			q := b.Replay.Request
			if q == nil || q.ID == "" || ids[q.ID] || q.Bug.Name != b.Name || q.Bug.CodeBehavior != "current" || q.Proposal != r.Proposal || len(q.Path.States) != 4 || len(q.Path.Steps) != 3 || q.Path.Offending != 2 {
				t.Fatalf("no-outDir run lost/crossed the complete obligation: %+v", q)
			}
			ids[q.ID] = true
			if q.Path.States[0]["n"].Atom != "0" || q.Path.Steps[0].Action != "Inc" || q.Path.Steps[1].Action != "Inc" || q.Path.Steps[2].Action != map[string]string{"BadA": "BreakA", "BadB": "BreakB"}[b.Name] {
				t.Errorf("substituted path: %+v", q.Path)
			}
		}
		if !strings.Contains(verify.Feedback(r), "replay-unavailable") {
			t.Fatal("builder feedback hides why the code gate cannot pass")
		}
	}
}

func TestModelOnlyPreparesCurrentReplayWithoutCode857(t *testing.T) {
	dir, tc, log := gateFixture857(t, "typescript", []string{"current", "hypothetical"}, false)
	t.Setenv("INVARIANT_GATE857_NO_CODE", "1")
	r := run857(t, dir, tc, true)
	if !r.Passed || !r.ModelOnly || !r.Bugs[0].Caught {
		t.Fatalf("replay unavailable changed model checking: %+v", r)
	}
	if r.Bugs[0].Replay == nil || r.Bugs[0].Replay.Request == nil || r.Bugs[0].Replay.Status != "pending" || r.Bugs[0].Replay.Passed {
		t.Fatalf("model check claims/lacks replay: %+v", r.Bugs[0])
	}
	if r.Bugs[1].Replay != nil && r.Bugs[1].Replay.Required {
		t.Fatal("hypothetical bug creates code obligation")
	}
	b, e := os.ReadFile(log)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), `"kind":"driver"`) || strings.Contains(string(b), `"kind":"proof"`) {
		t.Fatal("model-only invoked code")
	}
}

func TestLegacyAndMixedBugsKeepModelChecksWithoutInventedReplays857(t *testing.T) {
	dir, tc, log := gateFixture857(t, "typescript", []string{"", "hypothetical"}, false)
	r := run857(t, dir, tc, false)
	if !r.Passed {
		t.Fatalf("otherwise passing legacy sampled gate now fails: %+v", r)
	}
	view, _ := json.Marshal(r)
	if !strings.Contains(string(view), "not replayed: not classified") {
		t.Error("legacy report omits the exact non-replay label")
	}
	for _, b := range r.Bugs {
		if b.Replay != nil && (b.Replay.Required || b.Replay.Request != nil) {
			t.Errorf("invented replay obligation: %+v", b)
		}
	}
	calls, e := os.ReadFile(log)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(calls), "invariant-replay-v1") {
		t.Fatal("unclassified/hypothetical bug requested replay extraction")
	}
	t.Setenv("INVARIANT_GATE857_MISS_BUG", "1")
	if failed := run857(t, dir, tc, false); failed.Passed || failed.Bugs[0].Caught {
		t.Fatal("legacy model-strength failure no longer fails")
	}
	// An unclassified neighbor must not mask the mandatory current bug.
	dir, tc, _ = gateFixture857(t, "typescript", []string{"", "current"}, false)
	mixed := run857(t, dir, tc, false)
	if mixed.Passed || mixed.Bugs[1].Replay == nil || !mixed.Bugs[1].Replay.Required {
		t.Fatal("legacy neighbor masked current replay")
	}
	// Go continues to use its existing agreement/proof path.
	dir, tc, _ = gateFixture857(t, "go", []string{"current"}, false)
	goReport := run857(t, dir, tc, false)
	if !goReport.Passed || goReport.Agreement == nil || !goReport.Agreement.Passed || goReport.CodeAssurance() != "proved" {
		t.Fatalf("driver replay changed Go's gate: %+v", goReport)
	}
}

func TestLegacySampledExistingPythonStillPassesAndDoesNotReplay857(t *testing.T) {
	tc, log := pythongatetest.Fake(t)
	for _, explicit := range []bool{false, true} {
		f := pythongatetest.New(t, explicit, false)
		pythongatetest.Admit(t, f)
		r := pythongatetest.Run(t, f, tc)
		pythongatetest.Assert(t, f, r, true)
		b, _ := json.Marshal(r)
		if !strings.Contains(string(b), "not replayed: not classified") {
			t.Error("legacy Python gate omits classification status")
		}
		for _, bug := range r.Bugs {
			if bug.Replay != nil && (bug.Replay.Required || bug.Replay.Request != nil) {
				t.Fatal("legacy Python bug requested replay")
			}
		}
		t.Setenv("PA327_FAIL", "bug")
		failed := pythongatetest.Run(t, f, tc)
		if failed.Passed || failed.Bugs[0].Caught {
			t.Fatal("legacy Python model-strength refusal lost")
		}
		t.Setenv("PA327_FAIL", "")
	}
	b, e := os.ReadFile(log)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "invariant-replay-v1") {
		t.Fatal("legacy existing-code gate called replay extraction")
	}
	// The application gate runs model checks first and clears ModelOnly
	// afterward: that transition must activate, not drop, current obligations.
	f := pythongatetest.New(t, false, false)
	for i := range f.Project.Lock.Statements {
		if f.Project.Lock.Statements[i].Kind == project.Bug {
			f.Project.Lock.Statements[i].CodeBehavior = "current"
		}
	}
	pythongatetest.Save(t, f)
	current := pythongatetest.Run(t, f, tc)
	if current.Passed || current.ModelOnly || current.Bugs[0].Replay == nil || !current.Bugs[0].Replay.Required || current.Bugs[0].Replay.Passed || current.Bugs[0].Replay.Status == "pending" {
		t.Fatalf("existing application dropped its mandatory obligation: %+v", current)
	}
}
