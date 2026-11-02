package plumbing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The host already stops Docker on its deadline. That cannot substitute for
// bounded waits inside the container: the runner must stop subprocesses and
// return its partial records while its output pipe is still available.
// This Docker stand-in copies the staged entry and runs it independently,
// with precisely the budget/environment Run supplied. Its CLI returns early,
// so the host's cancellation/removal cannot rescue an unbounded inner phase.
func TestSandboxBoundsEveryPhaseInsideContainer(t *testing.T) {
	for _, phase := range []string{"vet", "build", "discovery", "acceptance"} {
		t.Run(phase, func(t *testing.T) {
			f := s397New(t)
			started := filepath.Join(f.observe, "phase-started")
			f.pkg(t, "alpha", fmt.Sprintf(`package alpha
import ("os"; "strings"; "testing"; "time")
func TestMain(m *testing.M) {
  if %t {
    for _, arg := range os.Args[1:] {
      if arg == "-test.list" || strings.HasPrefix(arg, "-test.list=") {
        os.WriteFile(%q, nil, 0600)
        time.Sleep(60*time.Second)
      }
    }
  }
  os.Exit(m.Run())
}
func TestHealthy(t *testing.T) {}
`, phase == "discovery", started))
			f.accept(t, "contract", "TestRatified", fmt.Sprintf(`package contract
import ("os"; "testing"; "time")
func TestRatified(t *testing.T) {
  p := %q
  if %t { if _, err := os.Stat(p); err == nil { time.Sleep(60*time.Second) } }
  if err := os.WriteFile(p, []byte("first execution finished"), 0600); err != nil { t.Fatal(err) }
}
`, filepath.Join(f.observe, "contract-runs"), phase == "acceptance"))
			f.warm(t)
			if phase == "vet" || phase == "build" {
				verb := "vet"
				if phase == "build" {
					verb = "test"
				}
				s397Write(t, filepath.Join(f.bin, "go"), fmt.Sprintf(`#!/usr/bin/env python3
import os, pathlib, sys, time
a = sys.argv[1:]
if a and a[0] == "-C": a = a[2:]
elif a and a[0].startswith("-C="): a = a[1:]
if a and a[0] == %q:
    pathlib.Path(%q).write_text("started")
    time.sleep(60)
os.execv(os.environ["S397_REAL_GO"], [os.environ["S397_REAL_GO"]] + sys.argv[1:])
`, verb, started), 0o755)
			}
			s397Write(t, filepath.Join(f.bin, "docker"), s397DetachedDocker, 0o755)
			t.Cleanup(func() {
				// The watchdog also cleans up if an assertion fails before the
				// runner returns. All fixture bodies themselves have a 60s bound.
				exec.Command("python3", "-c", `import os, pathlib, signal, sys
try: os.killpg(int(pathlib.Path(sys.argv[1]).read_text()), signal.SIGKILL)
except (FileNotFoundError, ProcessLookupError): pass
`, filepath.Join(f.control, "detached-group")).Run()
			})
			const timeout = 6 * time.Second
			began := time.Now()
			s := Sandbox{Image: "detached-fixture", ModCache: f.mod, Cache: f.cache, Timeout: timeout, ShardTimeout: 30 * time.Second}
			// The intentionally detached CLI cannot give Run a complete result;
			// assess the actual entry's output after it exits, below.
			_, _ = s.Run(context.Background(), f.root, f.plan)
			var exit []byte
			for time.Since(began) < timeout+5*time.Second {
				if b, err := os.ReadFile(filepath.Join(f.control, "inner-exit")); err == nil && len(b) > 0 {
					exit = b
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			out, _ := os.ReadFile(filepath.Join(f.control, "inner-output"))
			if len(exit) == 0 {
				t.Fatalf("%s outlived the runner's %s budget without host cancellation:\n%s", phase, timeout, out)
			}
			if _, err := strconv.Atoi(string(exit)); err != nil {
				t.Fatalf("%s ended only through the fixture watchdog: %s\n%s", phase, exit, out)
			}
			r := parse(string(out), f.plan)
			if !r.DeadlineExceeded || r.Passed() {
				t.Errorf("the inner %s deadline must produce failed partial results: %+v\n%s", phase, r, out)
			}
			s397Summary(t, r, "deadline")
			if phase == "acceptance" {
				if !r.Vet || !r.Tests {
					t.Errorf("completed phases were lost: %s", r.Summary())
				}
				s397Status(t, r, "contract", "TestRatified", "accept", "running")
			} else if _, err := os.Stat(started); err != nil {
				t.Errorf("fixture never entered %s: %v\n%s", phase, err, out)
			}
			if r.Accepted["contract/pinned_accept_test.go:TestRatified"] {
				t.Error("unfinished acceptance phase must not count as accepted")
			}
		})
	}
}

const s397DetachedDocker = `#!/usr/bin/env python3
import os, pathlib, shutil, signal, subprocess, sys
control = pathlib.Path(os.environ["S397_CONTROL"])
a = sys.argv[1:]
if a and a[0] == "rm": sys.exit(0)
if not a or a[0] != "run" or a[-3:-1] != ["sh", "-c"]: sys.exit(2)
env, mounts = dict(os.environ), {}
i = 1
while i < len(a):
    if a[i] in ("-e", "--env"):
        k, _, v = a[i+1].partition("="); env[k] = v; i += 2
    elif a[i] in ("-v", "--volume"):
        p = a[i+1].split(":"); mounts[p[1]] = p[0]; i += 2
    else: i += 1
src = str(control / "independent-src")
shutil.copytree(mounts["/src"], src)
mounts["/src"] = src
for k, v in list(env.items()):
    if v in mounts: env[k] = mounts[v]
import shlex
script = a[-1].replace("cd /src", "cd " + shlex.quote(src))
pid = os.fork()
if pid:
    (control / "detached-group").write_text(str(pid))
    sys.exit(0)
os.setsid()
# Do not keep the fake CLI's output pipes open after it returns.
fd = os.open(control / "inner-output", os.O_CREAT | os.O_WRONLY | os.O_TRUNC, 0o600)
os.dup2(fd, 1); os.dup2(fd, 2); os.close(fd)
try:
    code = subprocess.run(["sh", "-c", script], cwd=src, env=env, timeout=15).returncode
    (control / "inner-exit").write_text(str(code))
except subprocess.TimeoutExpired:
    (control / "inner-exit").write_text("watchdog")
finally:
    os.killpg(os.getpid(), signal.SIGKILL)
`
