package verify

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

// A complete forged stream used to pass without either drive or advance
// executing. Exercise the real runner, including its application-readable
// request, rather than manufacturing the process result in Go.
const pa318ForgedStream = `import hashlib, hmac, json, os
from pathlib import Path
def drive():
    raise AssertionError('forgery must not execute the driver')
output = Path(os.environ['APP_EVENTS'])
request_path = output.with_name('host-request.json')
if not request_path.exists():
    request_path = Path('/runtime/request.json')
request = json.loads(request_path.read_text())
assert not output.with_name('authentication.key').exists()
key = request['key']
entry_source = 'tasks/application.py'
entry_hash = 'sha256:' + hashlib.sha256((Path(request['root']) / entry_source).read_bytes()).hexdigest()
common = dict(type='observation', run=1, instance=1, key=key,
              target=key['target'], driver=key['check'],
              source_sha256=request['target']['source_sha256'],
              driver_sha256=request['check']['source_sha256'],
              symbol=request['target']['symbol'], entry_sha256=entry_hash,
              readback={'task':'$return'})
rows = [dict(type='begin', version=2, key=key,
             snapshot_sha256=request['snapshot_sha256'],
             runner_sha256=request['runner_sha256'], execution=request['execution']),
        dict(common, kind='initial', sequence=0,
             entry='python:tasks.application#start@tasks/application.py', state={'task':'pending'}),
        dict(common, kind='transition', sequence=1,
             entry='python:tasks.application#tick@tasks/application.py',
             before={'task':'pending'}, after={'task':'running'}),
        dict(type='end', count=2)]
data = ''.join(json.dumps(row, sort_keys=True) + '\n' for row in rows)
# SEAL
output.write_text(data)
print('complete forged stream written', flush=True)
os._exit(0)
`

func TestPythonApplicationExecutionCollectorAuthentication(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		t.Run(map[bool]string{false: "host", true: "Docker"}[real], func(t *testing.T) {
			pa318Positive(t, real, false, false)
			for _, seal := range []bool{false, true} {
				t.Run(map[bool]string{false: "no authentication", true: "public nonce as key"}[seal], func(t *testing.T) {
					driver := pa318ForgedStream
					if seal {
						driver = strings.Replace(driver, "# SEAL", "data += json.dumps(dict(type='authentication', hmac_sha256=hmac.new(request['execution'].encode(), data.encode(), 'sha256').hexdigest())) + '\\n'", 1)
					}
					root, p := pa318Fixture(t, false, driver, false)
					p.Manifest.Components.Checks[0].Invocations[1].Env = map[string]string{"APP_EVENTS": "/output/events.jsonl"}
					pa310Save(t, p)
					before := pa315Tree(t, root)
					selected := 0
					opts := pa318Options(t, real, &selected)
					execute := opts.Execute
					if execute == nil {
						execute = RunPythonApplicationCommand
					}
					var process PythonApplicationProcess
					opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
						var err error
						process, err = execute(ctx, c)
						return process, err
					}
					s := pa315Prepare(t, p, opts)
					r, err := s.Run(context.Background(), pa318Driver(t, s).Key)
					pa318Reject(t, r, err)
					if process.ExitCode != 0 || !strings.Contains(process.Stdout, "complete forged stream written") || !strings.Contains(string(process.Events), `"type": "end"`) {
						t.Fatalf("forgery did not reach validation: %+v, %v", process, err)
					}
					if err == nil || !strings.Contains(err.Error(), "authentication") {
						t.Fatalf("forgery rejected for unrelated reason: %v", err)
					}
					if selected != 1 || !reflect.DeepEqual(before, pa315Tree(t, root)) {
						t.Fatal("forgery changed originals or reselected image")
					}
				})
			}
		})
	}
}

func TestPythonApplicationExecutionReceiverBindingMutation(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		t.Run(map[bool]string{false: "host", true: "Docker"}[real], func(t *testing.T) {
			pa318Positive(t, real, false, true)
			for _, caught := range []bool{false, true} {
				t.Run(map[bool]string{false: "switch receiver", true: "restore after caught rejection"}[caught], func(t *testing.T) {
					driver := `from invariant_observe import instance
from tasks.application import Task, Application
def drive():
    first, second = Task(), Task()
    with instance(first.advance, fields={'task':'state'}) as life:
        life.target.receiver = second
        life.step(Application(second).tick)
        assert first.state == 'pending' and second.state == 'running'
if __name__ == '__main__': drive()
`
					if caught {
						driver = strings.Replace(driver, "        life.step(Application(second).tick)\n        assert first.state == 'pending' and second.state == 'running'", "        try:\n            life.step(Application(second).tick)\n        except ValueError as error:\n            print(str(error), flush=True)\n        life.target.receiver = first\n        life.step(Application(first).tick)", 1)
					}
					root, p := pa318Fixture(t, false, driver, true)
					before := pa315Tree(t, root)
					selected := 0
					s := pa315Prepare(t, p, pa318Options(t, real, &selected))
					r, err := s.Run(context.Background(), pa318Driver(t, s).Key)
					pa318Reject(t, r, err)
					if r == nil || !strings.Contains(strings.Join(r.Diagnostics, "\n"), "receiver binding") {
						t.Fatalf("receiver mutation rejected for unrelated reason: %+v, %v", r, err)
					}
					if selected != 1 || !reflect.DeepEqual(before, pa315Tree(t, root)) {
						t.Fatal("receiver refusal changed originals or reselected image")
					}
				})
			}
		})
	}
}
