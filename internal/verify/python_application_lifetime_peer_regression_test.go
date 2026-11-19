package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPythonApplicationExecutionLifetimeStoppedPeerAfterRunnerDeath(t *testing.T) {
	for _, mode := range []string{"cancel", "Close", "module deadline"} {
		for stopped := 0; stopped < 2; stopped++ {
			t.Run(fmt.Sprintf("%s/peer=%d", mode, stopped), func(t *testing.T) {
				driver, source := pa934Blocking+pa318Functional, pa321State+pa321Worker
				if mode == "module deadline" {
					// Start the step only after both helpers are stopped, so
					// startup time cannot consume the step's deadline.
					driver = `import invariant_observe, os, time
output = os.path.dirname(invariant_observe._collector.request['output'])
with open(os.path.join(output, 'ready'), 'w') as stream:
    stream.write(str(os.getpid()))
while not os.path.exists(os.path.join(output, 'release')):
    time.sleep(0.01)
` + pa321Deadline
					source = pa321State + `import sys
def advance(key, status):
    finish(key, status)
def submit(request, *, status):
    sys.setswitchinterval(600)
    sum(range(1 << 62))
`
				}
				_, p := pa321Fixture(t, source, driver)
				opts := pa318Options(t, false, new(int))
				execute := opts.Execute
				started := make(chan PythonApplicationCommand, 1)
				opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
					started <- c
					// pa318Local captures stdout and stderr through pipes.
					// Wait must join their readers after the runner exits.
					return execute(ctx, pa934Instrument(c, -1, false))
				}
				s := pa315Prepare(t, p, opts)
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				r := pa934RunAsync(s, pa318Driver(t, s).Key, ctx)
				var c PythonApplicationCommand
				select {
				case c = <-started:
				case <-r.done:
					t.Fatalf("runner did not start: %+v, %v", r.result, r.err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				var runner int
				var watchdogs []int
				t.Cleanup(func() {
					cancel()
					// Cleanup follows assertions and also frees a stopped
					// peer on the broken implementation, without hiding it.
					if len(watchdogs) == 0 {
						b, _ := os.ReadFile(filepath.Join(c.OutputDir, "watchdogs"))
						_ = json.Unmarshal(b, &watchdogs)
					}
					for _, pid := range append(watchdogs, runner) {
						if pid > 1 {
							_ = syscall.Kill(pid, syscall.SIGKILL)
						}
					}
					pa934Done(t, r.done, "stopped-peer emergency cleanup")
				})
				pa934Wait(t, 30*time.Second, "runner and both lifetime helpers", func() bool {
					b, err := os.ReadFile(filepath.Join(c.OutputDir, "ready"))
					if err != nil {
						return false
					}
					runner, err = strconv.Atoi(string(b))
					if err != nil || runner <= 1 {
						return false
					}
					b, err = os.ReadFile(filepath.Join(c.OutputDir, "watchdogs"))
					return err == nil && json.Unmarshal(b, &watchdogs) == nil && len(watchdogs) == 2 && watchdogs[0] > 1 && watchdogs[1] > 1
				})
				// Hold the other helper too until the runner has died. This
				// forces cleanup to begin after reparenting rather than racing
				// the runner's exit. Only the other helper is then resumed.
				for _, pid := range watchdogs {
					if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
						t.Fatal(err)
					}
				}
				closed := make(chan struct{})
				var closeErr error
				closeSession := func() { closeErr = s.Close(); close(closed) }
				switch mode {
				case "cancel":
					cancel()
				case "Close":
					go closeSession()
				case "module deadline":
					if err := os.WriteFile(filepath.Join(c.OutputDir, "release"), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if !t.Run("runner exits", func(t *testing.T) {
					pa934Wait(t, 5*time.Second, "runner stopping with both helpers suspended", func() bool {
						return !pa934Running(t, runner)
					})
				}) {
					return
				}
				select {
				case <-r.done:
					t.Fatal("Run returned while lifetime helpers still held its output pipes")
				default:
				}
				if err := syscall.Kill(watchdogs[1-stopped], syscall.SIGCONT); err != nil {
					t.Fatal(err)
				}
				if !t.Run("Run joins", func(t *testing.T) { pa934Rejected(t, r) }) {
					return
				}
				if !t.Run("helpers exit", func(t *testing.T) {
					// Pipe EOF and lock release can precede the kernel marking
					// an exiting orphan as a zombie or reaping it.
					pa934Wait(t, 5*time.Second, "both lifetime helpers exiting", func() bool {
						return !pa934Running(t, watchdogs[0]) && !pa934Running(t, watchdogs[1])
					})
				}) {
					return
				}
				if !t.Run("failure cause", func(t *testing.T) {
					if mode == "module deadline" {
						if ctx.Err() != nil || !strings.Contains(strings.Join(r.result.Diagnostics, "\n"), "module observation timed out") {
							t.Fatalf("step deadline did not stop the runner: %+v, %v, %v", r.result, r.err, ctx.Err())
						}
					} else if !errors.Is(r.err, context.Canceled) {
						t.Fatalf("cancellation cause was lost: %v", r.err)
					}
				}) {
					return
				}
				if mode != "Close" {
					go closeSession()
				}
				t.Run("Close joins", func(t *testing.T) {
					pa934Done(t, closed, "Close after runner death with a stopped peer")
					if closeErr != nil {
						t.Fatal(closeErr)
					}
				})
			})
		}
	}
}
