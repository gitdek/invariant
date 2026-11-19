package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests execute the embedded runner through the public session API.
// The local transport has the same extra parent as the gate's Docker fakes:
// cancelling its exec.Cmd kills the launcher, not the Python runner it owns.
// No watchdog, signal forwarding or lifetime implementation is supplied by
// the fixture. Each test has a private process group for emergency cleanup.
// The original argv remains literal; only container paths and -P (absent
// from host Python 3.9/3.10) are translated, as in pa318Local.
const pa934Launcher = `import os, subprocess, sys
p = subprocess.Popen(sys.argv[1:])
with open(os.environ['LIFE934_PIDS'], 'w') as stream:
    stream.write(str(os.getpid()) + ' ' + str(p.pid))
sys.exit(p.wait())
`

const pa934BeforeDriver = `import os, signal, sys, time
def block934():
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    with open(os.environ['LIFE934_READY'], 'w') as stream:
        stream.write(str(os.getpid()))
    if os.environ['LIFE934_MODE'] == 'sleep':
        time.sleep(600)
    else:
        # Builtin sum stays in C and holds the GIL; a Python watcher thread
        # or a signal handler on this interpreter cannot stop it promptly.
        sys.setswitchinterval(600)
        sum(range(1 << 62))
if os.environ.get('LIFE934_MODE') == 'import':
    block934()
`

const pa934AfterDriver = `
mode934 = os.environ.get('LIFE934_MODE')
if mode934 == 'normal-slow':
    time.sleep(6)
elif mode934 == 'failure':
    raise RuntimeError('expected lifetime fixture failure')
elif mode934 in ('sleep', 'busy'):
    block934()
`

type pa934Started struct {
	pid     int
	command PythonApplicationCommand
}

type pa934Transport struct {
	dir, python, mode string
	newGroup          bool
	started           chan pa934Started
	// An optional barrier keeps Execute and the session alive after the
	// launcher dies. This distinguishes parent loss from session cleanup.
	afterExit <-chan struct{}
}

func (f *pa934Transport) execute(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
	fail := func(err error) (PythonApplicationProcess, error) {
		return PythonApplicationProcess{ExitCode: -1}, err
	}
	b, err := os.ReadFile(filepath.Join(c.RuntimeDir, "request.json"))
	if err != nil {
		return fail(err)
	}
	var request map[string]any
	if err := json.Unmarshal(b, &request); err != nil {
		return fail(err)
	}
	mapped := func(s string) string {
		for _, pair := range [][2]string{{"/runtime", c.RuntimeDir}, {"/output", c.OutputDir}, {"/src", c.Root}} {
			if s == pair[0] || strings.HasPrefix(s, pair[0]+"/") {
				return pair[1] + strings.TrimPrefix(s, pair[0])
			}
		}
		return s
	}
	var remap func(any) any
	remap = func(value any) any {
		switch v := value.(type) {
		case string:
			return mapped(v)
		case map[string]any:
			for k, x := range v {
				v[k] = remap(x)
			}
		case []any:
			for i, x := range v {
				v[i] = remap(x)
			}
		}
		return value
	}
	remap(request)
	request["libraries"] = filepath.Join(f.dir, "libraries")
	b, err = json.Marshal(request)
	if err != nil {
		return fail(err)
	}
	local := filepath.Join(c.OutputDir, "local-request.json")
	if err := os.WriteFile(local, b, 0600); err != nil {
		return fail(err)
	}
	args := []string{"-I", "-S", "-c", pa934Launcher, f.python}
	for _, arg := range c.Args {
		if arg == "-P" {
			continue
		}
		if arg == "/runtime/request.json" {
			args = append(args, local)
		} else {
			args = append(args, mapped(arg))
		}
	}
	cmd := exec.CommandContext(ctx, f.python, args...)
	cmd.Dir = mapped(c.WorkingDir)
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Env = append(cmd.Env, "LIFE934_MODE="+f.mode,
		"LIFE934_READY="+filepath.Join(f.dir, "ready"),
		"LIFE934_PIDS="+filepath.Join(f.dir, "pids"))
	if f.newGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	log, err := os.Create(filepath.Join(f.dir, "runner.log"))
	if err != nil {
		return fail(err)
	}
	defer log.Close()
	// Files avoid inherited output pipes making Wait depend on the orphan.
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	f.started <- pa934Started{cmd.Process.Pid, c}
	err = cmd.Wait()
	if f.afterExit != nil {
		<-f.afterExit
	}
	out, _ := os.ReadFile(log.Name())
	events, _ := os.ReadFile(filepath.Join(c.OutputDir, "events.jsonl"))
	return PythonApplicationProcess{ExitCode: cmd.ProcessState.ExitCode(), Events: events, Stderr: string(out)}, err
}

