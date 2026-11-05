package plumbing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The callback runs after the real Go driver starts. Observe that driver's
// actual compiler for the selected package, then cancel while
// that process is live. This is neither pre-canceled discovery nor a sleep
// standing in for compilation. The fixture uses only standard-library modules.
func compilerChild604(ctx context.Context, parent int, pkg string) (int, error) {
	ps := ""
	if runtime.GOOS != "linux" {
		var err error
		ps, err = exec.LookPath("ps")
		if err != nil {
			return 0, err
		}
	}
	for ctx.Err() == nil {
		if runtime.GOOS == "linux" {
			// The Alpine plumbing image needs no procps installation. Only the
			// owned Go driver's direct children are inspected.
			threads, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", parent))
			if err != nil {
				return 0, err
			}
			var children strings.Builder
			for _, thread := range threads {
				// Go may launch a compiler from any of its OS threads.
				data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", parent, thread.Name()))
				if err == nil {
					children.Write(data)
					children.WriteByte(' ')
				}
			}
			for _, child := range strings.Fields(children.String()) {
				pid, err := strconv.Atoi(child)
				if err != nil {
					continue
				}
				command, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
				if err != nil {
					continue // A short-lived child may have exited already.
				}
				argv := strings.Split(string(command), "\x00")
				if len(argv) == 0 || filepath.Base(argv[0]) != "compile" {
					continue
				}
				for i := 1; i+1 < len(argv); i++ {
					if argv[i] == "-p" && argv[i+1] == pkg && syscall.Kill(pid, 0) == nil {
						return pid, nil
					}
				}
			}
		} else {
			out, err := exec.CommandContext(ctx, ps, "-axo", "pid=,ppid=,args=").Output()
			if err != nil {
				return 0, err
			}
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Fields(line)
				if len(fields) < 4 || fields[1] != strconv.Itoa(parent) || filepath.Base(fields[2]) != "compile" || !strings.Contains(line, "-p "+pkg+" ") {
					continue
				}
				pid, err := strconv.Atoi(fields[0])
				if err == nil && syscall.Kill(pid, 0) == nil {
					return pid, nil
				}
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Millisecond):
		}
	}
	return 0, ctx.Err()
}

func TestGoCompilerLifetime604(t *testing.T) {
	cache := t.TempDir()
	compilerControl604(t, cache)
	first := repositoryCase582{"api", "earlier", "TestReady", repositorySource582(`time.Sleep(300*time.Millisecond);t.Fatal("earlier real work 604")`, "")}
	later := compilerCase604("later")
	f := newRepositoryFixture582(t, cache, []repositoryCase582{first, later}, false, nil, nil)
	f.sandbox.ShardTimeout = time.Minute
	r := compilerBase604(t, f)
	repositoryResult582(t, r, f.plan.Tests[0], "failed", "earlier real work 604")
	compilerRecord604(t, r, f.plan, f.plan.Tests[1], "Future604")
	var firstRead, laterCompile repositoryObservation582
	for _, call := range f.runner.observed {
		if firstRead.at.IsZero() && call.stage == "read" {
			firstRead = call
		}
		if laterCompile.at.IsZero() && call.stage == "compile" && slices.Contains(call.argv, "./later") {
			laterCompile = call
		}
	}
	if firstRead.at.IsZero() || laterCompile.at.IsZero() || !firstRead.end.Equal(laterCompile.end) || laterCompile.at.Sub(firstRead.at) < 250*time.Millisecond {
		t.Fatalf("real work did not consume the same later compiler allowance: first=%+v later=%+v", firstRead, laterCompile)
	}
	if firstRead.end.Sub(firstRead.at) > time.Minute || laterCompile.end.Sub(laterCompile.at) >= firstRead.end.Sub(firstRead.at)-250*time.Millisecond {
		t.Fatal("compiler attribution renewed the shared test phase")
	}

	var slow strings.Builder
	slow.WriteString("package fixture\n")
	// Parsing a large, valid production file keeps the compiler observable,
	// without invalid production, external tools, runtime sleeps or dependencies.
	for i := 0; i < 60000; i++ {
		fmt.Fprintf(&slow, "func work604_%d(x int) int { return x + %d }\n", i, i)
	}
	later.part = "web"
	last := repositoryCase582{"zzz", "nested", "TestReady", compilerSource604(`t.Fatal("must remain unstarted")`)}
	g := newRepositoryFixture582(t, cache, []repositoryCase582{first, later, last}, false, map[string]string{"web/later/slow604.go": slow.String()}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var compilerPID atomic.Int64
	wrapper := &compilerRunner604{repositoryRunner582: g.runner}
	wrapper.open = func(e *repositoryEnvironment582, local *localGoTransport) {
		if e.request.Directory != "web" {
			return
		}
		local.onStart = func(call context.Context, argv []string, pid int) {
			if len(argv) < 2 || argv[0] != "go" || argv[1] != "test" || !slices.Contains(argv, "-c") {
				return
			}
			watch, done := context.WithTimeout(call, 15*time.Second)
			defer done()
			child, err := compilerChild604(watch, pid, "example.invalid/web582/later")
			if err != nil {
				t.Errorf("did not observe real in-progress selected-package compiler: %v", err)
			} else {
				compilerPID.Store(int64(child))
			}
			cancel()
		}
	}
	g.sandbox.Repository.Dependencies.Runner = wrapper
	began := time.Now()
	r, problem, _ := g.run(ctx, true)
	if compilerPID.Load() == 0 || !r.Interrupted || problem == "" || r.Passed() {
		t.Fatalf("real compilation cancellation supplied evidence or lost interruption: pid=%d problem=%s\n%s", compilerPID.Load(), problem, r.Summary())
	}
	if time.Since(began) > 45*time.Second {
		t.Fatal("compiler interruption or owned-process cleanup exceeded its bounds")
	}
	repositoryResult582(t, r, g.plan.Tests[0], "failed", "earlier real work 604")
	compilerNoCompletion604(t, r, g.plan.Tests[1])
	compilerNoCompletion604(t, r, g.plan.Tests[2])
	if result515(t, r, g.plan.Tests[2]).Status != "not_run" || len(g.runner.envs) != 2 {
		t.Fatal("cancellation launched a later part")
	}
	for _, env := range g.runner.envs {
		if !env.closed {
			t.Fatal("interrupted compiler lost environment ownership")
		}
	}
	if syscall.Kill(int(compilerPID.Load()), 0) == nil {
		t.Fatal("compiler child survived completed cleanup")
	}
	repositoryRoundTrip582(t, r)
}
