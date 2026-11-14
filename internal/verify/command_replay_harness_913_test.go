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

const replayDriverTS913 = `import {writeFileSync} from 'node:fs';
class Counter {
  n = 9007199254740993n;
  async increment(delta: bigint) {
    if (process.env.MODE === 'exception') throw new Error('actual method failed');
    if (this.n > 9007199254740993n) return false;
    this.n += delta; return true;
  }
  read() { return {n:{kind:'integer',atom:String(this.n)},tags:{kind:'set',elements:[{kind:'string',atom:'beta'},{kind:'string',atom:''}]}}; }
  async close() {
    writeFileSync(process.env.CLOSED!, 'closed');
    if (process.env.MODE === 'cleanup') throw new Error('actual close failed');
  }
}
export async function invariantReplay(initial: any) {
  const app = new Counter();
  return {read:async()=>app.read(), bind:async(action:string,args:any[])=>process.env.MODE === 'null' ? null : ({target:app,method:'increment',args:args.map(v=>BigInt(v.atom)),refusal:{equals:false}}),close:()=>app.close()};
}
`

const replayDriverPY913 = `import os
class Counter:
    def __init__(self):
        self.n = 9007199254740993
    async def increment(self, delta):
        if os.environ['MODE'] == 'exception':
            raise RuntimeError('actual method failed')
        if self.n > 9007199254740993:
            return False
        self.n += delta
        return True
    async def read(self):
        return dict(n=dict(kind='integer',atom=str(self.n)),tags=dict(kind='set',elements=[dict(kind='string',atom='beta'),dict(kind='string',atom='')]))
    async def close(self):
        with open(os.environ['CLOSED'], 'w') as output:
            output.write('closed')
        if os.environ['MODE'] == 'cleanup':
            raise RuntimeError('actual close failed')
async def invariant_replay(initial):
    app = Counter()
    async def bind(action, args):
        if os.environ['MODE'] == 'null':
            return None
        return dict(target=app,method='increment',args=[int(v['atom']) for v in args],refusal=dict(equals=False))
    return dict(read=app.read,bind=bind,close=app.close)
`

// These run the embedded harnesses themselves, including awaitable callbacks,
// exact integers and semantic set equality. The integration lane separately
// runs the full command adapters with pinned TLC and runtime images.
func TestCommandReplayHarnessLifecycle913(t *testing.T) {
	for _, language := range []string{"typescript", "python"} {
		t.Run(language, func(t *testing.T) {
			program, driver, source := "node", "driver.ts", replayDriverTS913
			if language == "python" {
				program, driver, source = "python3", "driver.py", replayDriverPY913
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
			for _, mode := range []string{"healthy", "null", "exception", "cleanup"} {
				t.Run(mode, func(t *testing.T) {
					dir := t.TempDir()
					state := func(n string) tlc.ReplayState {
						return tlc.ReplayState{"n": {Kind: "integer", Atom: n}, "tags": {Kind: "set", Elements: []tlc.ReplayValue{{Kind: "string"}, {Kind: "string", Atom: "beta"}}}}
					}
					q := BugReplayRequest{Version: 1, ID: "request", InvocationID: "fresh", Path: tlc.ReplayPath{Offending: 1,
						States: []tlc.ReplayState{state("9007199254740993"), state("9007199254740994"), state("9007199254740995")},
						Steps:  []tlc.ReplayStep{{From: 0, To: 1, Action: "Increment", Args: []tlc.ReplayValue{{Kind: "integer", Atom: "1"}}}, {From: 1, To: 2, Action: "Increment", Args: []tlc.ReplayValue{{Kind: "integer", Atom: "1"}}, Bug: true}}}}
					raw, err := json.Marshal(q)
					if err != nil {
						t.Fatal(err)
					}
					name, harness := ReplayHarness(language)
					for file, contents := range map[string][]byte{name: harness, driver: []byte(source), "request.json": raw, "package.json": []byte(`{"type":"module"}`)} {
						if err := os.WriteFile(filepath.Join(dir, file), contents, 0600); err != nil {
							t.Fatal(err)
						}
					}
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, bin, filepath.Join(dir, name))
					cmd.Env = append(os.Environ(), "MODE="+mode, "CLOSED="+filepath.Join(dir, "closed"), "DRIVER="+filepath.Join(dir, driver),
						"INVARIANT_REPLAY_REQUEST="+filepath.Join(dir, "request.json"), "INVARIANT_REPLAY_RESULT="+filepath.Join(dir, "result.json"), "INVARIANT_REPLAY_OBSERVATION="+filepath.Join(dir, "observed.json"))
					output, err := cmd.CombinedOutput()
					if (err == nil) != (mode == "healthy") {
						t.Fatalf("process completion: %v\n%s", err, output)
					}
					if b, err := os.ReadFile(filepath.Join(dir, "closed")); err != nil || string(b) != "closed" {
						t.Fatalf("close was not called: %v\n%s", err, output)
					}
					if mode != "healthy" {
						return
					}
					var result BugReplayResult
					var observed BugReplayObservation
					if err := readReplayRecord(dir, "result.json", &result); err != nil {
						t.Fatal(err)
					}
					if err := readReplayRecord(dir, "observed.json", &observed); err != nil {
						t.Fatal(err)
					}
					if result.Outcome != "refused" || !observed.Started || !observed.Completed || observed.ExitCode != 0 || len(result.Calls) != 2 || !sameReplayCalls(result.Calls, observed.Calls) || !sameReplayState(result.Initial, q.Path.States[0]) {
						t.Fatalf("incomplete or lossy lifecycle: %+v %+v", result, observed)
					}
					for _, call := range result.Calls {
						if !call.Dispatched || !sameReplayState(call.After, q.Path.States[1]) {
							t.Fatalf("method was not invoked and read: %+v", call)
						}
					}
				})
			}
		})
	}
}