func pa934Session(t *testing.T, dir, mode string, group bool, barrier <-chan struct{}) (*PythonApplicationSession, PythonApplicationInvocationKey, *pa934Transport, string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "libraries"), 0700); err != nil {
		t.Fatal(err)
	}
	root, p := pa318Fixture(t, false, pa934BeforeDriver+pa318Functional+pa934AfterDriver, false)
	f := &pa934Transport{dir: dir, python: python, mode: mode, newGroup: group, started: make(chan pa934Started, 8), afterExit: barrier}
	opts := pa315Options(new(int))
	opts.Execute = f.execute
	s := pa315Prepare(t, p, opts)
	return s, pa318Driver(t, s).Key, f, root
}

type pa934Run struct {
	done   chan struct{}
	result *PythonApplicationExecutionResult
	err    error
}

func pa934RunAsync(s *PythonApplicationSession, key PythonApplicationInvocationKey, ctx context.Context) *pa934Run {
	r := &pa934Run{done: make(chan struct{})}
	go func() {
		r.result, r.err = s.Run(ctx, key)
		close(r.done)
	}()
	return r
}

func pa934Wait(t *testing.T, duration time.Duration, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %s", description, duration)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func pa934Done(t *testing.T, done <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not finish within five seconds", description)
	}
}

func pa934Running(t *testing.T, pid int) bool {
	t.Helper()
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	if err != nil {
		t.Fatalf("inspect process %d: %v", pid, err)
	}
	// Container PID 1 need not immediately reap an adopted orphan. A zombie
	// has exited and cannot execute Python; never mistake it for a live run.
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if end := strings.LastIndex(string(b), ") "); end >= 0 {
			state := strings.Fields(string(b)[end+2:])
			if len(state) > 0 && (state[0] == "Z" || state[0] == "X") {
				return false
			}
		}
	}
	return true
}

func pa934Ready(t *testing.T, f *pa934Transport) (int, int) {
	t.Helper()
	var parent, runner int
	pa934Wait(t, 30*time.Second, "runner reaching the blocking driver", func() bool {
		b, e := os.ReadFile(filepath.Join(f.dir, "pids"))
		if e != nil {
			return false
		}
		if n, e := fmt.Sscanf(string(b), "%d %d", &parent, &runner); e != nil || n != 2 || parent <= 1 || runner <= 1 {
			return false
		}
		ready, e := os.ReadFile(filepath.Join(f.dir, "ready"))
		return e == nil && string(ready) == strconv.Itoa(runner)
	})
	if !pa934Running(t, parent) || !pa934Running(t, runner) {
		b, _ := os.ReadFile(filepath.Join(f.dir, "runner.log"))
		t.Fatalf("fixture died before the lifetime action: parent=%d runner=%d\n%s", parent, runner, b)
	}
	return parent, runner
}

func pa934StartedRun(t *testing.T, f *pa934Transport, r *pa934Run, cancel context.CancelFunc) pa934Started {
	t.Helper()
	var start pa934Started
	select {
	case start = <-f.started:
	case <-r.done:
		t.Fatalf("runner never started: %+v, %v", r.result, r.err)
	case <-time.After(30 * time.Second):
		t.Fatal("runner transport never started")
	}
	t.Cleanup(func() {
		cancel()
		// Emergency cleanup runs after assertions, including on the base
		// where the product does not stop the runner. It cannot make a test pass.
		_ = syscall.Kill(-start.pid, syscall.SIGKILL)
		pa934Done(t, r.done, "emergency invocation cleanup")
	})
	return start
}

func pa934Rejected(t *testing.T, r *pa934Run) {
	t.Helper()
	pa934Done(t, r.done, "interrupted invocation")
	if r.err == nil || r.result == nil || r.result.Success || r.result.Complete || len(r.result.Observations) != 0 {
		t.Fatalf("interruption earned evidence: %+v, %v", r.result, r.err)
	}
}

func TestPythonApplicationExecutionCancellationLeavesNoRunner934(t *testing.T) {
	// Normal runs, including one longer than the orphan grace, keep their
	// evidence and stable fingerprints. A subsequent failure stays a failure.
	t.Run("normal and failed runs", func(t *testing.T) {
		s, key, f, root := pa934Session(t, t.TempDir(), "normal-slow", true, nil)
		before := pa315Tree(t, root)
		var fingerprint string
		for _, mode := range []string{"normal-slow", "normal", "failure", "normal"} {
			f.mode = mode
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			r := pa934RunAsync(s, key, ctx)
			start := pa934StartedRun(t, f, r, cancel)
			select {
			case <-r.done:
			case <-ctx.Done():
				t.Fatalf("ordinary %s run did not finish: %v", mode, ctx.Err())
			}
			cancel()
			if mode == "failure" {
				pa934Rejected(t, r)
			} else {
				if r.err != nil || r.result == nil || !r.result.Success || !r.result.Complete || r.result.ExitCode != 0 || len(r.result.Observations) != 3 {
					t.Fatalf("normal execution changed: %+v, %v", r.result, r.err)
				}
				if fingerprint == "" {
					fingerprint = r.result.Fingerprint
				} else if fingerprint != r.result.Fingerprint {
					t.Fatal("lifetime identities changed a portable execution fingerprint")
				}
			}
			pa934Wait(t, 5*time.Second, "ordinary run and its helpers exiting", func() bool {
				return errors.Is(syscall.Kill(-start.pid, 0), syscall.ESRCH)
			})
		}
		if !reflect.DeepEqual(before, pa315Tree(t, root)) {
			t.Fatal("lifetime handling changed application inputs")
		}
	})
	for _, kind := range []string{"cancel", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			s, key, f, _ := pa934Session(t, t.TempDir(), "busy", true, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "deadline" {
				// Start the deadline at Execute, after session preparation.
				// A generous startup interval is separate from the five-second
				// lifetime bound checked after that deadline has actually fired.
				ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
			}
			r := pa934RunAsync(s, key, ctx)
			pa934StartedRun(t, f, r, cancel)
			_, runner := pa934Ready(t, f)
			if kind == "cancel" {
				cancel()
			} else {
				<-ctx.Done()
			}
			pa934Wait(t, 5*time.Second, "runner stopping after "+kind, func() bool { return !pa934Running(t, runner) })
			pa934Rejected(t, r)
			if !errors.Is(r.err, ctx.Err()) {
				t.Fatalf("lost invocation cancellation cause: %v, want %v", r.err, ctx.Err())
			}
		})
	}
}

