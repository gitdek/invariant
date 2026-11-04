package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This helper exercises opaque transport bytes, never framework events or
// evidence. Every framework test above uses the production testing hook.
const opaqueHelper538 = `package main
import("bufio";"encoding/json";"fmt";"os";"os/exec";"path/filepath";"time")
func silentChild(){
 c:=exec.Command(os.Args[0],"silent-child")
 pipe,err:=c.StdoutPipe();if err!=nil{panic(err)}
 if err:=c.Start();err!=nil{panic(err)}
 ready,err:=bufio.NewReader(pipe).ReadString('\n');if err!=nil{panic(err)}
 pipe.Close()
 fmt.Fprint(os.Stdout,ready)
 fmt.Fprintf(os.Stdout,"CHILD538=%d\n",c.Process.Pid)
}
func main(){
 if len(os.Args)>1&&os.Args[1]=="writer"{
  name:=filepath.Join(os.TempDir(),fmt.Sprintf("writer538-%d",os.Getpid()))
  if err:=os.WriteFile(name,[]byte("actually writing 538"),0600);err!=nil{panic(err)}
  fmt.Fprintln(os.Stdout,"READY538");os.Stdout.Close()
  for{os.WriteFile(name,[]byte(time.Now().String()),0600);time.Sleep(5*time.Millisecond)}
 }
 if len(os.Args)>1&&os.Args[1]=="silent-child"{
  c:=exec.Command(os.Args[0],"writer")
  pipe,err:=c.StdoutPipe();if err!=nil{panic(err)}
  if err:=c.Start();err!=nil{panic(err)}
  if _,err:=bufio.NewReader(pipe).ReadString('\n');err!=nil{panic(err)}
  pipe.Close()
  fmt.Fprintf(os.Stdout,"GRANDCHILD538=%d\n",c.Process.Pid);os.Stdout.Close()
  c.Wait();return
 }
 if len(os.Args)>1&&os.Args[1]=="run-tree"{silentChild();return}
 event,err:=os.OpenFile(os.Getenv("OPAQUE538"),os.O_WRONLY,0);if err!=nil{panic(err)}
 defer event.Close()
 if len(os.Args)>1&&os.Args[1]=="child"{for{time.Sleep(time.Hour)}}
 dir,_:=os.Getwd()
 values:=map[string]string{}
 for _,key:=range []string{"HOME","TMPDIR","GOCACHE","GOMODCACHE","GOPATH"}{values[key]=os.Getenv(key)}
 data,_:=json.Marshal(struct{Args []string; Dir string; Env map[string]string}{os.Args[2:],dir,values})
 fmt.Fprintln(os.Stdout,string(data))
 event.Write([]byte{0,1,2,255,10})
 fmt.Fprint(os.Stdout,"opaque stdout 538\n")
 fmt.Fprint(os.Stderr,"opaque stderr 538\n")
 if os.Args[1]=="silent hold"||os.Args[1]=="silent orphan"{
  silentChild()
  if os.Args[1]=="silent orphan"{return}
  for{time.Sleep(time.Hour)}
 }
 if os.Args[1]=="hold"||os.Args[1]=="orphan"{
  c:=exec.Command(os.Args[0],"child");c.Env=os.Environ();c.Stdout=os.Stdout;c.Stderr=os.Stderr;c.ExtraFiles=[]*os.File{event}
  if err:=c.Start();err!=nil{panic(err)}
  fmt.Fprintf(os.Stdout,"CHILD538=%d\n",c.Process.Pid)
  if os.Args[1]=="orphan"{return}
  for{time.Sleep(time.Hour)}
 }
 os.Exit(5)
}
`

// Observe the real OS tree while its root is still owned. Checking the whole
// group catches compilers and silent grandchildren even after the root exits
// and all three pipes reach EOF. These checks never wait for eventual cleanup:
// the synchronous operation must have stopped and reaped the tree on return.
type processTree538 struct{ pid, group int }

func observeProcessTree538(t *testing.T, pid int) processTree538 {
	t.Helper()
	group, err := syscall.Getpgid(pid)
	if err != nil || group <= 0 || group == syscall.Getpgrp() {
		t.Errorf("process %d has no independently owned process group: group=%d err=%v", pid, group, err)
		group = 0 // Never signal the acceptance runner's own group.
	}
	return processTree538{pid: pid, group: group}
}

