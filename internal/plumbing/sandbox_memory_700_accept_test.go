package plumbing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Report contract added by #700, alongside the existing fields:
// working_memory_peak = max(anon + shmem) over individual memory.stat reads;
// memory_samples counts those reads, sample_interval_seconds is 0.1;
// memory_max_events retains memory.events max as diagnostic data;
// complete means a successful final observation, not just a periodic snapshot.
// phases maps prepare/build/test/accept to working_memory_peak, memory_samples,
// elapsed_seconds and cpu_stat deltas. A phase not reached need not be present.
// Failed sampling is sticky; a final read cannot repair a gap in observation.
// The observer constant remains the actual opt-in Docker wrapper. These tests
// relocate only its cgroup root and replace the Docker command boundary.
func TestSandboxWorkingMemoryRule700(t *testing.T) {
	if file := os.Getenv("S700_CHECK_REPORT"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		sandboxBudgetCheckResources(t, 1, "@@s397-resources "+string(data))
		return
	}
	t.Setenv("INVARIANT_CONTAINER_CPUS", "")
	valid := func() map[string]any {
		cpu := map[string]any{"usage_usec": 2000, "user_usec": 1500, "system_usec": 500, "nr_periods": 10, "nr_throttled": 2, "throttled_usec": 300, "extra_counter": 19}
		phases := map[string]any{}
		for _, phase := range []string{"prepare", "build", "test", "accept"} {
			phases[phase] = map[string]any{"working_memory_peak": 131072, "memory_samples": 2, "elapsed_seconds": 1, "cpu_stat": map[string]any{"usage_usec": 500, "user_usec": 375, "system_usec": 125, "nr_periods": 2, "nr_throttled": 0, "throttled_usec": 0, "extra_counter": 1}}
		}
		return map[string]any{
			"memory_peak": int64(4) << 30, "memory_limit": int64(4) << 30,
			"working_memory_peak": 131072, "memory_samples": 8, "sample_interval_seconds": 0.1,
			"memory_max_events": 17, "oom": 0, "oom_kill": 0, "pids_peak": 8, "pids_limit": 1024, "pids_denied": 0,
			"cpu_max": "400000 100000", "cpu_stat": cpu, "elapsed_seconds": 4,
			"complete": true, "phases": phases,
		}
	}
	t.Run("file_cache_fills_cap", func(t *testing.T) { s700ValidateReport(t, valid(), true) })
	t.Run("zero_working_memory_is_measured", func(t *testing.T) {
		r := valid()
		r["working_memory_peak"] = 0
		for _, phase := range r["phases"].(map[string]any) {
			phase.(map[string]any)["working_memory_peak"] = 0
		}
		s700ValidateReport(t, r, true)
	})
	for _, tc := range []struct {
		field string
		value any
	}{
		{"working_memory_peak", int64(4) << 30}, {"working_memory_peak", (int64(4) << 30) + 1},
		{"oom", 1}, {"oom_kill", 1}, {"pids_peak", 1024}, {"pids_denied", 1},
		{"memory_samples", 0}, {"memory_samples", -1}, {"working_memory_peak", -1},
		{"memory_max_events", -1}, {"complete", false}, {"sample_interval_seconds", 0},
		{"memory_limit", int64(2) << 30}, {"pids_limit", 512}, {"cpu_max", "max 100000"},
		{"elapsed_seconds", 0}, {"error", "sampling failed before exit"},
		{"working_memory_peak", "131072"}, {"memory_samples", 1.5}, {"phases", map[string]any{}},
	} {
		t.Run(fmt.Sprintf("reject_%s_%v", tc.field, tc.value), func(t *testing.T) {
			r := valid()
			r[tc.field] = tc.value
			// Keep the kernel peak below the cap: refusals must not depend on
			// the obsolete cache-inclusive threshold.
			r["memory_peak"] = 1 << 20
			s700ValidateReport(t, r, false)
		})
	}
	for _, key := range []string{"memory_peak", "memory_limit", "working_memory_peak", "memory_samples", "sample_interval_seconds", "memory_max_events", "oom", "oom_kill", "pids_peak", "pids_limit", "pids_denied", "cpu_max", "cpu_stat", "elapsed_seconds", "complete", "phases"} {
		t.Run("missing_"+key, func(t *testing.T) {
			r := valid()
			r["memory_peak"] = 1 << 20
			delete(r, key)
			s700ValidateReport(t, r, false)
		})
	}
	for _, key := range []string{"usage_usec", "user_usec", "system_usec", "nr_periods", "nr_throttled", "throttled_usec"} {
		t.Run("missing_cpu_"+key, func(t *testing.T) {
			r := valid()
			r["memory_peak"] = 1 << 20
			delete(r["cpu_stat"].(map[string]any), key)
			s700ValidateReport(t, r, false)
		})
	}
}

