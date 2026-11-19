package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// Faults are injected by the process launching the unmodified runner, before
// any application import. No test replaces lifetime protection with a fake.
const pa934Bootstrap = `import atexit, ctypes, inspect, json, os, runpy, signal, sys
runner = sys.argv[1]
sys.argv = sys.argv[1:]
with open(sys.argv[1]) as stream:
    output = os.path.dirname(json.load(stream)['output'])
load = ctypes.CDLL
def library(*args, **kwargs):
    native = load(*args, **kwargs)
    popen = native.popen
    popen.argtypes, popen.restype = [ctypes.c_char_p, ctypes.c_char_p], ctypes.c_void_p
    started = []
    def start(*args):
        if len(started) == FAIL_FORK:
            return None
        handle = popen(*args)
        started.append(handle)
        return handle
    native.popen = start
    if FAIL_FORK == -2:
        native.__cxa_atexit = lambda *args: -1
    return native
ctypes.CDLL = library
register = atexit.register
def registered(fn, *args, **kwargs):
    if fn.__name__ == 'finish':
        children = inspect.getclosurevars(fn).nonlocals['children']
        with open(os.path.join(output, 'watchdogs'), 'w') as stream:
            json.dump(children, stream)
        if FAIL_CLEANUP:
            owner = os.getpid()
            def broken():
                if os.getpid() == owner:
                    os.kill(children[0], signal.SIGKILL)
                fn()
            return register(broken)
    return register(fn, *args, **kwargs)
atexit.register = registered
runpy.run_path(runner, run_name='__main__')
`

func pa934Instrument(c PythonApplicationCommand, fork int, cleanup bool) PythonApplicationCommand {
	program := strings.ReplaceAll(pa934Bootstrap, "FAIL_FORK", strconv.Itoa(fork))
	value := "False"
	if cleanup {
		value = "True"
	}
	program = strings.ReplaceAll(program, "FAIL_CLEANUP", value)
	c.Args = append([]string{"-S", "-s", "-P", "-B", "-c", program, c.Launcher}, c.Args[5:]...)
	return c
}

func TestPythonApplicationExecutionLifetimeFailures(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		for _, mode := range []string{"normal", "failed driver", "first watchdog", "second watchdog", "cleanup registration", "cleanup", "reaping failure", "lease startup", "lease renewal", "watchdog died", "watchdog stopped"} {
			t.Run(fmt.Sprintf("docker=%t/%s", real, mode), func(t *testing.T) {
				driver := pa318Functional
				if mode == "failed driver" {
					driver += "\nraise RuntimeError('expected failure')\n"
				}
				if mode == "lease renewal" {
					driver += "\nimport time\ntime.sleep(600)\n"
				}
				if mode == "reaping failure" {
					driver += "\nimport signal\nsignal.signal(signal.SIGCHLD, signal.SIG_IGN)\n"
				}
				if strings.HasPrefix(mode, "watchdog ") {
					signal := "SIGKILL"
					if mode == "watchdog stopped" {
						signal = "SIGSTOP"
					}
					driver += `
import json, os, signal, sys
import invariant_observe
output = os.path.dirname(invariant_observe._collector.request['output'])
with open(os.path.join(output, 'watchdogs')) as stream:
    watchdogs = json.load(stream)
assert len(watchdogs) == 2
os.kill(watchdogs[0], signal.` + signal + `)
sys.setswitchinterval(600)
sum(range(1 << 62))
`
				}
				_, p := pa318Fixture(t, false, driver, false)
				opts := pa318Options(t, real, new(int))
				execute := opts.Execute
				if execute == nil {
					execute = RunPythonApplicationCommand
				}
				var process PythonApplicationProcess
				var command PythonApplicationCommand
				helpersReaped := false
				opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
					command = c
					failFork := -1
					if mode == "first watchdog" {
						failFork = 0
					} else if mode == "second watchdog" {
						failFork = 1
					} else if mode == "cleanup registration" {
						failFork = -2
					}
					if mode == "lease startup" {
						lease := filepath.Join(c.RuntimeDir, "lifetime")
						if err := os.Remove(lease); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(lease, 0700); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "lease renewal" {
						// The updater cannot replace a directory with its next lease.
						if err := os.Mkdir(filepath.Join(c.RuntimeDir, "lifetime.next"), 0700); err != nil {
							t.Fatal(err)
						}
					}
					var err error
					process, err = execute(ctx, pa934Instrument(c, failFork, mode == "cleanup"))
					if mode == "normal" || mode == "failed driver" {
						status, e := os.ReadFile(filepath.Join(c.OutputDir, "lifetime.lock"))
						helpersReaped = e == nil && string(status) == "S11"
						if !real {
							var children []int
							b, e := os.ReadFile(filepath.Join(c.OutputDir, "watchdogs"))
							if e != nil || json.Unmarshal(b, &children) != nil || len(children) != 2 {
								t.Fatalf("missing helper identities: %s, %v", b, e)
							}
							for _, pid := range children {
								// ESRCH also refuses unreaped zombies on Linux.
								helpersReaped = helpersReaped && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
							}
						}
					}
					return process, err
				}
				s := pa315Prepare(t, p, opts)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				r, err := s.Run(ctx, pa318Driver(t, s).Key)
				if mode == "normal" {
					if err != nil || r == nil || !r.Success || !helpersReaped {
						t.Fatalf("normal run did not reap helpers: %+v, %v, %+v", r, err, process)
					}
				} else {
					pa318Reject(t, r, err)
					if mode == "failed driver" && !helpersReaped {
						t.Fatalf("failed driver did not reap helpers: %+v", process)
					}
				}
				if ctx.Err() != nil {
					t.Fatalf("lifetime failure depended on outer timeout: %v", ctx.Err())
				}
				if _, err := os.Stat(command.OutputDir); !os.IsNotExist(err) {
					t.Fatalf("execution retained private output: %v", err)
				}
				if _, err := os.Stat(filepath.Join(command.RuntimeDir, "lifetime")); !os.IsNotExist(err) {
					t.Fatalf("execution retained its lease: %v", err)
				}
			})
		}
	}
}