func (f *fixture538) observeStart(ctx context.Context, argv []string, pid int) {
	f.processes = append(f.processes, observeProcessTree538(f.t, pid))
	if f.started != nil {
		f.started(ctx, argv, pid)
	}
}

func assertProcessesGone538(t *testing.T, processes []processTree538) {
	t.Helper()
	for _, p := range processes {
		if err := syscall.Kill(p.pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("run returned before process %d was stopped and reaped: %v", p.pid, err)
		}
		if p.group > 0 {
			if err := syscall.Kill(-p.group, 0); !errors.Is(err, syscall.ESRCH) {
				t.Errorf("run returned with descendants in process group %d: %v", p.group, err)
				// Best effort only AFTER recording failure, so a broken build cannot
				// keep writing into this test's temporary directory during cleanup.
				_ = syscall.Kill(-p.group, syscall.SIGKILL)
			}
		}
	}
}

func openTransport538(t *testing.T, f *fixture538, ctx context.Context) RepositoryProcessTransport {
	t.Helper()
	selected, err := (GoRepositoryAcceptanceAdapter{}).Select(ctx, f.part, f.test)
	if err != nil {
		t.Fatal(err)
	}
	in := repositoryInvocation(&Plan{Sources: map[string]string{f.test.File: f.source}, Execution: &Execution{Digest: dependencyHash([]byte("captured execution policy 538"))}}, f.test, selected)
	env, err := f.open(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = env.Close(c)
	})
	return env
}

