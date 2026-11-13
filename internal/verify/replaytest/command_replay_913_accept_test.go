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
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// #913 is the complete command-adapter step of #789. This file fixes the
// driver interface and the boundary exercised by the Docker double below:
//
// * A TypeScript driver exports invariantReplay(initial), a Python driver
//   defines invariant_replay(initial). They construct fresh real application
//   state and return a session with read(), bind(action, typedArgs), close().
//   TypeScript callbacks and methods may return promises; Python awaitables
//   are awaited. initial and read() use the existing lossless ReplayState
//   representation. No JSON number conversion may lose exact integer atoms.
// * bind returns {target, method, args, refusal?: {equals: literal}} (a dict
//   in Python). The gate invokes target[method](...args), or getattr in
//   Python, itself, then reads the actual state. bind must not call or mutate
//   the application. A bug action binds to the same real method capable of
//   that bug, not to a synthetic mutation or a refusal callback. Null/None
//   bindings and missing methods fail. The optional refusal declaration
//   matches a normal primitive return value, never an exception. Without a
//   match the call returned normally; the model still checks its successor.
//   Every thrown exception is a failure in version 1. Review checks the
//   binding and read-back against the imported application, as with ordinary
//   conformance. This does not claim the authenticated existing-Python adapter.
// * Each required bug gets its own fresh session and invocation, from Init
//   through Offending inclusive, regardless of sampling choices. A driver
//   can suppress its normal sampling entry point during import by testing
//   INVARIANT_REPLAY_REQUEST; old drivers need no change when no current
//   obligation exists. The replay module is separate from invariant-explore
//   and invariant_explore; merely importing replay never implies Explores.
// * The gate stages request.json under a read-only /replay mount and fresh
//   embedded invariant-replay.ts / invariant_replay.py under /runtime:ro.
//   INVARIANT_REPLAY_REQUEST=/replay/request.json names one BugReplayRequest.
//   INVARIANT_REPLAY_RESULT=/out/replay-result.json and
//   INVARIANT_REPLAY_OBSERVATION=/out/replay-observation.json are distinct
//   gate-harness outputs, never successful records accepted from a driver or
//   public Run option. Each contains exactly one complete version-1 contract
//   record. The harness owns these records and observes actual invocation,
//   state reads and close; the host independently binds successful process
//   completion. Repeated keys/records, trailing data, substituted identities,
//   linked/absent output and incomplete lifecycle cannot supply evidence.
// * Replay commands retain DRIVER, image/dependency selection, network and
//   resource limits, and emit exactly one @@invariant replay=<exit> marker.
//   Sampling retains its existing markers and controls. No replay reuses
//   sampling's mutable runtime state or enters its traces/counts. Stage from
//   the same admitted source snapshot; keep owned recordings until checked
//   and propagate storage, close, process and cleanup failures on all exits.
//
// Required builder regression, in the NEW integration-tagged file
// internal/verify/command_replay_913_integration_test.go, uses these same
// fixture sources and assertions with real pinned tools, never fake913
// (copy the helpers into that package as needed): for each route run fixed,
// replay, reuse and both; assert the
// sample passes and the corresponding replay verdicts, four actual calls
// and fresh starts. Also run missing-interface, skip-prefix, null-binding,
// exception and cleanup-error driver variants through the actual harnesses.
// The tests run in the existing PR integration lane without a new opt-in or
// workflow. Use the pinned Node/Python/Java images and TLC jar; the existing
// TypeScript fixture's dependency-free lock requires no network packages.
// Verify network isolation and actual dependency-image execution, and retain
// the existing tsx-or-node selection at the command boundary. Preserve
// Python launcher/import-order/hash-seed and immutable dependency selection
// tests, plus the existing TypeScript imports-every-selection refusal.
// No ask3d source, network installation during a check, or verdict-only fake
// can substitute for these actual-runtime regressions.

const model913 = `---- MODULE DeliveryReplay ----
EXTENDS Naturals
CONSTANT Names
VARIABLE phase, deliveries, reused
vars == <<phase, deliveries, reused>>
Init == phase = 0 /\ deliveries = 0 /\ reused = FALSE
Deliver(id, name) == phase = 0 /\ id = 1 /\ name \in Names
  /\ phase' = 1 /\ deliveries' = 1 /\ UNCHANGED reused
Reload == phase = 1 /\ phase' = 2 /\ UNCHANGED <<deliveries, reused>>
Recover == phase = 2 /\ phase' = 3 /\ UNCHANGED <<deliveries, reused>>
Idle == phase = 3 /\ UNCHANGED vars
Next == (\E id \in {1}, name \in Names : Deliver(id, name)) \/ Reload \/ Recover \/ Idle
Spec == Init /\ [][Next]_vars
Safe == deliveries <= 1 /\ ~reused
ReplayTask(id, name) == phase = 3 /\ id = 1 /\ name \in Names
  /\ deliveries' = 2 /\ UNCHANGED <<phase, reused>>
ReuseName(id, name) == phase = 3 /\ id = 2 /\ name \in Names
  /\ deliveries' = 2 /\ reused' = TRUE /\ UNCHANGED phase
ReplayCompletedTask == \E id \in {1}, name \in Names : ReplayTask(id, name)
ReuseRecoveredName == \E id \in {2}, name \in Names : ReuseName(id, name)
====
`

