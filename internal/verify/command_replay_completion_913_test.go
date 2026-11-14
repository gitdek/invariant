package verify

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
)

// These drivers manufacture a complete, matching pair of records without
// dispatch or cleanup, then terminate successfully inside the real harness.
// They write the records themselves, or seal them with a key of their own,
// which the seal defends against. A driver that takes the harness's own seal
// from inside the process they share is tampering with the gate, which no
// in-process harness can rule out (docs/gate.md, Command driver interface).
const forgeReplayTS913 = `import {readFileSync} from 'node:fs';
import {createHmac} from 'node:crypto';
function forge() {
  const q = JSON.parse(readFileSync(process.env.INVARIANT_REPLAY_REQUEST!, 'utf8'));
  const calls = q.path.steps.map((s:any,index:number) => ({index, action:s.action, args:s.args, before:q.path.states[s.from], after:q.path.states[s.to], dispatched:true, outcome:'returned'}));
  calls.at(-1).after = calls.at(-1).before; calls.at(-1).outcome = 'refused';
  const result = {version:1, request_id:q.id, invocation_id:q.invocation_id, initial:q.path.states[0], calls, outcome:'refused'};
  const observed = {request_id:q.id, invocation_id:q.invocation_id, initial:result.initial, calls, started:true, completed:true, exit_code:0};
  const payload = JSON.stringify(result)+'\n'+JSON.stringify(observed)+'\n';
  writeFileSync(process.env.INVARIANT_REPLAY_RESULT!, JSON.stringify(result));
  writeFileSync(process.env.INVARIANT_REPLAY_OBSERVATION!, JSON.stringify(observed));
  if (process.env.FORGE_CHANNEL === '1') writeFileSync(3, createHmac('sha256', q.invocation_id).update(payload).digest('hex')+'\n'+payload);
  writeFileSync(1, 'forged replay exit=0\n');
  process.exit(0);
}
`

const forgeReplayPY913 = `import json, hmac
def forge():
    with open(os.environ['INVARIANT_REPLAY_REQUEST']) as source:
        q = json.load(source)
    calls = [dict(index=i, action=s['action'], args=s['args'], before=q['path']['states'][s['from']], after=q['path']['states'][s['to']], dispatched=True, outcome='returned') for i,s in enumerate(q['path']['steps'])]
    calls[-1]['after'], calls[-1]['outcome'] = calls[-1]['before'], 'refused'
    result = dict(version=1, request_id=q['id'], invocation_id=q['invocation_id'], initial=q['path']['states'][0], calls=calls, outcome='refused')
    observed = dict(request_id=q['id'], invocation_id=q['invocation_id'], initial=result['initial'], calls=calls, started=True, completed=True, exit_code=0)
    payload = (json.dumps(result)+'\n'+json.dumps(observed)+'\n').encode()
    for name, record in [('INVARIANT_REPLAY_RESULT', result), ('INVARIANT_REPLAY_OBSERVATION', observed)]:
        with open(os.environ[name], 'w') as output:
            json.dump(record, output)
    if os.environ.get('FORGE_CHANNEL') == '1':
        with os.fdopen(int(os.environ['INVARIANT_REPLAY_CHANNEL']), 'wb') as output:
            output.write(hmac.new(q['invocation_id'].encode(), payload, 'sha256').hexdigest().encode()+b'\n'+payload)
    print('forged replay exit=0', flush=True)
    os._exit(0)
`

