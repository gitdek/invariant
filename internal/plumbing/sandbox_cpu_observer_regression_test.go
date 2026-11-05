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
		"memory.peak": "1048576", "memory.max": "4294967296", "pids.peak": "8", "pids.max": "1024",
		"memory.events": "oom 0\noom_kill 0\n", "pids.events": "max 0\n", "cpu.max": "400000 100000\n",
		"cpu.stat": "usage_usec 2000\nuser_usec 1500\nsystem_usec 500\nnr_periods 10\nnr_throttled 2\nthrottled_usec 300\n",
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
import contextlib, io
probe = probe.replace('/sys/fs/cgroup', str(root))
def observe():
    output = io.StringIO()
    with contextlib.redirect_stdout(output): exec(probe)
    return extract(output.getvalue().encode())
cid = "a" * 64
cidfile = root / "cid"
cidfile.write_text(cid)
atomic(root / "run-test.json", dict(cidfile=str(cidfile), name="fixture", started=time.monotonic()-1))
first = observe()
assert first["cpu_max"] == "400000 100000"
assert first["cpu_stat"]["usage_usec"] == 2000
assert first["cpu_stat"]["nr_throttled"] == 2
assert first["cpu_stat"]["throttled_usec"] == 300
save(cid, first)
(root / "cpu.stat").write_text("usage_usec 3000\nuser_usec 2000\nsystem_usec 1000\nnr_periods 20\nnr_throttled 3\nthrottled_usec 400\n")
save(cid, observe())
save(cid, first)
saved = json.loads((reports / (cid + ".json")).read_text())
assert saved["cpu_stat"]["usage_usec"] == 3000
assert saved["cpu_stat"]["nr_throttled"] == 3
assert saved["cpu_stat"]["throttled_usec"] == 400
assert saved["elapsed_seconds"] >= 1
(root / "cpu.max").unlink()
missing = observe()
assert missing.get("error"), missing
save(cid, missing)
assert json.loads((reports / (cid + ".json")).read_text()) == saved
changed = dict(saved, cpu_max="max 100000")
save(cid, changed)
assert "limits changed" in json.loads((reports / (cid + ".json")).read_text())["error"]
print("@@s397-resources " + json.dumps(saved))
`
	t.Setenv("S397_REAL_DOCKER", "unused")
	t.Setenv("S397_RESOURCE_DIR", root)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "python3", "-c", prefix+exercise).CombinedOutput()
	if err != nil {
		t.Fatalf("resource observer: %v\n%s", err, out)
	}
	sandboxBudgetCheckResources(t, 1, string(out))
}