// Both buggy variants persist completion and reserved names, then lose one
// during recovery. The application has no replay-specific methods. Both bug
// bindings call deliver; the driver reads the application's own counters.
const appTS913 = `export class DeliveryStore {
  phase = 0; deliveries = 0; reused = false;
  diskIDs = new Set<number>(); diskNames = new Set<string>();
  ids = new Set<number>(); names = new Set<string>();
  deliver(id: number, name: string) {
    if (this.ids.has(id) || this.names.has(name)) return false;
    this.reused = this.diskNames.has(name) && !this.diskIDs.has(id);
    this.diskIDs.add(id); this.diskNames.add(name);
    this.ids.add(id); this.names.add(name);
    this.deliveries++; if (this.phase === 0) this.phase = 1;
    return true;
  }
  reload() { this.ids.clear(); this.names.clear(); this.phase = 2; return true; }
  recover() {
    this.ids = __REPLAY_BUG__ ? new Set() : new Set(this.diskIDs);
    this.names = __REUSE_BUG__ ? new Set() : new Set(this.diskNames);
    // To repeat a completed identity its name must be allowed as that same
    // identity, while a different identity may not reuse the reserved name.
    this.phase = 3; return true;
  }
  read() { return {phase: this.phase, deliveries: this.deliveries, reused: this.reused}; }
  close() {}
}
`

// On recovery, name reservation is checked only for a different task ID.
// Forgetting the completed-ID set therefore exposes ReplayCompletedTask,
// independently of the reserved-name set used by ReuseRecoveredName.
func appSourceTS913(mode string) string {
	s := strings.ReplaceAll(appTS913, "__REPLAY_BUG__", fmt.Sprint(mode == "replay" || mode == "both"))
	s = strings.ReplaceAll(s, "__REUSE_BUG__", fmt.Sprint(mode == "reuse" || mode == "both"))
	return strings.Replace(s, "this.names.has(name)", "(this.names.has(name) && !this.diskIDs.has(id))", 1)
}

const appPY913 = `class DeliveryStore:
    def __init__(self):
        self.phase = self.deliveries = 0
        self.reused = False
        self.disk_ids, self.disk_names, self.ids, self.names = set(), set(), set(), set()
    def deliver(self, identity, name):
        if identity in self.ids or (name in self.names and identity not in self.disk_ids):
            return False
        self.reused = name in self.disk_names and identity not in self.disk_ids
        self.disk_ids.add(identity)
        self.disk_names.add(name)
        self.ids.add(identity)
        self.names.add(name)
        self.deliveries += 1
        if self.phase == 0:
            self.phase = 1
        return True
    def reload(self):
        self.ids.clear()
        self.names.clear()
        self.phase = 2
        return True
    def recover(self):
        self.ids = set() if __REPLAY_BUG__ else set(self.disk_ids)
        self.names = set() if __REUSE_BUG__ else set(self.disk_names)
        self.phase = 3
        return True
    def read(self):
        return dict(phase=self.phase, deliveries=self.deliveries, reused=self.reused)
    def close(self):
        pass
`

const driverTS913 = `import {writeFileSync} from 'node:fs';
import {DeliveryStore} from '__APP__';
function typed(s: any): any {
  return Object.fromEntries(Object.entries(s).map(([k,v]) => [k,
    typeof v === 'boolean' ? {kind:'bool',atom:v?'TRUE':'FALSE'} : {kind:'integer',atom:String(v)}]));
}
export function invariantReplay(initial: any) {
  const app = new DeliveryStore();
  return {
    read: () => typed(app.read()),
    bind(action: string, args: any[]) {
      const method = ({Deliver:'deliver', Reload:'reload', Recover:'recover', ReplayTask:'deliver', ReuseName:'deliver'} as any)[action];
      if (!method) throw new Error('missing operation '+action);
      return {target:app, method, args:args.map(v => v.kind==='integer' ? Number(v.atom) : v.atom), refusal:{equals:false}};
    },
    close: () => app.close()
  };
}
if (!process.env.INVARIANT_REPLAY_REQUEST) {
  // The sample's weights give reload/recover zero probability. It exercises
  // real code but cannot find either two-delivery path, for any fixed seed.
  const app = new DeliveryStore(); const initial = app.read(); app.deliver(1,'job');
  writeFileSync(process.env.INVARIANT_TRACES!, JSON.stringify({traces:[[initial,app.read()]]}));
}
`