func runReplayHarness913(t *testing.T, language, source string, q BugReplayRequest, env ...string) (dir, output string, err error) {
	t.Helper()
	program, driver := "node", "driver.ts"
	if language == "python" {
		program, driver = "python3", "driver.py"
	}
	bin, err := exec.LookPath(program)
	if err != nil {
		t.Skip(err)
	}
	if language == "typescript" {
		version, err := exec.Command(bin, "-p", "process.versions.node").Output()
		major, _ := strconv.Atoi(strings.Split(string(version), ".")[0])
		if err != nil || major < 24 {
			t.Skip("Node 24 is required for native TypeScript")
		}
	}
	dir = t.TempDir()
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	name, harness := ReplayHarness(language)
	for file, body := range map[string][]byte{name: harness, driver: []byte(source), "request.json": raw, "package.json": []byte(`{"type":"module"}`)} {
		if err := os.WriteFile(filepath.Join(dir, file), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, filepath.Join(dir, name))
	cmd.Env = append(os.Environ(), "MODE=healthy", "CLOSED="+filepath.Join(dir, "closed"), "DRIVER="+filepath.Join(dir, driver),
		"INVARIANT_REPLAY_REQUEST="+filepath.Join(dir, "request.json"), "INVARIANT_REPLAY_RESULT="+filepath.Join(dir, "result.json"), "INVARIANT_REPLAY_OBSERVATION="+filepath.Join(dir, "observed.json"))
	cmd.Env = append(cmd.Env, env...)
	b, err := cmd.CombinedOutput()
	return dir, string(b), err
}

func TestCommandReplayRejectsForgedCompletion913(t *testing.T) {
	state := func(n string) tlc.ReplayState {
		return tlc.ReplayState{"n": {Kind: "integer", Atom: n}, "tags": {Kind: "set", Elements: []tlc.ReplayValue{{Kind: "string"}, {Kind: "string", Atom: "beta"}}}}
	}
	q := BugReplayRequest{Version: 1, ID: "request", InvocationID: "public-identity", Path: tlc.ReplayPath{Offending: 1,
		States: []tlc.ReplayState{state("9007199254740993"), state("9007199254740994"), state("9007199254740995")},
		Steps: []tlc.ReplayStep{{From: 0, To: 1, Action: "Increment", Args: []tlc.ReplayValue{{Kind: "integer", Atom: "1"}}},
			{From: 1, To: 2, Action: "Increment", Args: []tlc.ReplayValue{{Kind: "integer", Atom: "1"}}, Bug: true}}}}
	for _, language := range []string{"typescript", "python"} {
		for _, stage := range []string{"import", "dispatch", "close", "public nonce"} {
			t.Run(language+"/"+stage, func(t *testing.T) {
				source := forgeReplayTS913 + replayDriverTS913
				if language == "python" {
					source = forgeReplayPY913 + replayDriverPY913
					switch stage {
					case "dispatch":
						source = strings.Replace(source, "async def increment(self, delta):", "async def increment(self, delta):\n        forge()", 1)
					case "close":
						source = strings.Replace(source, "async def close(self):", "async def close(self):\n        forge()", 1)
					default:
						source += "\nforge()\n"
					}
				} else {
					switch stage {
					case "dispatch":
						source = strings.Replace(source, "async increment(delta: bigint) {", "async increment(delta: bigint) { forge();", 1)
					case "close":
						source = strings.Replace(source, "async close() {", "async close() { forge();", 1)
					default:
						source += "\nforge();\n"
					}
				}
				channel := "0"
				if stage == "public nonce" {
					channel = "1"
				}
				dir, output, err := runReplayHarness913(t, language, source, q, "FORGE_CHANNEL="+channel)
				if err == nil || !strings.Contains(output, "forged replay exit=0") || !strings.Contains(output, "replay harness authentication") {
					t.Fatalf("forged completion was not rejected by supervisor: %v\n%s", err, output)
				}
				// The forged files really are valid contract records. Refusal is
				// due to missing authentic completion, not malformed fixtures.
				var result BugReplayResult
				var observed BugReplayObservation
				if err := readReplayRecord(dir, "result.json", &result); err != nil {
					t.Fatal(err)
				}
				if err := readReplayRecord(dir, "observed.json", &observed); err != nil {
					t.Fatal(err)
				}
				if len(result.Calls) != 2 || !sameReplayCalls(result.Calls, observed.Calls) || !observed.Completed {
					t.Fatal("forgery did not create complete matching records")
				}
			})
		}
	}
}

func TestCommandReplayFalsyCleanupException913(t *testing.T) {
	state := tlc.ReplayState{"label": {Kind: "string", Atom: "\ufffd"}}
	q := BugReplayRequest{Version: 1, ID: "request", InvocationID: "fresh", Path: tlc.ReplayPath{Offending: 0,
		States: []tlc.ReplayState{state, {"label": {Kind: "string", Atom: "forbidden"}}},
		Steps:  []tlc.ReplayStep{{From: 0, To: 1, Action: "Change", Bug: true}}}}
	for _, value := range []string{"undefined", "null", "false", "0", `''`} {
		t.Run(value, func(t *testing.T) {
			source := strings.Replace(unicodeReplayTS913, "close() { console.log('closed'); }", "close() { console.log('cleanup threw'); throw "+value+"; }", 1)
			_, output, err := runReplayHarness913(t, "typescript", source, q, "BEFORE=\ufffd", `AFTER="\ufffd"`)
			if err == nil || !strings.Contains(output, "cleanup threw") || !strings.Contains(output, "replay worker failed") {
				t.Fatalf("cleanup exception produced authenticated completion: %v\n%s", err, output)
			}
		})
	}
}
