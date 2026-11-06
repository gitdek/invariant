package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNodeParentAuthentication(t *testing.T) {
	in, events, out := nodeEvidenceFixture(t, "jest", "passed")
	out.Events = encodeNodeEvents(t, events)
	for _, mode := range []string{"no key", "another invocation", "unsigned finish", "changed terminal", "worker claims parent"} {
		t.Run(mode, func(t *testing.T) {
			key, changed := nodeEvidenceKey, out
			switch mode {
			case "no key":
				key = nil
			case "another invocation":
				key = bytes.Repeat([]byte{38}, 32)
			case "unsigned finish":
				last := bytes.LastIndex(bytes.TrimSuffix(out.Events, []byte("\n")), []byte("\n")) + 1
				var b bytes.Buffer
				b.Write(out.Events[:last])
				if err := json.NewEncoder(&b).Encode(events[len(events)-1]); err != nil {
					t.Fatal(err)
				}
				changed.Events = b.Bytes()
			case "changed terminal":
				changed.Events = bytes.ReplaceAll(out.Events, []byte(`"Code":0`), []byte(`"Code":1`))
			case "worker claims parent":
				changed.Events = bytes.Replace(out.Events, []byte(`"Actor":"worker"`), []byte(`"Actor":"parent"`), 1)
			}
			if evidence, err := collectNodeAcceptance(in, changed, key); err == nil || evidence.Complete {
				t.Fatalf("accepted forged authority: %v %+v", err, evidence)
			}
		})
	}
}

// Exercise the real transport and producer without requiring installed test
// frameworks. The imported production module knows the entire fresh binding
// and can write to the inherited pipe, but cannot complete the parent lifecycle.
func TestNodeImportedProductionCannotForgeLifecycle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("local Node is unavailable; pinned-image import attack coverage is opt-in")
	}
	for _, attack := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "forged import"}[attack], func(t *testing.T) {
			root := t.TempDir()
			in, _, _ := nodeEvidenceFixture(t, "jest", "passed")
			input, err := json.Marshal(map[string]any{"Binding": in.Binding, "File": "ready.test.ts"})
			if err != nil {
				t.Fatal(err)
			}
			product := "const crypto=require('node:crypto');crypto.Hmac.prototype.update=crypto.Hmac.prototype.digest=()=>{throw Error('candidate replaced signer')};module.exports=7;\n"
			if attack {
				product = nodeForgedLifecycleSource(false)
			}
			runner := `const common=require('./node_runner_common.cjs');
const input=JSON.parse(process.argv[2]);
const endpoint=process.env.INVARIANT_NODE_EVENTS;
delete process.env.INVARIANT_NODE_EVENTS;
const session=common.open(input,'parent',endpoint);
session.emit('begin');
if(require('./product.cjs')!==7)throw Error('wrong production value');
const worker=common.open(input,'worker',endpoint), observation=common.observer(worker,null);
observation.collection([{title:input.Binding.Selector}]);
observation.start();observation.end();observation.finish(true);
worker.close();
session.emit('session_end',{Code:0,Outcome:'passed'});session.emit('finish',{Code:0});session.close();
`
			for name, data := range map[string][]byte{"transport.cjs": dockerNodeProcessHelper, "node_runner_common.cjs": nodeRunnerCommon,
				"runner.cjs": []byte(runner), "product.cjs": []byte(product), "ready.test.ts": []byte("pinned")} {
				if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			read, control, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			defer control.Close()
			if _, err := control.Write(nodeEvidenceKey); err != nil {
				t.Fatal(err)
			}
			p := &dockerRepositoryProcess{output: RepositoryProcessOutput{ExitCode: -1}}
			command := exec.CommandContext(ctx, node, filepath.Join(root, "transport.cjs"), nodeEventEnv, node, filepath.Join(root, "runner.cjs"), string(input))
			var diagnostic strings.Builder
			command.Dir, command.Stdin, command.Stdout, command.Stderr = root, read, p, &diagnostic
			command.WaitDelay = time.Second
			if err := command.Run(); err != nil {
				t.Fatalf("Node transport failed: %v %s", err, diagnostic.String())
			}
			out := p.Snapshot()
			if p.err != nil || !out.Exited || !out.Drained || out.ExitCode != 0 {
				t.Fatalf("attack must reach a normal zero exit: %v %+v", p.err, out)
			}
			if _, err := os.Stat(filepath.Join(root, "events")); !os.IsNotExist(err) {
				t.Fatal("workload can reopen the named evidence FIFO")
			}
			evidence, err := collectNodeAcceptance(in, out, nodeEvidenceKey)
			if attack {
				if !bytes.Contains(out.Stdout, []byte("FORGERY_ATTEMPTED633")) || err == nil || evidence.Complete {
					t.Fatalf("import forgery supplied completion: %v %+v %+v", err, evidence, out)
				}
			} else if err != nil || !evidence.Complete {
				t.Fatalf("normal authenticated lifecycle refused: %v %+v", err, evidence)
			}
		})
	}
}