const pa934Blocking = `
import invariant_observe, os, signal, sys, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
output = os.path.dirname(invariant_observe._collector.request['output'])
with open(os.path.join(output, 'ready'), 'w') as stream:
    stream.write(str(os.getpid()))
sys.setswitchinterval(600)
sum(range(1 << 62))
`

func TestPythonApplicationExecutionLifetimeConcurrentClose(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		t.Run(fmt.Sprintf("docker=%t", real), func(t *testing.T) {
			_, p := pa318Fixture(t, false, pa934Blocking+pa318Functional, false)
			opts := pa318Options(t, real, new(int))
			execute := opts.Execute
			if execute == nil {
				execute = RunPythonApplicationCommand
			}
			started := make(chan PythonApplicationCommand, 1)
			opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				started <- c
				return execute(ctx, c)
			}
			s := pa315Prepare(t, p, opts)
			key := pa318Driver(t, s).Key
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			r := pa934RunAsync(s, key, ctx)
			c := <-started
			pa934Wait(t, 30*time.Second, "blocking import", func() bool {
				_, err := os.Stat(filepath.Join(c.OutputDir, "ready"))
				return err == nil
			})
			// Queue another invocation before concurrent closers. Neither may
			// run after Close has requested cancellation of the first one.
			queued := pa934RunAsync(s, key, ctx)
			closed := make(chan error, 8)
			for i := 0; i < cap(closed); i++ {
				go func() { closed <- s.Close() }()
			}
			for i := 0; i < cap(closed); i++ {
				select {
				case err := <-closed:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("concurrent Close did not join the runner")
				}
			}
			pa934Rejected(t, r)
			pa934Done(t, queued.done, "queued invocation")
			if queued.err == nil || queued.result != nil {
				t.Fatalf("queued invocation ran after Close: %+v, %v", queued.result, queued.err)
			}
			if !errors.Is(r.err, context.Canceled) || ctx.Err() != nil {
				t.Fatalf("Close did not supply cancellation: %v, %v", r.err, ctx.Err())
			}
			select {
			case <-started:
				t.Fatal("closed session launched a queued invocation")
			default:
			}
		})
	}
}

