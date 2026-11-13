package plumbing_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Publish a final report after removal has read an incomplete one. Exercise
// the actual dispatch, observations, locking and merger; only the Docker
// command boundary and the timing of the final publication are controlled.
func TestSandboxRemovalCompletionRace(t *testing.T) {
	const dispatchStart = "if args and args[0] == \"rm\":"
	prefix, dispatch, ok := strings.Cut(sandboxBudgetDocker, dispatchStart)
	if !ok {
		t.Fatal("cannot locate the observer's command dispatch")
	}
	for _, mode := range []string{"during_exec", "before_lock", "incomplete", "sticky", "probe_error"} {
		t.Run(mode, func(t *testing.T) {
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
			t.Setenv("S397_REAL_DOCKER", "fixture-docker")
			t.Setenv("S397_RESOURCE_DIR", root)
			t.Setenv("S397_OBSERVER_SECONDS", sandboxBudgetObserverSeconds())
			t.Setenv("S700_REMOVAL_RACE", mode)
			script := strings.ReplaceAll(prefix, "/sys/fs/cgroup", root) +
				"\nremoval_dispatch = " + strconv.Quote(dispatchStart+dispatch) + "\n" + sandboxRemovalRace
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "python3", "-c", script).CombinedOutput()
			if err != nil {
				t.Fatalf("removal completion race: %v\n%s", err, out)
			}
		})
	}
}

const sandboxRemovalRace = `
mode = os.environ["S700_REMOVAL_RACE"]
cid = "a" * 64
args = ["rm", "--force", cid]
state = root / "state.json"
path = reports / (cid + ".json")
atomic(root / "run-test.json", dict(cid=cid, name="fixture", state=str(state)))
first = save(cid, observation(state, initial=True))
assert not first["complete"]
memory = (root / "memory.stat").read_text()
if mode == "sticky":
    (root / "memory.stat").unlink()
    failed = observation(state)
    assert failed.get("error"), failed
    (root / "memory.stat").write_text(memory)
final = observation(state, final=True)
if mode != "sticky": assert completed(final), final
expected = None
pending_publication = False
original_locked = locked

@contextlib.contextmanager
def locked(path):
    global pending_publication, expected
    if pending_publication and path == reports / (cid + ".lock"):
        # Publication just before cleanup acquires its report lock catches
        # a recheck outside the lock, which would still see the old sample.
        pending_publication = False
        expected = save(cid, final)
    with original_locked(path):
        yield

probes = 0
def docker_run(command, **options):
    global probes, pending_publication, expected, failed
    probes += 1
    assert command == [real, "exec", cid, "python3", "-c", probe, str(state)], command
    assert options["timeout"] == 3, options
    assert not completed(json.loads(path.read_text()))
    if mode == "before_lock":
        pending_publication = True
    elif mode != "incomplete":
        expected = save(cid, final)
    if mode == "probe_error":
        # A real failed observation must stay sticky even when a healthy
        # completed report was published concurrently with the probe.
        (root / "memory.stat").unlink()
        failed = observe({}, final=True)
        (root / "memory.stat").write_text(memory)
        assert failed.get("error"), failed
        return subprocess.CompletedProcess(command, 0, marker + json.dumps(failed).encode() + b"\n", b"")
    return subprocess.CompletedProcess(command, 1, b"", b"container is unavailable")

class Removed(Exception): pass
removals = 0
def docker_exec(executable, command):
    global removals
    assert executable == real and command == [real, *args], (executable, command)
    removals += 1
    raise Removed()

subprocess.run = docker_run
os.execv = docker_exec
try:
    exec(removal_dispatch)
except Removed:
    pass
assert probes == 1 and removals == 1, (probes, removals)
assert list(reports.glob("*.json")) == [path]
saved = json.loads(path.read_text())
assert saved["container_id"] == cid and saved["container_name"] == "fixture", saved
if mode in ("during_exec", "before_lock"):
    assert completed(saved), saved
    assert saved == expected, (saved, expected)
elif mode == "incomplete":
    assert not saved["complete"] and "before removal of" in saved.get("error", ""), saved
    assert saved["memory_samples"] == first["memory_samples"], saved
else:
    assert saved.get("error") == failed["error"], saved
    assert not completed(saved), saved
    assert saved["memory_samples"] >= final["memory_samples"], saved
assert saved["working_memory_peak"] >= first["working_memory_peak"], saved
assert saved["cpu_stat"] == final["cpu_stat"], saved
`