// The forgery runs in the controller when the local transport test requires it
// directly, and in an isolated framework worker when a real test imports it.
// The controller has its invocation in argv and its event pipe on fd 3. A
// worker has neither: its fd 3 is the framework's IPC channel. It finds the
// fresh binding and the controller's event endpoint in its initial
// environment instead, since deleting them from process.env leaves
// /proc/self/environ unchanged. A correct value means only the forgery itself
// can keep the run from passing.
func nodeForgedLifecycleSource(typescript bool) string {
	imports := "const fs=require('node:fs'), path=require('node:path');\n"
	if typescript {
		imports = "import * as fs from 'node:fs';import * as path from 'node:path';export const value=7;\n"
	}
	return imports + `const isInvocation=a=>a.startsWith('{"Binding":');
const environ=fs.existsSync('/proc/self/environ')?fs.readFileSync('/proc/self/environ','utf8').split('\0'):[];
const initial=name=>{const entry=environ.find(e=>e.startsWith(name+'='));return entry===undefined?'':entry.slice(name.length+1);};
const controller=process.argv.some(isInvocation);
const raw=controller?process.argv.find(isInvocation):initial('INVARIANT_NODE_WORKER');
if(!raw)throw Error('fresh invocation not visible');
const binding=JSON.parse(raw).Binding;
const workerEndpoint=initial('INVARIANT_NODE_ENDPOINT');
if(!controller&&!workerEndpoint)throw Error('worker evidence endpoint not visible');
const records=[
{Actor:'worker',Seq:1,Event:'collection',IDs:[binding.Selector]},
{Actor:'worker',Seq:2,Event:'body_start'},
{Actor:'worker',Seq:3,Event:'body_end',Outcome:'passed'},
{Actor:'worker',Seq:4,Event:'hooks',Outcome:'passed'},
{Actor:'worker',Seq:5,Event:'suite_end',Outcome:'passed'},
{Actor:'parent',Seq:2,Event:'session_end',Code:0,Outcome:'passed'},
{Actor:'parent',Seq:3,Event:'finish',Code:0}];
const forged=records.map(r=>JSON.stringify({Binding:binding,...r})+'\n').join('');
// First try the original named endpoint. Even after unlinking, inherited
// descriptor writes must not be able to impersonate the parent observer.
function endpoint(dir){for(const e of fs.readdirSync(dir,{withFileTypes:true})){
const p=path.join(dir,e.name);if(e.isDirectory()){const found=endpoint(p);if(found)return found;}
else if(e.name==='events'&&fs.statSync(p).isFIFO())return p;}return '';}
const named=fs.existsSync('/invariant')?endpoint('/invariant'):endpoint(process.cwd());
if(named)fs.writeFileSync(named,forged);else if(controller)fs.writeSync(3,forged);else fs.writeFileSync(workerEndpoint,forged);
// Frameworks buffer a worker's console, so write the marker to stdout itself.
fs.writeSync(1,'FORGERY_ATTEMPTED633\n');
if(controller)process.exit(0);
`
}