const driverPY913 = `import json, os
from core.delivery import DeliveryStore
def typed(state):
    return {k: dict(kind='bool', atom='TRUE' if v else 'FALSE') if isinstance(v,bool) else dict(kind='integer',atom=str(v)) for k,v in state.items()}
def invariant_replay(initial):
    app = DeliveryStore()
    def bind(action, args):
        method = dict(Deliver='deliver', Reload='reload', Recover='recover', ReplayTask='deliver', ReuseName='deliver')[action]
        return dict(target=app, method=method, args=[int(v['atom']) if v['kind']=='integer' else v['atom'] for v in args], refusal=dict(equals=False))
    return dict(read=lambda: typed(app.read()), bind=bind, close=app.close)
if not os.environ.get('INVARIANT_REPLAY_REQUEST'):
    app = DeliveryStore()
    initial = app.read()
    app.deliver(1, 'job')
    with open(os.environ['INVARIANT_TRACES'], 'w') as output:
        json.dump(dict(traces=[[initial,app.read()]]),output)
`

type fixtureConfig913 struct{ Route, Mode string }

func write913(t *testing.T, root, name, value string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

// fixture913 does not install a double or select tools, so the integration
// regression can execute precisely the same project against actual tools.
func fixture913(t *testing.T, route, mode string) string {
	t.Helper()
	var root string
	if base := os.Getenv("INVARIANT_COMMAND913_FIXTURES"); base != "" && os.Getenv("INVARIANT_COMMAND913_DOUBLE") == "1" {
		var err error
		root, err = os.MkdirTemp(base, "project-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(root) })
	} else {
		root = t.TempDir()
	}
	dir := root
	language, driver, code := "typescript", "driver.ts", "src"
	if route == "python" {
		language, driver, code = "python", "driver.py", "core"
	}
	m := project.Manifest{Name: "delivery recovery replay", Module: ".invariant/specs/DeliveryReplay.tla", Language: language, Conformance: driver, Code: code}
	appImport := "./src/delivery.ts"
	if route == "existing" {
		dir = filepath.Join(root, "verification", "delivery")
		m.Code, m.Existing = ".", []string{"src/delivery.ts"}
		appImport = "../../src/delivery.ts"
	}
	if route != "python" {
		write913(t, root, "package.json", `{"name":"delivery-replay-fixture","version":"1.0.0","type":"module"}`)
		write913(t, root, "package-lock.json", `{"name":"delivery-replay-fixture","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"delivery-replay-fixture","version":"1.0.0"}}}`)
		write913(t, root, "src/delivery.ts", appSourceTS913(mode))
		write913(t, dir, driver, strings.ReplaceAll(driverTS913, "__APP__", appImport))
	} else {
		source := strings.ReplaceAll(appPY913, "__REPLAY_BUG__", map[bool]string{true: "True", false: "False"}[mode == "replay" || mode == "both"])
		source = strings.ReplaceAll(source, "__REUSE_BUG__", map[bool]string{true: "True", false: "False"}[mode == "reuse" || mode == "both"])
		write913(t, dir, "core/__init__.py", "")
		write913(t, dir, "core/delivery.py", source)
		write913(t, dir, driver, driverPY913)
	}
	if mode == "missing-interface" {
		b, err := os.ReadFile(filepath.Join(dir, driver))
		if err != nil {
			t.Fatal(err)
		}
		s := strings.ReplaceAll(string(b), "invariantReplay", "missingReplay")
		s = strings.ReplaceAll(s, "invariant_replay", "missing_replay")
		write913(t, dir, driver, s)
	}
	parsed, err := tla.Read(model913, nil)
	if err != nil {
		t.Fatal(err)
	}
	ss := []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "Delivery survives reload and recovery."},
		{Name: "Safe", Kind: project.Invariant, Says: "A task and its reserved name are delivered at most once."},
		{Name: "ReplayCompletedTask", Kind: project.Bug, Says: "A completed task is delivered again after recovery.", Expect: "Safe", CodeBehavior: "current"},
		{Name: "ReuseRecoveredName", Kind: project.Bug, Says: "A different task reuses a recovered reserved name.", Expect: "Safe", CodeBehavior: "current"},
	}
	for i := range ss {
		ss[i].SHA256, err = parsed.PinHash(ss[i].Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
	}
	lock := project.Lock{Bounds: map[string]string{"Names": `{"job"}`}, Statements: ss}
	lock.Ratified = &project.Ratification{By: "fixture", Issue: 913, Proposal: project.ProposalHash(lock.Bounds, lock.Statements)}
	for name, v := range map[string]any{".invariant/invariant.json": m, ".invariant/ratified.lock": lock, "fixture913.json": fixtureConfig913{route, mode}} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		write913(t, dir, name, string(b))
	}
	write913(t, dir, m.Module, model913)
	return dir
}