// A subprocess lets us assert both success and refusal of the actual
// testing.T-based real-budget validator without replacing its assertions.
func s700ValidateReport(t *testing.T, report map[string]any, wantPass bool) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "report.json")
	s700WriteJSON(t, file, report)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSandboxWorkingMemoryRule700$", "-test.count=1", "-test.timeout=10s")
	cmd.Env = append(os.Environ(), "S700_CHECK_REPORT="+file)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("resource validator did not complete: %v\n%s", ctx.Err(), out)
	}
	if (err == nil) != wantPass {
		t.Errorf("resource validator pass=%v, want %v:\n%s", err == nil, wantPass, out)
	}
	if err != nil && !bytes.Contains(out, []byte("--- FAIL: TestSandboxWorkingMemoryRule700")) {
		t.Fatalf("validator did not return a completed test refusal: %v\n%s", err, out)
	}
}

func TestSandboxSamplesDuringWork700(t *testing.T) {
	for _, mode := range []string{"normal", "failure", "missing_stat", "malformed_stat", "missing_max", "missing_cpu"} {
		t.Run(mode, func(t *testing.T) {
			f := s700Observer(t, mode)
			cmd, output, done := f.start(t)
			defer f.stop(t, cmd)
			select {
			case err := <-done:
				if mode == "normal" && err != nil {
					t.Fatalf("observer launch failed: %v\n%s", err, output)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("bounded observer fixture did not finish")
			}
			r := f.report(t)
			if mode != "normal" && mode != "failure" {
				if errorText, _ := r["error"].(string); errorText == "" {
					t.Errorf("%s during work was erased by healthy final counters: %v", mode, r)
				}
				return
			}
			if r["error"] != nil && r["error"] != "" {
				t.Errorf("observer error: %v", r)
			}
			if r["complete"] != true {
				t.Errorf("natural exit lacks final observation: %v", r)
			}
			if got := s700Number(t, r, "working_memory_peak"); got != 410*(1<<20) {
				t.Errorf("peak = %v, want max of contemporaneous sums 410 MiB, not 700 MiB or final usage", got)
			}
			if s700Number(t, r, "memory_samples") < 12 || s700Number(t, r, "sample_interval_seconds") != 0.1 {
				t.Errorf("missing bounded 100 ms sampling evidence: %v", r)
			}
			if s700Number(t, r, "memory_samples") > s700Number(t, r, "elapsed_seconds")/0.1+16 {
				t.Errorf("sampler ran faster than its stated interval and bounded phase/final observations: %v", r)
			}
			commands, err := os.ReadFile(filepath.Join(f.root, "commands.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			execs := 0
			for _, line := range strings.Split(strings.TrimSpace(string(commands)), "\n") {
				var args []string
				if err := json.Unmarshal([]byte(line), &args); err != nil {
					t.Fatal(err)
				}
				if len(args) > 0 && args[0] == "exec" {
					execs++
				}
			}
			if execs > 8 {
				t.Errorf("100 ms sampling must not launch a Docker exec for every sample: %d execs", execs)
			}
			if s700Number(t, r, "memory_peak") != 4*(1<<30) || s700Number(t, r, "memory_max_events") != 17 {
				t.Errorf("cache saturation diagnostics lost: %v", r)
			}
			phases, ok := r["phases"].(map[string]any)
			if !ok {
				t.Fatalf("no phase memory evidence: %v", r)
			}
			for phase, want := range map[string]float64{"prepare": 410, "build": 320, "test": 130, "accept": 100} {
				p, ok := phases[phase].(map[string]any)
				if !ok {
					t.Errorf("missing %s evidence: %v", phase, phases)
					continue
				}
				if s700Number(t, p, "working_memory_peak") != want*(1<<20) || s700Number(t, p, "memory_samples") < 2 || s700Number(t, p, "elapsed_seconds") <= 0 {
					t.Errorf("wrong %s measurements: %v", phase, p)
				}
				cpu, ok := p["cpu_stat"].(map[string]any)
				if !ok || s700Number(t, cpu, "usage_usec") <= 0 {
					t.Errorf("missing %s CPU delta: %v", phase, p)
				}
			}
			cpu, ok := r["cpu_stat"].(map[string]any)
			if !ok || s700Number(t, cpu, "usage_usec") < 5000 || s700Number(t, cpu, "throttled_usec") < 500 || s700Number(t, cpu, "extra_counter") < 50 || r["cpu_max"] != "400000 100000" || s700Number(t, r, "elapsed_seconds") <= 0 {
				t.Errorf("complete CPU and elapsed evidence lost: %v", r)
			}
			// Even a failed workload leaves a valid resource report; its test
			// verdict and the independent resource check are separate evidence.
			s700ValidateReport(t, r, true)
			before, _ := json.Marshal(r)
			f.remove(t)
			after, _ := json.Marshal(f.report(t))
			if !bytes.Equal(before, after) {
				t.Errorf("late removal race erased or changed valid completed evidence:\nbefore %s\nafter %s", before, after)
			}
		})
	}
}

func TestSandboxResourcesSurviveRemoval700(t *testing.T) {
	for _, mode := range []string{"cancel", "lost_final", "sampling_error"} {
		t.Run(mode, func(t *testing.T) {
			f := s700Observer(t, mode)
			cmd, _, done := f.start(t)
			defer f.stop(t, cmd)
			// The workload holds a high sample, lowers usage, then announces
			// readiness. No final shell probe has run at this point.
			f.waitFile(t, "ready", 8*time.Second)
			var during map[string]any
			until := time.Now().Add(3 * time.Second)
			for time.Now().Before(until) {
				data, err := os.ReadFile(filepath.Join(f.root, "reports", s700CID+".json"))
				if err == nil && json.Unmarshal(data, &during) == nil && s700LooseNumber(during, "memory_samples") >= 2 {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if s700LooseNumber(during, "working_memory_peak") != 410*(1<<20) {
				t.Errorf("host lacks the earlier peak while work is still running: %v", during)
			}
			if during["complete"] == true {
				t.Error("a periodic snapshot claimed final evidence")
			}
			if mode == "lost_final" {
				s700Write(t, filepath.Join(f.root, "deny-exec"), "1", 0o600)
			}
			// Killing the wrapper loses its pipe, just as a canceled Docker CLI
			// does. Container work remains alive until independent rm cleanup.
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			f.remove(t)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("canceled observer did not reap")
			}
			r := f.report(t)
			if s700Number(t, r, "working_memory_peak") != 410*(1<<20) || s700Number(t, r, "oom") < 1 || s700Number(t, r, "oom_kill") < 1 || s700Number(t, r, "pids_denied") < 1 || s700Number(t, r, "pids_peak") < 42 {
				t.Errorf("removal discarded earlier memory, OOM or process evidence: %v", r)
			}
			cpu, ok := r["cpu_stat"].(map[string]any)
			if !ok || s700Number(t, cpu, "usage_usec") < 9000 || s700Number(t, cpu, "throttled_usec") < 900 || s700Number(t, cpu, "extra_counter") < 90 {
				t.Errorf("removal discarded CPU counters: %v", r)
			}
			if mode == "cancel" {
				if r["complete"] != true || (r["error"] != nil && r["error"] != "") {
					t.Errorf("available bounded final observation missing: %v", r)
				}
			} else if errorText, _ := r["error"].(string); errorText == "" {
				t.Errorf("incomplete snapshot masked %s: %v", mode, r)
			}
			if _, err := os.Stat(filepath.Join(f.root, "removed")); err != nil {
				t.Errorf("observer failure prevented cleanup: %v", err)
			}
			// A second removal races an already removed container. It may not
			// lose any known counters, clear an error or create a second report.
			f.remove(t)
			again := f.report(t)
			if s700Number(t, again, "working_memory_peak") < s700Number(t, r, "working_memory_peak") || s700Number(t, again, "oom") < s700Number(t, r, "oom") {
				t.Errorf("repeat removal regressed evidence: %v", again)
			}
			if r["error"] != nil && r["error"] != "" && (again["error"] == nil || again["error"] == "") {
				t.Error("repeat removal cleared observer failure")
			}
		})
	}
}

const s700CID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type s700ObserverFixture struct{ root, wrapper string }

func s700Observer(t *testing.T, mode string) s700ObserverFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	cgroup := filepath.Join(root, "cgroup")
	if err := os.Mkdir(cgroup, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"memory.peak": "4294967296\n", "memory.max": "4294967296\n", "memory.stat": "anon 131072\nshmem 0\nfile 4294836224\n",
		"memory.events": "max 17\noom 0\noom_kill 0\n", "pids.peak": "8\n", "pids.max": "1024\n", "pids.events": "max 0\n",
		"cpu.max": "400000 100000\n", "cpu.stat": "usage_usec 1000\nuser_usec 600\nsystem_usec 400\nnr_periods 10\nnr_throttled 1\nthrottled_usec 100\nextra_counter 10\n",
	} {
		s700Write(t, filepath.Join(cgroup, name), data, 0o600)
	}
	wrapper := filepath.Join(root, "observer")
	s700Write(t, wrapper, strings.ReplaceAll(sandboxBudgetDocker, "/sys/fs/cgroup", cgroup), 0o755)
	s700Write(t, filepath.Join(root, "docker"), s700DockerFixture, 0o755)
	s700Write(t, filepath.Join(root, "work.py"), s700ResourceWork, 0o600)
	t.Setenv("S397_REAL_DOCKER", filepath.Join(root, "docker"))
	t.Setenv("S397_RESOURCE_DIR", root)
	t.Setenv("S397_OBSERVER_SECONDS", sandboxBudgetObserverSeconds())
	t.Setenv("S700_ROOT", root)
	t.Setenv("S700_MODE", mode)
	t.Setenv("INVARIANT_CONTAINER_CPUS", "")
	f := s700ObserverFixture{root, wrapper}
	t.Cleanup(func() {
		// Fixture cleanup is independent of whether the actual observer works.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		exec.CommandContext(ctx, filepath.Join(root, "docker"), "rm", "--force", s700CID).Run()
	})
	return f
}

func (f s700ObserverFixture) start(t *testing.T) (*exec.Cmd, *bytes.Buffer, <-chan error) {
	t.Helper()
	cmd := exec.Command("python3", f.wrapper, "run", "--name", "fixture700", "--rm", "--memory", "4g", "--pids-limit", "1024", "--cpus", "4", "fixture-image", "sh", "-c", "exec python3 "+s700ShellQuote(filepath.Join(f.root, "work.py")))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	out := new(bytes.Buffer)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return cmd, out, done
}

func (f s700ObserverFixture) stop(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func (f s700ObserverFixture) remove(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "python3", f.wrapper, "rm", "--force", s700CID).CombinedOutput()
	if err != nil {
		t.Errorf("observer did not delegate bounded removal: %v\n%s", err, out)
	}
}

func (f s700ObserverFixture) report(t *testing.T) map[string]any {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(f.root, "reports", "*.json"))
	if err != nil || len(files) != 1 || filepath.Base(files[0]) != s700CID+".json" {
		t.Fatalf("want one identified host report, got %v: %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var r map[string]any
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func (f s700ObserverFixture) waitFile(t *testing.T, name string, timeout time.Duration) {
	t.Helper()
	until := time.Now().Add(timeout)
	for time.Now().Before(until) {
		if _, err := os.Stat(filepath.Join(f.root, name)); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fixture did not reach %s", name)
}

func s700Number(t *testing.T, r map[string]any, key string) float64 {
	t.Helper()
	v, ok := r[key].(float64)
	if !ok {
		t.Errorf("missing/malformed numeric %s: %v", key, r)
	}
	return v
}
func s700LooseNumber(r map[string]any, key string) float64 { v, _ := r[key].(float64); return v }
func s700ShellQuote(s string) string                       { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func s700Write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}
func s700WriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	s700Write(t, path, string(data), 0o600)
}

// Real wrapper, real probes and real subprocesses; only Docker lifecycle and
// cgroup files are local fixtures. No canned resource reports are emitted.
const s700DockerFixture = `#!/usr/bin/env python3
import json, os, pathlib, signal, subprocess, sys
root = pathlib.Path(os.environ['S700_ROOT'])
a = sys.argv[1:]
cid = 'a'*64
with (root/'commands.jsonl').open('a') as log: log.write(json.dumps(a)+'\n')
if a and a[0] == 'inspect':
    if (root/'removed').exists(): sys.exit(1)
    print(cid); sys.exit(0)
if a and a[0] == 'exec':
    if (root/'deny-exec').exists() or (root/'removed').exists() or (root/'exited').exists():
        sys.stderr.write('container is unavailable\n'); sys.exit(1)
    # The real observer's probe/sampler is executed verbatim.
    os.execvp(a[2],a[2:])
if a and a[0] == 'rm':
    (root/'removed').write_text(' '.join(a))
    try: os.killpg(int((root/'group').read_text()),signal.SIGKILL)
    except (FileNotFoundError,ProcessLookupError,PermissionError): pass
    sys.exit(0)
if not a or a[0]!='run' or a[-3:-1]!=['sh','-c']: sys.exit('unexpected Docker boundary')
for i,arg in enumerate(a):
    if arg=='--cidfile': pathlib.Path(a[i+1]).write_text(cid)
    elif arg.startswith('--cidfile='): pathlib.Path(arg.split('=',1)[1]).write_text(cid)
p = subprocess.Popen(['sh','-c',a[-1]],start_new_session=True)
(root/'group').write_text(str(p.pid))
try: code=p.wait(timeout=18)
except subprocess.TimeoutExpired:
    os.killpg(p.pid,signal.SIGKILL); p.wait(); code=124
(root/'exited').write_text(str(code))
sys.exit(code if code>=0 else 128-code)
`

const s700ResourceWork = `import json, os, pathlib, sys, time
root=pathlib.Path(os.environ['S700_ROOT']); cg=root/'cgroup'; mode=os.environ['S700_MODE']
def write(name,text):
    p=cg/name; tmp=p.with_suffix('.next'); tmp.write_text(text); os.replace(tmp,p)
def memory(anon,shmem):
    write('memory.stat','anon %d\nshmem %d\nfile %d\n' % (anon*(1<<20),shmem*(1<<20),4*(1<<30)-anon*(1<<20)))
def cpu(n):
    write('cpu.stat','usage_usec %d\nuser_usec %d\nsystem_usec %d\nnr_periods %d\nnr_throttled %d\nthrottled_usec %d\nextra_counter %d\n' % (n*1000,n*600,n*400,n*10,n,n*100,n*10))
def phase(name):
    print('@@result '+json.dumps(dict(kind='phase_start',phase=name)),flush=True)
phase('prepare'); time.sleep(.25)
memory(400,10); cpu(2); time.sleep(.65)
memory(1,0); time.sleep(.25)
if mode in ('cancel','lost_final','sampling_error'):
    write('memory.events','max 17\noom 1\noom_kill 1\n')
    write('pids.peak','42\n'); write('pids.events','max 1\n'); cpu(9)
    if mode=='sampling_error':
        write('memory.stat','anon invalid\nshmem 0\n'); time.sleep(.65); memory(1,0)
    time.sleep(.65)
    (root/'ready').write_text('working')
    time.sleep(14)
    sys.exit(0)
for n,(name,anon,shmem) in enumerate((('build',20,300),('test',100,30),('accept',60,40)),3):
    phase(name); time.sleep(.25); memory(anon,shmem); cpu(n)
    saved=None; broken=None
    if name=='build':
        if mode in ('missing_stat','malformed_stat'): broken='memory.stat'
        if mode=='missing_max': broken='memory.events'
        if mode=='missing_cpu': broken='cpu.stat'
        if broken:
            saved=(cg/broken).read_text()
            if mode=='missing_stat': (cg/broken).unlink()
            elif mode=='malformed_stat': write(broken,'anon invalid\nshmem 0\n')
            elif mode=='missing_max': write(broken,'oom 0\noom_kill 0\n')
            else: write(broken,'usage_usec 3000\n')
    time.sleep(.65)
    if broken: write(broken,saved)
    memory(1,0); time.sleep(.25)
print('@@result '+json.dumps(dict(kind='done')),flush=True)
sys.exit(7 if mode=='failure' else 0)
`