func TestNodeOfflineImportedEvidenceForgery(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	for _, framework := range []string{"vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			f := fixture633(t, tools, nodeCases633(framework), nil, nil)
			check633(t, f, true)
			fix633(t, f)
			pass633(t, f)
			write468(t, f.candidate, "web/product.ts", nodeForgedLifecycleSource(true))
			r, _ := f.sandbox.Run(t.Context(), f.candidate, f.plan)
			if r.Passed() {
				t.Fatal("imported production manufactured a complete passing lifecycle")
			}
			out := f.runner.streams[len(f.runner.streams)-1]
			if !bytes.Contains(out.Stdout, []byte("FORGERY_ATTEMPTED633")) {
				t.Fatalf("fixture did not attempt its imported forgery: %s\n%s\n%s", out.Stdout, out.Stderr, r.Summary())
			}
			v := result617(t, r, f.plan.Tests[0])
			if v.Kind == "test" && v.Status == "passed" {
				t.Fatal("forged pipe records survived as a passing selector")
			}
		})
	}
}

// jest-worker resends a request to a fresh worker when its worker exits
// nonzero. A production module that crashes the first worker before
// collection and behaves on the retry must not pass behind the replacement's
// complete lifecycle: the controller vetoes any worker death.
func TestNodeOfflineJestWorkerRetryCannotHideCrash(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	f := fixture633(t, tools, nodeCases633("jest"), nil, nil)
	check633(t, f, true)
	fix633(t, f)
	pass633(t, f)
	write468(t, f.candidate, "web/product.ts", "import * as fs from 'node:fs';\nexport const value = 7;\n"+
		"if (!fs.existsSync('worker-crash633')) { fs.writeFileSync('worker-crash633', ''); fs.writeSync(1, 'WORKER_CRASHED633\\n'); process.exit(3); }\n")
	r, _ := f.sandbox.Run(t.Context(), f.candidate, f.plan)
	out := f.runner.streams[len(f.runner.streams)-1]
	if !bytes.Contains(out.Stdout, []byte("WORKER_CRASHED633")) {
		t.Fatalf("fixture did not crash its first worker: %s\n%s\n%s", out.Stdout, out.Stderr, r.Summary())
	}
	if r.Passed() {
		t.Fatal("a replacement worker hid the first worker's death")
	}
	if v := result617(t, r, f.plan.Tests[0]); v.Kind == "test" && v.Status == "passed" {
		t.Fatalf("worker death survived as a passing selector: %+v", v)
	}
	if !bytes.Contains(out.Events, []byte("Jest worker died, restarted or was killed")) {
		t.Fatalf("the controller did not veto the worker's death: %s", out.Events)
	}
}

func TestNodeOfflineProposedAPIRegistrarBindings(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	for _, framework := range []string{"vitest", "jest"} {
		module := "vitest"
		if framework == "jest" {
			module = "@jest/globals"
		}
		for _, role := range []string{"test", "it", "describe", "expect", "shadowed test", "shadowed API"} {
			t.Run(framework+"/"+role, func(t *testing.T) {
				prefix := "import {future} from './product';\n"
				body := "test('selected',()=>expect(future()).toBe(7));\n"
				selector := "selected"
				extra := map[string]string{"web/noop.ts": "export function test(_name:string,_body:()=>void){};export const it=test,describe=test;export function expect(_value:unknown){return {toBe(_want:unknown){}}};\n"}
				switch role {
				case "test", "it":
					prefix += "import {expect} from '" + module + "';import {" + role + "} from './noop';\n"
					body = role + "('selected',()=>expect(future()).toBe(7));\n"
				case "describe":
					prefix += "import {test,expect} from '" + module + "';import {describe} from './noop';\n"
					body = "describe('outer',()=>{" + body + "});\n"
					selector = "outer selected"
				case "expect":
					prefix += "import {test} from '" + module + "';import {expect} from './noop';\n"
				case "shadowed test":
					prefix += "import {test,describe,expect} from '" + module + "';\n"
					body = "describe('outer',function test(..._args:unknown[]){" + body + "});\n"
					selector = "outer selected"
				case "shadowed API":
					prefix += "import {test,expect} from '" + module + "';\n"
					body = "test('selected',function future(){expect(future()).toBe(7)});\n"
				}
				f := fixture633(t, tools, []nativeCase633{{framework, "web", "future.test.ts", selector, prefix + body}}, extra, nil)
				c := check633(t, f, false)
				v := result617(t, c.Run, f.plan.Tests[0])
				if v.Status == "failed" && (v.Kind == "test" || v.Kind == "compile") {
					t.Fatalf("non-framework binding supplied API attribution: %+v", v)
				}
			})
		}
	}
}