func init() {
	if os.Getenv("INVARIANT_COMMAND913_DOUBLE") == "1" {
		os.Exit(docker913(os.Args[1:]))
	}
}

func fake913(t *testing.T) (toolchain.Toolchain, string, string) {
	t.Helper()
	bin, events, tmp := t.TempDir(), t.TempDir(), t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	write913(t, bin, "docker", "#!/bin/sh\nexec '"+strings.ReplaceAll(exe, "'", "'\"'\"'")+"' \"$@\"\n")
	if err = os.Chmod(filepath.Join(bin, "docker"), 0755); err != nil {
		t.Fatal(err)
	}
	write913(t, bin, "tools.jar", "fixture jar")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INVARIANT_COMMAND913_DOUBLE", "1")
	t.Setenv("INVARIANT_COMMAND913_EVENTS", events)
	t.Setenv("INVARIANT_COMMAND913_FIXTURES", t.TempDir())
	t.Setenv("TMPDIR", tmp)
	return toolchain.Toolchain{JavaImage: "command913-java", TLCJar: filepath.Join(bin, "tools.jar"), NodeImage: "command913-node", PythonImage: "command913-python"}, events, tmp
}

func event913(kind string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	f, err := os.CreateTemp(os.Getenv("INVARIANT_COMMAND913_EVENTS"), kind+"-")
	if err != nil {
		panic(err)
	}
	if _, err = f.Write(b); err != nil {
		panic(err)
	}
	if err = f.Close(); err != nil {
		panic(err)
	}
}

func frame913(code int, body string) {
	fmt.Printf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
}
func pass913() int {
	frame913(2193, "Model checking completed. No error has been found.")
	frame913(2199, "5 states generated, 4 distinct states found, 0 states left on queue.")
	frame913(2194, "The depth of the complete state graph search is 4.")
	frame913(2186, "Finished.")
	return 0
}