func TestPythonApplicationExecutionCloseStopsRunner934(t *testing.T) {
	s, key, f, _ := pa934Session(t, t.TempDir(), "sleep", true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := pa934RunAsync(s, key, ctx)
	start := pa934StartedRun(t, f, r, cancel)
	_, runner := pa934Ready(t, f)
	closed := make(chan struct{})
	var closeErr error
	go func() { closeErr = s.Close(); close(closed) }()
	pa934Wait(t, 5*time.Second, "Close stopping the active runner", func() bool { return !pa934Running(t, runner) })
	pa934Done(t, closed, "Close")
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	pa934Rejected(t, r)
	if err := s.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if result, err := s.Run(context.Background(), key); err == nil || result != nil && result.Success {
		t.Fatalf("closed session ran: %+v, %v", result, err)
	}
	for _, path := range []string{start.command.RuntimeDir, start.command.OutputDir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("Close retained private resources %s: %v", path, err)
		}
	}
}

func TestPythonApplicationExecutionOwnerDeathStopsRunner934(t *testing.T) {
	if dir := os.Getenv("LIFE934_OWNER_HELPER"); dir != "" {
		// Put every child-owned temporary path under the outer test's root,
		// since SIGKILL deliberately prevents all Go deferred cleanup.
		t.Setenv("TMPDIR", dir)
		s, key, _, _ := pa934Session(t, dir, os.Getenv("LIFE934_OWNER_MODE"), false, nil)
		_, _ = s.Run(context.Background(), key)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"import", "sleep"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			log, err := os.Create(filepath.Join(dir, "owner.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd := exec.Command(executable, "-test.run=^TestPythonApplicationExecutionOwnerDeathStopsRunner934$", "-test.timeout=60s")
			cmd.Env = append(os.Environ(), "LIFE934_OWNER_HELPER="+dir, "LIFE934_OWNER_MODE="+mode)
			cmd.Stdout, cmd.Stderr = log, log
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			ownerDone := make(chan struct{})
			go func() { _ = cmd.Wait(); close(ownerDone) }()
			t.Cleanup(func() {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				pa934Done(t, ownerDone, "owner emergency cleanup")
			})
			launcher, runner := pa934Ready(t, &pa934Transport{dir: dir})
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			pa934Done(t, ownerDone, "killed session owner")
			// Only the Go owner was killed. The intermediate launcher was
			// intentionally left alive, as a Docker daemon/client may be.
			pa934Wait(t, 5*time.Second, "runner stopping after owner SIGKILL", func() bool { return !pa934Running(t, runner) })
			pa934Wait(t, 5*time.Second, "launcher reaping the stopped runner", func() bool { return !pa934Running(t, launcher) })
		})
	}
}

func TestPythonApplicationExecutionParentDeathStopsRunner934(t *testing.T) {
	barrier := make(chan struct{})
	s, key, f, _ := pa934Session(t, t.TempDir(), "busy", true, barrier)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := pa934RunAsync(s, key, ctx)
	start := pa934StartedRun(t, f, r, cancel)
	// Release before emergency cleanup waits on Execute, including on failure.
	t.Cleanup(func() { close(barrier) })
	launcher, runner := pa934Ready(t, f)
	if launcher != start.pid {
		t.Fatal("fixture did not record the actual launcher")
	}
	if err := syscall.Kill(launcher, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	pa934Wait(t, 5*time.Second, "orphan runner stopping with its session still alive", func() bool { return !pa934Running(t, runner) })
	if ctx.Err() != nil {
		t.Fatal("test cancelled the session instead of killing only the parent")
	}
	if _, err := os.Stat(start.command.OutputDir); err != nil {
		t.Fatalf("test removed execution resources before checking parent loss: %v", err)
	}
}
