//go:build integration

package verify_test

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

const forgedCompletionTS913 = `
if (process.env.INVARIANT_REPLAY_REQUEST) {
  const q = JSON.parse(replayReadFile(process.env.INVARIANT_REPLAY_REQUEST, 'utf8'));
  const calls = q.path.steps.map((s:any,index:number) => ({index,action:s.action,args:s.args,before:q.path.states[s.from],after:q.path.states[s.to],dispatched:true,outcome:'returned'}));
  calls.at(-1).after = calls.at(-1).before; calls.at(-1).outcome = 'refused';
  const result = {version:1,request_id:q.id,invocation_id:q.invocation_id,initial:q.path.states[0],calls,outcome:'refused'};
  const observed = {request_id:q.id,invocation_id:q.invocation_id,initial:result.initial,calls,started:true,completed:true,exit_code:0};
  try {
    writeFileSync(process.env.INVARIANT_REPLAY_RESULT!, JSON.stringify(result));
    writeFileSync(process.env.INVARIANT_REPLAY_OBSERVATION!, JSON.stringify(observed));
  } catch (err:any) { if (err.code !== 'EACCES') throw err; }
  writeFileSync(1, 'forged replay exit=0\n');
  process.exit(0);
}
`

const forgedCompletionPY913 = `
if os.environ.get('INVARIANT_REPLAY_REQUEST'):
    with open(os.environ['INVARIANT_REPLAY_REQUEST']) as source:
        q = json.load(source)
    calls = [dict(index=i,action=s['action'],args=s['args'],before=q['path']['states'][s['from']],after=q['path']['states'][s['to']],dispatched=True,outcome='returned') for i,s in enumerate(q['path']['steps'])]
    calls[-1]['after'], calls[-1]['outcome'] = calls[-1]['before'], 'refused'
    result = dict(version=1,request_id=q['id'],invocation_id=q['invocation_id'],initial=q['path']['states'][0],calls=calls,outcome='refused')
    observed = dict(request_id=q['id'],invocation_id=q['invocation_id'],initial=result['initial'],calls=calls,started=True,completed=True,exit_code=0)
    try:
        for name, record in [('INVARIANT_REPLAY_RESULT', result), ('INVARIANT_REPLAY_OBSERVATION', observed)]:
            with open(os.environ[name], 'w') as output:
                json.dump(record, output)
    except PermissionError:
        pass
    print('forged replay exit=0', flush=True)
    os._exit(0)
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
	root := t.TempDir()
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

// Real pinned TLC and runtime images run in the ordinary integration lane.
// The samples miss both paths, while each bug owns four actual method calls.
func TestCommandReplayPinned913(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	t.Cleanup(cancel)
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"typescript", "existing", "python"} {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			for _, mode := range []string{"fixed", "replay", "reuse", "both", "missing-interface", "skip-prefix", "null-binding", "exception", "cleanup-error", "forged-completion"} {
				t.Run(mode, func(t *testing.T) {
					dir := fixture913(t, route, mode)
					if mode == "exception" || mode == "cleanup-error" {
						appPath := filepath.Join(dir, "src", "delivery.ts")
						if route == "existing" {
							appPath = filepath.Join(dir, "..", "..", "src", "delivery.ts")
						}
						if route == "python" {
							appPath = filepath.Join(dir, "core", "delivery.py")
						}
						app, err := os.ReadFile(appPath)
						if err != nil {
							t.Fatal(err)
						}
						source := string(app)
						if route == "python" {
							method := "reload"
							if mode == "cleanup-error" {
								method = "close"
							}
							source = strings.Replace(source, "def "+method+"(self):", "def "+method+"(self):\n        raise RuntimeError('application "+mode+"')", 1)
						} else if mode == "exception" {
							source = strings.Replace(source, "reload() {", "reload() { throw new Error('application exception');", 1)
						} else {
							source = strings.Replace(source, "close() {}", "close() { throw new Error('application cleanup error'); }", 1)
						}
						if err := os.WriteFile(appPath, []byte(source), 0644); err != nil {
							t.Fatal(err)
						}
					}
					driver := "driver.ts"
					if route == "python" {
						driver = "driver.py"
					}
					path := filepath.Join(dir, driver)
					b, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					body := string(b)
					if route == "python" {
						body = "import invariant_replay, invariant_explore\n" + body
						// A network-isolated Linux container has no non-loopback IPv4 route.
						body = "assert not any(line.split()[0] != 'lo' for line in open('/proc/net/route').readlines()[1:]), 'network isolation lost'\n" + body
						switch mode {
						case "skip-prefix":
							body = strings.ReplaceAll(body, "Reload='reload'", "Reload='recover'")
						case "null-binding":
							body = strings.Replace(body, "return dict(target=app", "return None\n        return dict(target=app", 1)
						case "forged-completion":
							body += forgedCompletionPY913
						}
					} else {
						body = "import './invariant-replay.ts';\n" + body
						body = "import {networkInterfaces} from 'node:os';\nif (Object.values(networkInterfaces()).flat().some(v => v && !v.internal)) throw new Error('network isolation lost');\n" + body
						if route == "existing" {
							body = "import {existsSync} from 'node:fs';\nif (!existsSync('/deps/package-lock.json')) throw new Error('dependency image not executed');\n" + body
						}
						switch mode {
						case "skip-prefix":
							body = strings.ReplaceAll(body, "Reload:'reload'", "Reload:'recover'")
						case "null-binding":
							body = strings.Replace(body, "return {target:app", "return null; return {target:app", 1)
						case "forged-completion":
							body = "import {readFileSync as replayReadFile} from 'node:fs';\n" + body + forgedCompletionTS913
						}
					}
					if err = os.WriteFile(path, []byte(body), 0644); err != nil {
						t.Fatal(err)
					}
					r, err := verify.Run(ctx, dir, "", tc, verify.Options{TLCParallel: 2})
					if err != nil || r == nil {
						t.Fatalf("gate: %v", err)
					}
					switch mode {
					case "fixed", "replay", "reuse", "both":
						assertReport913(t, r, route, mode)
					default:
						if r.Passed || !r.Build.Passed || r.Conformance == nil || !r.Conformance.Passed {
							t.Fatalf("invalid replay masked or sample changed: %+v", r)
						}
						for _, bug := range r.Bugs {
							if !bug.Caught || bug.Replay == nil || bug.Replay.Passed {
								t.Fatalf("invalid execution passed: %+v", bug)
							}
							if mode == "forged-completion" && (!strings.Contains(bug.Replay.Message, "forged replay exit=0") || !strings.Contains(bug.Replay.Message, "replay harness authentication")) {
								t.Fatalf("forged exit zero did not reach the completion check: %+v", bug.Replay)
							}
						}
					}
				})
			}
		})
	}
}