func docker913(args []string) int {
	if len(args) == 0 {
		return 90
	}
	switch args[0] {
	case "image", "inspect":
		fmt.Println("linux/amd64")
		return 0
	case "info":
		fmt.Println(`{"NCPU":1,"MemTotal":8589934592}`)
		return 0
	case "rm":
		event913("removed", args)
		return 0
	case "run":
	default:
		panic("unexpected Docker operation: " + strings.Join(args, " "))
	}
	mounts, readonly, env := map[string]string{}, map[string]bool{}, map[string]string{}
	has := func(value string) bool {
		for _, a := range args {
			if a == value {
				return true
			}
		}
		return false
	}
	for i, a := range args[:len(args)-1] {
		if a == "-v" {
			parts := strings.Split(args[i+1], ":")
			mounts[parts[1]] = parts[0]
			readonly[parts[1]] = len(parts) > 2 && parts[2] == "ro"
		}
		if a == "-e" || a == "--env" {
			k, v, _ := strings.Cut(args[i+1], "=")
			env[k] = v
		}
	}
	if !has("--network") || !has("none") {
		panic("network boundary lost")
	}
	if has("tlc2.TLC") {
		module := strings.TrimSuffix(filepath.Base(args[len(args)-1]), ".tla")
		b, err := os.ReadFile(filepath.Join(mounts["/work"], module+".tla"))
		if err != nil {
			panic(err)
		}
		body := string(b)
		if strings.HasPrefix(module, "Bug_") {
			frame913(2110, "Invariant Safe is violated.")
			for i := 0; i < 5; i++ {
				phase, deliveries, reused := min(i, 3), min(i, 1), "FALSE"
				if i == 4 {
					deliveries = 2
					if strings.Contains(module, "ReuseRecoveredName") {
						reused = "TRUE"
					}
				}
				frame913(2217, fmt.Sprintf("%d: <opaque label>\n/\\ phase = %d\n/\\ deliveries = %d\n/\\ reused = %s", i+1, phase, deliveries, reused))
			}
			frame913(2186, "Finished.")
			return 12
		}
		if strings.Contains(body, "invariant-replay-v1") {
			action, id := "ReplayTask", 1
			if strings.Contains(body, "Next \\/ ReuseRecoveredName") {
				action, id = "ReuseName", 2
			}
			fmt.Print("<<\"invariant-replay-v1\", 0, {<<\"Deliver\", <<1, \"job\">>, FALSE>>}>>\n")
			fmt.Print("<<\"invariant-replay-v1\", 1, {<<\"Reload\", <<>>, FALSE>>}>>\n")
			fmt.Print("<<\"invariant-replay-v1\", 2, {<<\"Recover\", <<>>, FALSE>>}>>\n")
			fmt.Printf("<<\"invariant-replay-v1\", 3, {<<\"%s\", <<%d, \"job\">>, TRUE>>}>>\n", action, id)
		}
		if module == "Invariant_ReplayResult" {
			event913("alternative", body)
			if strings.Contains(body, "phase |-> 9") {
				frame913(2110, "Invariant Invariant_ReplayAllowed is violated.")
				frame913(2186, "Finished.")
				return 12
			}
		}
		return pass913()
	}
	if !has("2g") || !has("512") || env["HOME"] != "/tmp" || mounts["/src"] == "" || mounts["/out"] == "" {
		panic("sandbox limits lost")
	}
	if env["DRIVER"] == "" {
		panic("DRIVER lost")
	}
	configPath := filepath.Join(mounts["/src"], filepath.Dir(env["DRIVER"]), "fixture913.json")
	b, err := os.ReadFile(configPath)
	if err != nil {
		panic(err)
	}
	var c fixtureConfig913
	if err = json.Unmarshal(b, &c); err != nil {
		panic(err)
	}
	script := args[len(args)-1]
	if !strings.Contains(script, "exec 2>&1") {
		panic("ordered markers lost")
	}
	if c.Route == "existing" && !strings.Contains(strings.Join(args, " "), "invariant-deps:") {
		panic("lost locked Node dependencies")
	}
	if c.Route == "existing" && (!strings.Contains(script, "/deps/node_modules") || !strings.Contains(script, ".bin/tsx")) {
		panic("lost existing TypeScript loader selection")
	}
	if c.Route == "typescript" && !has("command913-node") {
		panic("lost Node image")
	}
	if c.Route == "python" && (!has("command913-python") || !has("linux/amd64") || env["PYTHONHASHSEED"] != "0" || !readonly["/runtime"] || !strings.Contains(script, "/usr/local/bin/python -S -s -P /runtime/invariant_python.py")) {
		panic("lost trusted Python startup")
	}
	request := env["INVARIANT_REPLAY_REQUEST"]
	if request == "" {
		if env["INVARIANT_TRACES"] != "/out/traces.json" || env["INVARIANT_RUNS"] != "1000" || env["INVARIANT_STEPS"] != "40" || env["INVARIANT_SEED"] != "1" {
			panic("changed sample controls")
		}
		trace := `{"traces":[[{"phase":0,"deliveries":0,"reused":false},{"phase":1,"deliveries":1,"reused":false}]]}`
		if err = os.WriteFile(filepath.Join(mounts["/out"], "traces.json"), []byte(trace), 0600); err != nil {
			panic(err)
		}
		event913("sample", env)
		if c.Route == "python" {
			fmt.Println("@@invariant compile=0")
		}
		if c.Route != "existing" {
			fmt.Println("@@invariant test=0")
		}
		fmt.Println("@@invariant conform=0")
		return 0
	}
	if request != "/replay/request.json" || !readonly["/replay"] || !readonly["/runtime"] {
		panic("replay request/harness not read-only")
	}
	name := "invariant-replay.ts"
	if c.Route == "python" {
		name = "invariant_replay.py"
	}
	if source, err := os.ReadFile(filepath.Join(mounts["/runtime"], name)); err != nil || len(source) == 0 {
		panic("missing fresh replay harness")
	}
	if !strings.Contains(script, "@@invariant replay=") {
		panic("replay marker missing from command")
	}
	if env["INVARIANT_REPLAY_RESULT"] != "/out/replay-result.json" || env["INVARIANT_REPLAY_OBSERVATION"] != "/out/replay-observation.json" {
		panic("replay outputs not separated")
	}
	b, err = os.ReadFile(filepath.Join(mounts["/replay"], "request.json"))
	if err != nil {
		panic(err)
	}
	var q verify.BugReplayRequest
	if err = json.Unmarshal(b, &q); err != nil {
		panic(err)
	}
	if err = q.Path.Validate(); err != nil {
		panic(err)
	}
	if q.Version != 1 || q.ID == "" || q.InvocationID == "" || q.Path.Offending != 3 || len(q.Path.Steps) != 4 {
		panic("wrong replay request")
	}
	event913("replay", q)
	if c.Mode == "cancel" || c.Mode == "timeout" {
		event913("ready", q.ID)
		time.Sleep(time.Minute)
		return 77
	}
	if c.Mode == "missing-interface" {
		fmt.Println("invariantReplay / invariant_replay is required for current bug replay\n@@invariant replay=1")
		return 0
	}
	if c.Mode == "missing" || (c.Mode == "omitted-second" && q.Bug.Name == "ReuseRecoveredName") {
		fmt.Println("@@invariant replay=0")
		return 0
	}
	result := verify.BugReplayResult{Version: 1, RequestID: q.ID, InvocationID: q.InvocationID, Outcome: "refused", Initial: q.Path.States[0]}
	for i, step := range q.Path.Steps {
		result.Calls = append(result.Calls, verify.BugReplayCall{Index: i, Action: step.Action, Args: step.Args, Before: q.Path.States[step.From], After: q.Path.States[step.To], Dispatched: true, Outcome: "returned"})
	}
	last := &result.Calls[len(result.Calls)-1]
	last.After, last.Outcome = last.Before, "refused"
	reproduces := c.Mode == "both" || c.Mode == "replay" && q.Bug.Name == "ReplayCompletedTask" || c.Mode == "reuse" && q.Bug.Name == "ReuseRecoveredName"
	if reproduces {
		last.After, last.Outcome, result.Outcome = q.Path.States[4], "returned", "complete"
	}
	if c.Mode == "alternative" {
		last.Outcome, result.Outcome = "returned", "complete"
	}
	mutateState := func(s any) map[string]json.RawMessage {
		raw, _ := json.Marshal(s)
		var m map[string]json.RawMessage
		json.Unmarshal(raw, &m)
		return m
	}
	switch c.Mode {
	case "skip-prefix":
		result.Calls = result.Calls[1:]
	case "null-binding":
		last.Dispatched = false
	case "exception":
		last.Error = "unexpected application exception"
		last.Outcome = "failed"
		result.Outcome = "failed"
	case "wrong-start":
		result.Initial = q.Path.States[1]
	case "wrong-args":
		result.Calls[0].Args = nil
	case "diverged-prefix":
		result.Calls[0].After = q.Path.States[2]
	case "stale":
		result.InvocationID = "from-another-run"
	case "illegal-alternative":
		m := mutateState(last.Before)
		m["phase"] = json.RawMessage(`{"kind":"integer","atom":"9"}`)
		raw, _ := json.Marshal(m)
		last.After = nil
		json.Unmarshal(raw, &last.After)
		last.Outcome, result.Outcome = "returned", "complete"
	}
	observed := verify.BugReplayObservation{RequestID: result.RequestID, InvocationID: result.InvocationID, Initial: result.Initial, Calls: result.Calls, Started: true, Completed: true, ExitCode: 0}
	if c.Mode == "cleanup-error" {
		observed.Completed = false
	}
	if c.Mode == "conflicting" {
		observed.Initial = q.Path.States[2]
	}
	for name, value := range map[string]any{"replay-result.json": result, "replay-observation.json": observed} {
		raw, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		if c.Mode == "truncated" {
			raw = raw[:len(raw)/2]
		}
		if c.Mode == "duplicate" {
			raw = append(raw, raw...)
		}
		if c.Mode == "duplicate-key" {
			raw = append([]byte(`{"version":99,`), raw[1:]...)
		}
		if c.Mode == "malformed" {
			raw = []byte(`{"initial":"not a state"}`)
		}
		if err = os.WriteFile(filepath.Join(mounts["/out"], name), raw, 0600); err != nil {
			panic(err)
		}
	}
	if c.Mode != "missing-marker" {
		fmt.Println("@@invariant replay=0")
	}
	if c.Mode == "duplicate-marker" {
		fmt.Println("@@invariant replay=0")
	}
	if c.Mode == "process-failure" {
		return 7
	}
	return 0
}

