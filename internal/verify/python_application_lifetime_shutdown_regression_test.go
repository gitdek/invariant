package verify

import (
	"context"
	"encoding/json"
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

const pa934ShutdownDriver = `import atexit, invariant_observe, os, signal, sys, time
output = os.path.dirname(invariant_observe._collector.request['output'])
mode = os.environ['PA934_SHUTDOWN_MODE']
signal.signal(signal.SIGTERM, signal.SIG_IGN)
sys.setswitchinterval(600)
if mode.startswith('fork'):
    expected = int(os.environ.get('PA934_CHILD_STATUS', '7'))
    child = os.fork()
    if child == 0:
        # A second fork must not close application descriptors that reused
        # the lifetime descriptors discarded by the first fork.
        reader, writer = os.pipe()
        grandchild = os.fork()
        if grandchild == 0:
            os.write(writer, b'child')
            sys.exit(0)
        os.close(writer)
        assert os.read(reader, 5) == b'child'
        os.close(reader)
        _, nested = os.waitpid(grandchild, 0)
        assert os.WIFEXITED(nested) and os.WEXITSTATUS(nested) == 0, nested
        sys.exit(expected)
    _, status = os.waitpid(child, 0)
    assert os.WIFEXITED(status) and os.WEXITSTATUS(status) == expected, status
    if mode == 'fork-busy':
        with open(os.path.join(output, 'ready'), 'w') as stream:
            stream.write(str(os.getpid()))
        sum(range(1 << 62))
if mode.startswith('finalization'):
    def exiting(path=os.path.join(output, 'atexit')):
        with open(path, 'w') as stream:
            stream.write('application atexit completed')
    atexit.register(exiting)
    class Finalizer:
        def __del__(self, path=os.path.join(output, 'ready'), pid=os.getpid(),
                    finalizing=sys.is_finalizing, write=open, sleep=time.sleep,
                    total=sum, sequence=range, mode=mode):
            with write(path, 'w') as stream:
                stream.write(str(pid) if finalizing() else 'finalized too early')
            if mode == 'finalization-busy':
                total(sequence(1 << 62))
            else:
                sleep(6 if mode == 'finalization-slow' else 600)
    # Keep the object in a module until interpreter shutdown, after atexit.
    sys._invariant_finalizer934 = Finalizer()
`

func TestPythonApplicationExecutionLifetimeShutdownOutcomes(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		for _, mode := range []string{"fork-zero", "fork-nonzero", "fork-failed", "finalization-slow"} {
			t.Run(fmt.Sprintf("docker=%t/%s", real, mode), func(t *testing.T) {
				driver := pa934ShutdownDriver + pa318Functional
				if mode == "fork-failed" {
					driver += "\nraise RuntimeError('expected failure after child exit')\n"
				}
				_, p := pa318Fixture(t, false, driver, false)
				opts := pa318Options(t, real, new(int))
				execute := opts.Execute
				if execute == nil {
					execute = RunPythonApplicationCommand
				}
				opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
					c.Env["PA934_SHUTDOWN_MODE"] = mode
					if mode == "fork-zero" {
						c.Env["PA934_CHILD_STATUS"] = "0"
					}
					process, err := execute(ctx, c)
					if mode == "finalization-slow" {
						for _, name := range []string{"atexit", "ready"} {
							b, readErr := os.ReadFile(filepath.Join(c.OutputDir, name))
							if readErr != nil || strings.Contains(string(b), "too early") {
								t.Fatalf("missing late finalization: %s, %v", b, readErr)
							}
						}
					}
					return process, err
				}
				s := pa315Prepare(t, p, opts)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				r, err := s.Run(ctx, pa318Driver(t, s).Key)
				if mode == "fork-failed" {
					pa318Reject(t, r, err)
				} else if err != nil || r == nil || !r.Success || !r.Complete || r.ExitCode != 0 || len(r.Observations) != 3 {
					t.Fatalf("shutdown changed the ordinary outcome: %+v, %v", r, err)
				}
				if ctx.Err() != nil {
					t.Fatal(ctx.Err())
				}
			})
		}
	}
}

