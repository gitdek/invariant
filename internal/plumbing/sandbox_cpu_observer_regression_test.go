package plumbing_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Execute the observer's actual probe and report writer against cgroup files.
// Docker measurements remain opt-in, but missing counters must never look like
// an unthrottled container and a late removal must not erase the exit report.
func TestSandboxCPUResourceObserver(t *testing.T) {
	t.Setenv("INVARIANT_CONTAINER_CPUS", "")
	root := t.TempDir()
	for name, value := range map[string]string{
		"memory.peak":   "1048576",
		"memory.max":    "4294967296",
		"memory.stat":   "anon 65536\nshmem 4096\nfile 978944\n",
		"memory.events": "max 0\noom 0\noom_kill 0\n",
		"pids.peak":     "8",
		"pids.max":      "1024",
		"pids.events":   "max 0\n",
		"cpu.max":       "400000 100000\n",
		"cpu.stat":      "usage_usec 2000\nuser_usec 1500\nsystem_usec 500\nnr_periods 10\nnr_throttled 2\nthrottled_usec 300\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "reports"), 0700); err != nil {
		t.Fatal(err)
	}
	prefix, _, ok := strings.Cut(sandboxBudgetDocker, "if args and args[0] == \"rm\":")
	if !ok {
		t.Fatal("cannot locate the observer's command dispatch")
	}
	const exercise = `
state = root / "state.json"
def observe_now():
    return observation(state, initial=True)
cid = "a" * 64
cidfile = root / "cid"
cidfile.write_text(cid)
atomic(root / "run-test.json", dict(cidfile=str(cidfile), name="fixture", state=str(state)))
first = observe_now()
assert first["cpu_max"] == "400000 100000"
assert first["cpu_stat"]["usage_usec"] == 2000
assert first["cpu_stat"]["nr_throttled"] == 2
assert first["cpu_stat"]["throttled_usec"] == 300
assert not first["complete"]
save(cid, first)
local = json.loads(state.read_text())
local["_started"] -= 1
local["_phase_started"] -= 1
atomic(state, local)
(root / "cpu.stat").write_text("usage_usec 3000\nuser_usec 2000\nsystem_usec 1000\nnr_periods 20\nnr_throttled 3\nthrottled_usec 400\nextra_counter 10\n")
save(cid, observe_now())
save(cid, first)
saved = json.loads((reports / (cid + ".json")).read_text())
assert saved["cpu_stat"]["usage_usec"] == 3000
assert saved["cpu_stat"]["nr_throttled"] == 3
assert saved["cpu_stat"]["throttled_usec"] == 400
assert saved["cpu_stat"]["extra_counter"] == 10
assert saved["elapsed_seconds"] >= 1
# Use the actual final-probe command, with only the cgroup root relocated.
result = subprocess.run([sys.executable, "-c", probe, str(state)], capture_output=True, timeout=3)
assert result.returncode == 0, result.stderr
save(cid, extract(result.stdout))
saved = json.loads((reports / (cid + ".json")).read_text())
assert completed(saved), saved
assert saved["phases"]["prepare"]["cpu_stat"]["usage_usec"] == 1000
print("@@s397-resources " + json.dumps(saved))
# Sampling errors remain sticky even when a later observation is healthy.
original = (root / "cpu.max").read_text()
(root / "cpu.max").unlink()
missing = observe({})
assert missing.get("error"), missing
save(cid, missing)
(root / "cpu.max").write_text(original)
save(cid, saved)
failed = json.loads((reports / (cid + ".json")).read_text())
assert failed.get("error"), failed
assert failed["cpu_stat"]["usage_usec"] == 3000
assert failed["memory_samples"] >= saved["memory_samples"]
changed = dict(saved, cpu_max="200000 100000")
save("b" * 64, saved)
save("b" * 64, changed)
assert "limits changed" in json.loads((reports / ("b" * 64 + ".json")).read_text())["error"]
# Every required CPU counter and malformed memory/process evidence is checked
# by the real reader, rather than supplying canned reports.
cpu = (root / "cpu.stat").read_text()
for key in cpu_keys:
    (root / "cpu.stat").write_text("\n".join(line for line in cpu.splitlines() if not line.startswith(key + " ")))
    assert observe({}).get("error"), key
(root / "cpu.stat").write_text(cpu)
for name, broken in (("memory.stat", "anon 1\n"), ("memory.stat", "anon 1\nshmem -1\n"),
                     ("memory.events", "max 0\noom 0\n"), ("pids.events", "max -1\n"),
                     ("cpu.stat", cpu + "usage_usec 0\n"), ("cpu.max", "max 100000"),
                     ("memory.max", "max"), ("pids.peak", "invalid")):
    original = (root / name).read_text()
    (root / name).write_text(broken)
    assert observe({}).get("error"), (name, broken)
    (root / name).write_text(original)
`
	t.Setenv("S397_REAL_DOCKER", "unused")
	t.Setenv("S397_RESOURCE_DIR", root)
	t.Setenv("S397_OBSERVER_SECONDS", sandboxBudgetObserverSeconds())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "python3", "-c", strings.ReplaceAll(prefix, "/sys/fs/cgroup", root)+exercise).CombinedOutput()
	if err != nil {
		t.Fatalf("resource observer: %v\n%s", err, out)
	}
	sandboxBudgetCheckResources(t, 1, string(out))
}