func run913(t *testing.T, dir string, tc toolchain.Toolchain) *verify.Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := verify.Run(ctx, dir, "", tc, verify.Options{TLCParallel: 2})
	if err != nil || r == nil {
		t.Fatalf("gate did not return evidence: %v %+v", err, r)
	}
	return r
}

func assertReport913(t *testing.T, r *verify.Report, route, mode string) {
	t.Helper()
	if !r.Design.Passed || !r.Build.Passed || r.Conformance == nil || !r.Conformance.Passed {
		t.Fatalf("sample/model must pass independently: %+v", r)
	}
	if r.Conformance.Exhaustive || !r.Sampled() || r.Conformance.Runs != 1 || r.Conformance.Steps != 1 || r.Conformance.States != 2 || r.Conformance.Transitions != 1 {
		t.Fatalf("replays polluted sample counts or assurance: %+v", r.Conformance)
	}
	wantPass := mode == "fixed" || mode == "alternative"
	if r.Passed != wantPass {
		t.Fatalf("%s/%s: gate passed=%v, want %v; bugs=%+v", route, mode, r.Passed, wantPass, r.Bugs)
	}
	if len(r.Bugs) != 2 {
		t.Fatalf("lost independent bugs: %+v", r.Bugs)
	}
	ids := map[string]bool{}
	for _, b := range r.Bugs {
		e := b.Replay
		if !b.Caught || e == nil || !e.Required || e.Request == nil || e.Result == nil || e.Observation == nil {
			t.Fatalf("%s missing executed replay alongside model catch: %+v", b.Name, e)
		}
		q := e.Request
		if ids[q.InvocationID] || q.InvocationID == "" {
			t.Fatal("bugs reused an invocation")
		}
		ids[q.InvocationID] = true
		if q.Path.Offending != 3 || len(e.Result.Calls) != 4 || !e.Observation.Started || !e.Observation.Completed || e.Observation.ExitCode != 0 {
			t.Fatalf("incomplete ordered replay: %+v", e)
		}
		for i, action := range []string{"Deliver", "Reload", "Recover"} {
			if e.Result.Calls[i].Action != action || !e.Result.Calls[i].Dispatched {
				t.Fatalf("prefix not actually dispatched: %+v", e.Result.Calls)
			}
		}
		bad := mode == "both" || mode == "replay" && b.Name == "ReplayCompletedTask" || mode == "reuse" && b.Name == "ReuseRecoveredName"
		if e.Passed == bad || bad && e.Status != "reproduced" || !bad && !b.ReplayStatus().Replayed {
			t.Fatalf("%s independently wrong verdict: %+v", b.Name, e)
		}
		if bad {
			text := verify.Feedback(r) + "\n" + receipt.Markdown(r)
			for _, want := range []string{b.Name, e.Result.Calls[3].Action, "deliveries", "2"} {
				if !strings.Contains(text, want) {
					t.Errorf("reproduction diagnostic omits %q: %s", want, text)
				}
			}
		}
	}
	if route == "existing" && (len(r.Existing) != 1 || r.Existing[0].SHA256 == "") {
		t.Fatal("lost selected existing source hashes")
	}
	before := verify.Fingerprint(*r)
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var round verify.Report
	if err = json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if verify.Fingerprint(round) != before {
		t.Fatal("replay evidence fingerprint changed across JSON round trip")
	}
	for i := range round.Bugs {
		e := round.Bugs[i].Replay
		e.Request.InvocationID = "fresh"
		e.Result.InvocationID = "fresh"
		e.Observation.InvocationID = "fresh"
	}
	if verify.Fingerprint(round) != before {
		t.Fatal("private invocation identity changed stable fingerprint")
	}
	round.Bugs[0].Replay.Result.Calls[3].Dispatched = false
	if verify.Fingerprint(round) == before {
		t.Fatal("fingerprint omitted actual replay results")
	}
}