func TestPythonApplicationExecutionLifetimeSimultaneousStop(t *testing.T) {
	// Both watchdogs can notice the same withdrawal at once. Neither may
	// terminate its peer before the runner has been stopped, even when the
	// transport's cancellation kills only the intermediate launcher.
	for attempt := 0; attempt < 8; attempt++ {
		t.Run(strconv.Itoa(attempt), func(t *testing.T) {
			s, key, f, _ := pa934Session(t, t.TempDir(), "busy", true, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := pa934RunAsync(s, key, ctx)
			pa934StartedRun(t, f, r, cancel)
			_, runner := pa934Ready(t, f)
			cancel()
			pa934Rejected(t, r)
			if pa934Running(t, runner) {
				t.Fatal("Run returned before the cancelled runner stopped")
			}
		})
	}
}

func TestPythonApplicationExecutionLifetimeUnreapedParent(t *testing.T) {
	s, key, f, _ := pa934Session(t, t.TempDir(), "busy", true, nil)
	execute := s.execute
	s.execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
		// Keep the runner's parent as a zombie: its own parent deliberately
		// never waits for it, while the session and its lease remain alive.
		program := `import os, subprocess, sys, time
parent = subprocess.Popen([sys.executable, '-I', '-S', '-c',
    'import subprocess, sys; p = subprocess.Popen(sys.argv[1:]); sys.exit(p.wait())',
    *sys.argv[1:]])
with open(os.environ['LIFE934_PIDS'] + '.unreaped', 'w') as stream:
    stream.write(str(parent.pid))
time.sleep(600)
`
		c.Args = append([]string{"-I", "-S", "-c", program, f.python}, c.Args...)
		return execute(ctx, c)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := pa934RunAsync(s, key, ctx)
	start := pa934StartedRun(t, f, r, cancel)
	var parent, runner int
	pa934Wait(t, 30*time.Second, "runner with an unreaped parent", func() bool {
		p, pe := os.ReadFile(filepath.Join(f.dir, "pids.unreaped"))
		b, be := os.ReadFile(filepath.Join(f.dir, "ready"))
		if pe != nil || be != nil {
			return false
		}
		parent, pe = strconv.Atoi(string(p))
		runner, be = strconv.Atoi(string(b))
		return pe == nil && be == nil && parent > 1 && runner > 1
	})
	if err := syscall.Kill(parent, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	pa934Wait(t, 5*time.Second, "runner stopping before its parent is reaped", func() bool {
		return !pa934Running(t, runner)
	})
	select {
	case <-r.done:
		t.Fatal("execution ended instead of retaining the unreaped parent")
	default:
	}
	if _, err := os.Stat(start.command.OutputDir); err != nil || ctx.Err() != nil {
		t.Fatalf("execution resources were withdrawn: %v, %v", err, ctx.Err())
	}
	cancel()
	pa934Rejected(t, r)
}

// The owner helper intentionally leaves the Docker daemon and container
// alive when SIGKILL removes the Go process. A host PID check cannot protect
// this case; the mounted lease must expire inside the running container.
func TestPythonApplicationExecutionLifetimeDockerOwnerDeath(t *testing.T) {
	if dir := os.Getenv("PA934_DOCKER_OWNER"); dir != "" {
		t.Setenv("TMPDIR", dir)
		_, p := pa318Fixture(t, false, pa934Blocking+pa318Functional, false)
		opts := pa318Options(t, true, new(int))
		opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
			c.Env["PA934_CONTAINER"] = filepath.Base(filepath.Dir(dir)) + "-" + filepath.Base(dir)
			if err := os.WriteFile(filepath.Join(dir, "output"), []byte(c.OutputDir), 0600); err != nil {
				t.Fatal(err)
			}
			return RunPythonApplicationCommand(ctx, c)
		}
		s := pa315Prepare(t, p, opts)
		_, _ = s.Run(context.Background(), pa318Driver(t, s).Key)
		return
	}
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
		t.Skip("set INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL=1 for Docker owner death")
	}
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestPythonApplicationExecutionLifetimeDockerOwnerDeath$", "-test.timeout=90s")
	cmd.Env = append(os.Environ(), "PA934_DOCKER_OWNER="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	log, err := os.Create(filepath.Join(dir, "owner.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	var container string
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		pa934Done(t, done, "Docker owner cleanup")
		if container != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = toolchain.Docker(ctx, "rm", "-f", container).Run()
		}
	})
	pa934Wait(t, 60*time.Second, "container blocking during import", func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "output"))
		if err != nil {
			return false
		}
		_, err = os.Stat(filepath.Join(string(b), "ready"))
		return err == nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := toolchain.Docker(ctx, "ps", "-q").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range strings.Fields(string(b)) {
		env, err := toolchain.Docker(ctx, "inspect", "--format", "{{json .Args}}", id).Output()
		if err == nil && strings.Contains(string(env), "PA934_CONTAINER="+filepath.Base(filepath.Dir(dir))+"-"+filepath.Base(dir)) {
			container = id
			break
		}
	}
	if container == "" {
		t.Fatal("could not identify the live fixture container")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	pa934Done(t, done, "killed Docker owner")
	pa934Wait(t, 5*time.Second, "container stopping after owner SIGKILL", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		b, err := toolchain.Docker(ctx, "ps", "-q", "--filter", "id="+container).Output()
		return err == nil && strings.TrimSpace(string(b)) == ""
	})
}