func awaitOutput538(t *testing.T, ctx context.Context, process RepositoryProcess, marker string) RepositoryProcessOutput {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		out := process.Snapshot()
		text := string(out.Stdout) + string(out.Stderr)
		needle := marker
		if marker == "CHILD538=" {
			// A grandchild marker must not satisfy the child readiness handshake.
			text, needle = "\n"+text, "\n"+marker
		}
		_, after, found := strings.Cut(text, needle)
		if found && (marker != "CHILD538=" || strings.Contains(after, "\n")) {
			return out
		}
		select {
		case <-ctx.Done():
			t.Fatalf("real process never reached %q: %v %+v", marker, ctx.Err(), out)
		case <-tick.C:
		}
	}
}
func childPID538(t *testing.T, out RepositoryProcessOutput) int {
	t.Helper()
	for _, line := range strings.Split(string(out.Stdout), "\n") {
		if value, ok := strings.CutPrefix(line, "CHILD538="); ok {
			n, err := strconv.Atoi(value)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatal("missing real child PID")
	return 0
}
func gone538(t *testing.T, pid int) {
	t.Helper()
	limit := time.Now().Add(3 * time.Second)
	for time.Now().Before(limit) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("actual child %d was not stopped and reaped", pid)
}

func TestLocalGoTransportOwnsChannelsAndResources(t *testing.T) {
	ctx := context538(t)
	f := fixtureFor538(t, t.TempDir(), "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){}\n")
	t.Setenv("GOFLAGS", "-exec=must-not-run-538")
	t.Setenv("GOPROXY", "https://must-not-contact.invalid")
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "user-cache"))
	env := openTransport538(t, f, ctx)
	out, err := env.Run(ctx, []string{"go", "env", "-json", "GOTOOLCHAIN", "GOENV", "GOWORK", "GOFLAGS", "GOPROXY", "GOSUMDB", "GOCACHE", "GOMODCACHE", "GOPATH", "GOTMPDIR"})
	if err != nil {
		t.Fatalf("inherited environment hook or unavailable real Go: %v %s", err, out)
	}
	var values map[string]string
	if err := json.Unmarshal(out, &values); err != nil {
		t.Fatal(err)
	}
	// With GOENV=off, go env -json reports an empty GOENV path.
	for key, want := range map[string]string{"GOTOOLCHAIN": "local", "GOENV": "", "GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"} {
		if values[key] != want {
			t.Fatalf("unsafe %s=%q", key, values[key])
		}
	}
	if strings.Contains(values["GOFLAGS"], "must-not-run") {
		t.Fatal("inherited hooks")
	}
	for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		rel, err := filepath.Rel(f.cache, values[key])
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("%s escaped private test cache: %s", key, values[key])
		}
	}
	for _, name := range []string{"../escape", "/absolute", ".git/config", "a/../../escape", "a\\escape"} {
		if p, err := env.Stage(ctx, name, []byte("bad")); err == nil || p != "" {
			t.Fatalf("unsafe helper stage accepted: %q %q %v", name, p, err)
		}
	}
	helper, err := env.Stage(ctx, "opaque/main.go", []byte(opaqueHelper538))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(helper)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("helper not a private regular file: %v", err)
	}
	if strings.HasPrefix(helper, f.root+string(filepath.Separator)) {
		t.Fatal("helper staged in caller candidate")
	}
	binary := helper + ".bin"
	if out, err := env.Run(ctx, []string{"go", "build", "-o", binary, helper}); err != nil {
		t.Fatalf("actual helper compilation: %v %s", err, out)
	}
	literal := []string{"", "one two", "$(touch_PWN);`literal`", "name=value", "--option", "line\nbreak"}
	spec := RepositoryProcessSpec{Argv: append([]string{binary, "bytes"}, literal...), EventEnv: "OPAQUE538"}
	prepared, err := env.Prepare(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	process, err := prepared.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := process.Wait(ctx)
	assertProcessesGone538(t, f.processes)
	if err != nil || !result.Started || !result.Exited || !result.Drained || result.ExitCode != 5 {
		t.Fatalf("lost ordinary exit: %+v %v", result, err)
	}
	if !bytes.Equal(result.Events, []byte{0, 1, 2, 255, 10}) || string(result.Stderr) != "opaque stderr 538\n" {
		t.Fatalf("transport merged/interpreted byte channels: %+v", result)
	}
	line, rest, ok := strings.Cut(string(result.Stdout), "\n")
	var observed struct {
		Args []string
		Dir  string
		Env  map[string]string
	}
	if !ok || json.Unmarshal([]byte(line), &observed) != nil || !slices.Equal(observed.Args, literal) || rest != "opaque stdout 538\n" {
		t.Fatalf("literal arguments or output changed: %q", result.Stdout)
	}
	if observed.Dir == filepath.Join(f.root, "api") || filepath.Base(observed.Dir) != "api" {
		t.Fatalf("wrong/nonprivate part workdir: %s", observed.Dir)
	}
	for key, value := range observed.Env {
		rel, err := filepath.Rel(f.cache, value)
		if value == "" || err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("%s uses nonprivate storage: %q", key, value)
		}
	}
	if b, err := os.ReadFile(filepath.Join(observed.Dir, "nested", "ready_accept_test.go")); err != nil || string(b) != f.source {
		t.Fatal("workdir is not the private staged part")
	}
	snapshot := process.Snapshot()
	snapshot.Stdout[0] = '!'
	if process.Snapshot().Stdout[0] == '!' {
		t.Fatal("snapshot aliases retained transport bytes")
	}
	probe, err := env.Stage(ctx, "ancestor/child/probe", []byte("regular"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(probe)); err != nil {
		t.Fatal(err)
	}
	outsideHelper := t.TempDir()
	if err := os.Symlink(outsideHelper, filepath.Dir(probe)); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Stage(ctx, "ancestor/child/escape", []byte("bad")); err == nil {
		t.Fatal("staging followed a linked helper ancestor")
	}
	if _, err := os.Stat(filepath.Join(outsideHelper, "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("helper staging escaped its private root")
	}
	for _, name := range []string{"../go.mod", "/etc/passwd", "nested/../../go.mod", ".git/config"} {
		if _, err := env.ReadFile(ctx, name); err == nil {
			t.Fatalf("unsafe read accepted: %q", name)
		}
	}
	// Run also owns the whole tree, including the compiler/test-like descendants
	// of a real Go command, even when silent children no longer hold its pipes.
	runOut, runErr := env.Run(ctx, []string{"go", "run", helper, "run-tree"})
	assertProcessesGone538(t, f.processes)
	if runErr != nil || !strings.Contains(string(runOut), "GRANDCHILD538=") || !strings.Contains(string(runOut), "CHILD538=") {
		t.Fatalf("real Run descendant fixture failed: %v %s", runErr, runOut)
	}
	for _, mode := range []string{"hold", "orphan", "silent hold", "silent orphan"} {
		p, err := env.Prepare(ctx, RepositoryProcessSpec{Argv: []string{binary, mode}, EventEnv: "OPAQUE538"})
		if err != nil {
			t.Fatal(err)
		}
		running, err := p.Start(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ready, done := context.WithTimeout(ctx, 10*time.Second)
		initial := awaitOutput538(t, ready, running, "CHILD538=")
		done()
		pid := childPID538(t, initial)
		wait, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		start := time.Now()
		partial, waitErr := running.Wait(wait)
		assertProcessesGone538(t, f.processes)
		cancel()
		if time.Since(start) > 3*time.Second || !partial.Started || !bytes.Equal(partial.Events, []byte{0, 1, 2, 255, 10}) || strings.HasSuffix(mode, "hold") && waitErr == nil || waitErr == nil && (!partial.Exited || !partial.Drained) {
			t.Fatalf("unbounded/lost partial wait: %s %+v %v", mode, partial, waitErr)
		}
		cleanup, end := context.WithTimeout(context.Background(), 2*time.Second)
		_ = running.Close(cleanup)
		_ = p.Close(cleanup)
		end()
		gone538(t, pid)
	}
	// A prepared handle can exist even when actual executable startup fails.
	missing, err := env.Prepare(ctx, RepositoryProcessSpec{Argv: []string{binary + "-missing"}, EventEnv: "OPAQUE538"})
	if err == nil {
		child, startErr := missing.Start(ctx)
		if startErr == nil {
			t.Fatal("missing executable started")
		}
		if child != nil && child.Snapshot().Started {
			t.Fatal("launch failure invented a running process")
		}
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if p, err := env.Prepare(canceled, spec); err == nil {
		_ = p
		t.Fatal("canceled preparation succeeded")
	}
	cleanup, end := context.WithTimeout(context.Background(), 5*time.Second)
	defer end()
	// Prior interruption/launch errors may be retained by release; the owner
	// still has to remove every acquired private resource, including helpers.
	firstErr := env.Close(cleanup)
	secondErr := env.Close(cleanup)
	if firstErr != nil && secondErr == nil {
		t.Fatal("repeated release discarded prior infrastructure error")
	}
	if _, err := os.Stat(helper); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("helper survived shared Close: %v", err)
	}
	if _, err := os.Stat(observed.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private candidate survived shared Close")
	}
	// Source ancestors are checked before copying, not followed into a foreign
	// checkout. Existing symlink/hard-link leaf cases are in the producer test.
	g := fixtureFor538(t, t.TempDir(), f.source)
	outside := t.TempDir()
	write538(t, outside, "ready_accept_test.go", g.source)
	if err := os.RemoveAll(filepath.Join(g.root, "api", "nested")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(g.root, "api", "nested")); err != nil {
		t.Fatal(err)
	}
	if r, err := g.start(ctx); err == nil || r != nil && r.Process != nil {
		t.Fatal("followed unsafe source ancestor")
	}
	g = fixtureFor538(t, t.TempDir(), f.source)
	if err := syscall.Mkfifo(filepath.Join(g.root, "api", "nested", "blocked.go"), 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := g.start(ctx); err == nil || r != nil && r.Process != nil {
		t.Fatal("special source file was copied or read")
	}
}

func TestGoProducerBoundsCancellationAndPartialStartup(t *testing.T) {
	ctx, cache := context538(t), t.TempDir()
	source := "package fixture\nimport (\"testing\";\"fmt\";\"os\";\"time\")\nfunc TestReady(t *testing.T){fmt.Fprintln(os.Stdout,\"ACTUALLY_RUNNING538\");for{time.Sleep(time.Hour)}}\n"
	// Warm only this isolated cache through an actual successful production run.
	warm := fixtureFor538(t, cache, "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){}\n")
	warm.execute(ctx)
	for _, phase := range []string{"open", "read", "helper", "prepare process", "start"} {
		t.Run(phase, func(t *testing.T) {
			f := fixtureFor538(t, cache, source)
			limited, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			expected, _ := limited.Deadline()
			seen := false
			f.before = func(op context.Context, current string) error {
				d, ok := op.Deadline()
				if !ok || d.After(expected) {
					t.Error("work renewed remaining allowance")
				}
				if current == phase {
					seen = true
					cancel()
					<-op.Done()
					return op.Err()
				}
				return op.Err()
			}
			started := time.Now()
			r, err := f.start(limited)
			if !seen || !errors.Is(err, context.Canceled) || time.Since(started) > 10*time.Second {
				t.Fatalf("%s not bounded by inherited allowance: seen=%v %v", phase, seen, err)
			}
			if r != nil && r.Process != nil && r.Process.Snapshot().Started {
				t.Fatal("interrupted preparation started runtime")
			}
			if r != nil {
				c, done := context.WithTimeout(context.Background(), 2*time.Second)
				_ = r.Close(c)
				done()
			}
		})
	}
	// Cancel only after the actual package loader/compiler has an OS PID. No
	// synthetic go list response, fake wait or already-canceled child suffices.
	for _, operation := range []string{"list", "test"} {
		t.Run("actual "+operation, func(t *testing.T) {
			f := fixtureFor538(t, cache, source)
			running, cancel := context.WithCancel(ctx)
			defer cancel()
			observed := 0
			f.started = func(op context.Context, argv []string, pid int) {
				if len(argv) > 1 && argv[1] == operation && observed == 0 {
					observed = pid
					cancel()
				}
			}
			start := time.Now()
			r, err := f.start(running)
			if observed <= 0 || !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
				t.Fatalf("actual %s was not canceled/reaped: pid=%d %v", operation, observed, err)
			}
			if r != nil && r.Process != nil {
				t.Fatal("canceled discovery launched producer")
			}
			if r != nil {
				c, done := context.WithTimeout(context.Background(), 2*time.Second)
				_ = r.Close(c)
				done()
			}
			gone538(t, observed)
		})
	}
	f := fixtureFor538(t, cache, source)
	running, cancel := context.WithCancel(ctx)
	r, err := f.start(running)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	ready, stopReady := context.WithTimeout(ctx, 10*time.Second)
	awaitOutput538(t, ready, r.Process, "ACTUALLY_RUNNING538")
	stopReady()
	start := time.Now()
	cancel()
	got, err := r.Wait(running)
	assertProcessesGone538(t, f.processes)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 3*time.Second {
		t.Fatalf("actual running test was not interrupted: %v", err)
	}
	if !slices.Equal(actions538(got, "TestReady"), []string{"start"}) || !strings.Contains(string(got.Output.Stdout), "ACTUALLY_RUNNING538") {
		t.Fatalf("cancellation invented completion or lost running diagnostics: %+v", got)
	}
	cleanup, done := context.WithTimeout(context.Background(), 2*time.Second)
	_ = r.Close(cleanup)
	done()
	// A previous successful selector cannot renew a shared test-phase context.
	shared, finish := context.WithTimeout(ctx, 15*time.Second)
	defer finish()
	deadline, _ := shared.Deadline()
	first := fixtureFor538(t, cache, warm.source)
	observe := func(op context.Context, _ string) error {
		d, ok := op.Deadline()
		if !ok || d.After(deadline) {
			t.Error("later selector reset shared allowance")
		}
		return op.Err()
	}
	first.before = observe
	first.execute(shared)
	second := fixtureFor538(t, cache, source)
	second.before = observe
	rr, startErr := second.start(shared)
	if startErr == nil {
		_, err = rr.Wait(shared)
		assertProcessesGone538(t, second.processes)
	} else {
		err = startErr
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second selector escaped original phase deadline: %v", err)
	}
	// Every nonnil handle returned with error remains owned, including a real
	// started process. These wrappers change only infrastructure failures.
	for _, phase := range []string{"open", "prepare process", "start", "close"} {
		t.Run("partial "+phase, func(t *testing.T) {
			f := fixtureFor538(t, cache, source)
			f.partial = phase
			run, err := f.start(ctx)
			if phase != "close" && !errors.Is(err, injected538) {
				t.Fatalf("partial %s lost error: %v", phase, err)
			}
			if run == nil || len(f.environments) != 1 {
				t.Fatal("acquired environment was left unowned")
			}
			// Record an extra staged resource to prove the same owner releases it.
			resource, stageErr := f.environments[0].Stage(ctx, "owned/resource", []byte("owned"))
			if stageErr != nil {
				t.Fatal(stageErr)
			}
			c, end := context.WithTimeout(context.Background(), 2*time.Second)
			closeErr := run.Close(c)
			again := run.Close(c)
			end()
			if !errors.Is(closeErr, injected538) || !errors.Is(again, injected538) {
				t.Fatalf("release discarded infrastructure error: %v / %v", closeErr, again)
			}
			if _, err := os.Stat(resource); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("part Close leaked a partial-start resource")
			}
		})
	}
	// A shorter cleanup context is honored, and cancellation of execution does
	// not cancel the fresh context passed to the environment's Close.
	if len(f.environments) == 0 {
		t.Fatal("runtime fixture did not acquire an environment")
	}
}