func clean913(t *testing.T, tmp string) {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("owned replay or sample storage leaked: %v", entries)
	}
}

func TestCommandReplayFindsBothDeliveryBugs913(t *testing.T) {
	tc, _, tmp := fake913(t)
	for _, route := range []string{"typescript", "existing", "python"} {
		t.Run(route, func(t *testing.T) {
			for _, mode := range []string{"fixed", "replay", "reuse", "both", "alternative"} {
				t.Run(mode, func(t *testing.T) {
					dir := fixture913(t, route, mode)
					r := run913(t, dir, tc)
					assertReport913(t, r, route, mode)
					clean913(t, tmp)
				})
			}
		})
	}
}

func TestCommandReplayRejectsIncompleteExecution913(t *testing.T) {
	tc, _, tmp := fake913(t)
	// A refusal-only suite would already pass the old unavailable gate.
	assertReport913(t, run913(t, fixture913(t, "typescript", "fixed"), tc), "typescript", "fixed")
	for _, route := range []string{"typescript", "existing", "python"} {
		for _, mode := range []string{"missing-interface", "omitted-second", "skip-prefix", "null-binding", "wrong-start", "wrong-args", "diverged-prefix", "exception", "missing", "truncated", "duplicate", "duplicate-key", "malformed", "stale", "conflicting", "illegal-alternative", "process-failure", "missing-marker", "duplicate-marker", "cleanup-error"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				r, err := verify.Run(ctx, fixture913(t, route, mode), "", tc)
				if err == nil && (r == nil || r.Passed) {
					t.Fatalf("incomplete or invalid replay passed: %+v", r)
				}
				if r != nil && mode == "omitted-second" {
					if len(r.Bugs) != 2 || r.Bugs[0].Replay == nil || !r.Bugs[0].Replay.Passed || r.Bugs[1].Replay == nil || r.Bugs[1].Replay.Passed {
						t.Fatalf("one result masked the absent second obligation: %+v", r.Bugs)
					}
				}
				if r != nil && err == nil && mode == "missing-interface" {
					message := verify.Feedback(r)
					if !strings.Contains(message, "invariantReplay") && !strings.Contains(message, "invariant_replay") {
						t.Fatalf("missing interface diagnostic not actionable: %s", message)
					}
				}
				clean913(t, tmp)
			})
		}
	}
}