func TestPythonApplicationExecutionLifetimeShutdownOwnerDeath(t *testing.T) {
	if dir := os.Getenv("PA934_SHUTDOWN_OWNER"); dir != "" {
		t.Setenv("TMPDIR", dir)
		real := os.Getenv("PA934_SHUTDOWN_REAL") == "true"
		_, p := pa318Fixture(t, false, pa934ShutdownDriver+pa318Functional, false)
		opts := pa318Options(t, real, new(int))
		execute := RunPythonApplicationCommand
		if !real {
			python, err := exec.LookPath("python3")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "libraries"), 0700); err != nil {
				t.Fatal(err)
			}
			transport := &pa934Transport{dir: dir, python: python, mode: "normal", started: make(chan pa934Started, 1)}
			execute = transport.execute
		}
		opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
			c.Env["PA934_SHUTDOWN_MODE"] = os.Getenv("PA934_SHUTDOWN_MODE")
			c.Env["PA934_CONTAINER"] = filepath.Base(filepath.Dir(dir)) + "-" + filepath.Base(dir)
			if err := os.WriteFile(filepath.Join(dir, "output"), []byte(c.OutputDir), 0600); err != nil {
				t.Fatal(err)
			}
			return execute(ctx, pa934Instrument(c, -1, false))
		}
		s := pa315Prepare(t, p, opts)
		_, _ = s.Run(context.Background(), pa318Driver(t, s).Key)
		return
	}
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		for _, mode := range []string{"fork-busy", "finalization-sleep", "finalization-busy"} {
			t.Run(fmt.Sprintf("docker=%t/%s", real, mode), func(t *testing.T) {
				dir := t.TempDir()
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(executable, "-test.run=^TestPythonApplicationExecutionLifetimeShutdownOwnerDeath$", "-test.timeout=90s")
				cmd.Env = append(os.Environ(), "PA934_SHUTDOWN_OWNER="+dir,
					"PA934_SHUTDOWN_MODE="+mode, "PA934_SHUTDOWN_REAL="+strconv.FormatBool(real))
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
					pa934Done(t, done, "shutdown owner cleanup")
					if container != "" {
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						_ = toolchain.Docker(ctx, "rm", "-f", container).Run()
					}
				})
				var output string
				var runner int
				pa934Wait(t, 60*time.Second, "runner reaching "+mode, func() bool {
					b, err := os.ReadFile(filepath.Join(dir, "output"))
					if err != nil {
						return false
					}
					output = string(b)
					b, err = os.ReadFile(filepath.Join(output, "ready"))
					if err != nil {
						return false
					}
					runner, err = strconv.Atoi(string(b))
					return err == nil && runner > 1
				})
				if strings.HasPrefix(mode, "finalization") {
					if _, err := os.Stat(filepath.Join(output, "atexit")); err != nil {
						t.Fatalf("destructor did not run after atexit: %v", err)
					}
				}
				var launcher int
				var watchdogs []int
				if real {
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
						t.Fatal("could not identify the live shutdown fixture container")
					}
				} else {
					b, err := os.ReadFile(filepath.Join(output, "watchdogs"))
					if err != nil || json.Unmarshal(b, &watchdogs) != nil || len(watchdogs) != 2 {
						t.Fatalf("missing helper identities: %s, %v", b, err)
					}
					b, err = os.ReadFile(filepath.Join(dir, "pids"))
					var launched int
					if _, scanErr := fmt.Sscanf(string(b), "%d %d", &launcher, &launched); err != nil || scanErr != nil || launched != runner {
						t.Fatalf("missing surviving launcher: %s, %v, %v", b, err, scanErr)
					}
					for _, pid := range append(watchdogs, launcher, runner) {
						if !pa934Running(t, pid) {
							t.Fatalf("process %d died before owner SIGKILL", pid)
						}
					}
				}
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				pa934Done(t, done, "killed shutdown owner")
				pa934Wait(t, 5*time.Second, "shutdown runner and helpers stopping after owner SIGKILL", func() bool {
					if real {
						ctx, cancel := context.WithTimeout(context.Background(), time.Second)
						defer cancel()
						b, err := toolchain.Docker(ctx, "ps", "-q", "--filter", "id="+container).Output()
						return err == nil && strings.TrimSpace(string(b)) == ""
					}
					for _, pid := range append(watchdogs, runner, launcher) {
						if pa934Running(t, pid) {
							return false
						}
					}
					return true
				})
			})
		}
	}
}