func TestCommandReplayCancellationAndCompatibility913(t *testing.T) {
	tc, events, tmp := fake913(t)
	first := run913(t, fixture913(t, "typescript", "fixed"), tc)
	assertReport913(t, first, "typescript", "fixed")
	second := run913(t, fixture913(t, "typescript", "fixed"), tc)
	if verify.Fingerprint(*first) != verify.Fingerprint(*second) {
		t.Fatal("fresh identical replay runs have different stable evidence")
	}
	if first.Bugs[0].Replay.Request.InvocationID == second.Bugs[0].Replay.Request.InvocationID {
		t.Fatal("reused stale invocation")
	}
	for _, route := range []string{"typescript", "existing", "python"} {
		for _, mode := range []string{"cancel", "timeout"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				entries, _ := os.ReadDir(events)
				for _, e := range entries {
					if strings.HasPrefix(e.Name(), "ready-") {
						os.Remove(filepath.Join(events, e.Name()))
					}
				}
				limit := 20 * time.Second
				if mode == "timeout" {
					limit = 5 * time.Second
				}
				ctx, cancel := context.WithTimeout(context.Background(), limit)
				defer cancel()
				done := make(chan struct{})
				go func() {
					defer close(done)
					for ctx.Err() == nil {
						entries, _ := os.ReadDir(events)
						for _, e := range entries {
							if strings.HasPrefix(e.Name(), "ready-") {
								if mode == "cancel" {
									cancel()
								}
								return
							}
						}
						time.Sleep(time.Millisecond)
					}
				}()
				r, err := verify.Run(ctx, fixture913(t, route, mode), "", tc)
				cancel()
				<-done
				ready, _ := filepath.Glob(filepath.Join(events, "ready-*"))
				if len(ready) == 0 || err == nil || r != nil && r.Passed {
					t.Fatalf("replay never reached cancellation or passed it: %+v %v", r, err)
				}
				clean913(t, tmp)
			})
		}
		dir := fixture913(t, route, "missing-interface")
		p, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		for i := range p.Lock.Statements {
			p.Lock.Statements[i].CodeBehavior = ""
		}
		p.Lock.Ratified.Proposal = project.ProposalHash(p.Lock.Bounds, p.Lock.Statements)
		raw, _ := json.Marshal(p.Lock)
		write913(t, dir, ".invariant/ratified.lock", string(raw))
		if r := run913(t, dir, tc); !r.Passed {
			t.Fatalf("legacy driver now requires replay: %+v", r.Bugs)
		}
		if verify.Explores(p) {
			t.Fatal("replay driver incorrectly inferred exhaustive")
		}
		model, err := verify.RunModel(context.Background(), fixture913(t, route, "missing-interface"), tc)
		if err != nil || !model.Passed || !model.ModelOnly {
			t.Fatalf("model-only changed: %+v %v", model, err)
		}
		for _, bug := range model.Bugs {
			if bug.Replay == nil || bug.Replay.Status != "pending" || bug.Replay.Result != nil {
				t.Fatalf("model-only executed or lost replay: %+v", bug)
			}
		}
	}
	// The existing import obligation still applies even if a replay interface
	// claims it can dispatch the operation.
	dir := fixture913(t, "existing", "fixed")
	write913(t, dir, "driver.ts", "export const invariantReplay = () => ({});\n")
	r := run913(t, dir, tc)
	if r.Passed || r.Build.Passed || !strings.Contains(r.Build.Output, "doesn't import") {
		t.Fatalf("existing-source import refusal lost: %+v", r.Build)
	}
	// Compatibility pin: no production test may be weakened to accommodate
	// replay. The old gate569 Docker fake retains DRIVER, fixed 1000/40/1,
	// trace path, mounts/images, Python startup and original marker assertions.
	clean913(t, tmp)
}
