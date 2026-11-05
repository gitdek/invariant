package plumbing

import (
	"bytes"
	"context"
	"encoding/binary"
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

func TestDockerProcessFramingRetainsOpaquePrefix(t *testing.T) {
	frame := func(kind byte, data string) string {
		header := [5]byte{kind}
		binary.BigEndian.PutUint32(header[1:], uint32(len(data)))
		return string(header[:]) + data
	}
	prefix := dockerProcessProtocol + frame('S', "") + frame('V', "\x00\xffopaque\n") + frame('O', "stdout") + frame('E', "stderr")
	for _, tc := range []struct {
		name, suffix string
		valid        bool
	}{
		{"complete", frame('X', "7") + frame('D', ""), true},
		{"missing exit", frame('D', ""), false},
		{"duplicate start", frame('S', ""), false},
		{"invalid exit", frame('X', "exit status 0"), false},
		{"duplicate exit", frame('X', "0") + frame('X', "0"), false},
		{"truncated", frame('X', "0") + "D\x00", false},
		{"trailing", frame('X', "0") + frame('D', "") + frame('V', "extra"), false},
		{"helper error", frame('X', "0") + frame('F', "failed drain"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &dockerRepositoryProcess{output: RepositoryProcessOutput{ExitCode: -1}}
			err := p.decode(strings.NewReader(prefix + tc.suffix))
			if (err == nil) != tc.valid {
				t.Fatalf("wrong transport verdict: %v", err)
			}
			out := p.Snapshot()
			if !out.Started || string(out.Events) != "\x00\xffopaque\n" || string(out.Stdout) != "stdout" || string(out.Stderr) != "stderr" {
				t.Fatalf("lost opaque prefix: %+v", out)
			}
			if tc.valid && (!out.Exited || !out.Drained || out.ExitCode != 7) {
				t.Fatalf("lost status: %+v", out)
			}
		})
	}
}

func TestDockerProcessHelperDrainsAndReaps(t *testing.T) {
	b := newDocker591(t)
	ctx, done := context.WithTimeout(t.Context(), 2*time.Minute)
	defer done()
	q := request484(t, "go")
	environment, err := (&DockerDependencyRunner{}).OpenChecks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	e := environment.(RepositoryProcessTransport)
	t.Cleanup(func() { _ = docker591Close(t, e) })
	source := `package main
import("bytes";"fmt";"os";"os/exec";"time")
func main(){
 child:=exec.Command("/bin/sh","-c","exec sleep 120")
 if err:=child.Start();err!=nil{panic(err)}
 event,err:=os.OpenFile(os.Getenv("PROCESS_EVENTS"),os.O_WRONLY,0);if err!=nil{panic(err)}
 fmt.Fprintln(event,child.Process.Pid)
 for _,stream:=range []*os.File{os.Stdout,os.Stderr,event}{if _,err:=stream.Write(bytes.Repeat([]byte{0,255,10},70000));err!=nil{panic(err)}}
 if len(os.Args)>1{if err:=os.WriteFile("ready",[]byte("ok"),0600);err!=nil{panic(err)};for{time.Sleep(time.Hour)}}
}
`
	main, err := e.Stage(ctx, "probe/main.go", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	binary, err := e.Stage(ctx, "probe/runner", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := e.Run(ctx, []string{"go", "build", "-o", binary, main}); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	for _, cancelRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelRun), func(t *testing.T) {
			argv := []string{binary}
			if cancelRun {
				argv = append(argv, "wait")
			}
			prepared, err := e.Prepare(ctx, RepositoryProcessSpec{Argv: argv, EventEnv: "PROCESS_EVENTS"})
			if err != nil {
				t.Fatal(err)
			}
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			process, err := prepared.Start(runCtx)
			if err != nil {
				t.Fatal(err)
			}
			if cancelRun {
				ready := filepath.Join(b.root, "containers", q.Token, "invariant", "work", q.Directory, "ready")
				docker591Await(t, ctx, func() bool { _, err := os.Stat(ready); return err == nil }, "full transport pipes")
				cancel()
			}
			out, err := process.Wait(runCtx)
			if (err != nil) != cancelRun {
				t.Fatalf("wait: %v", err)
			}
			if !out.Started || !out.Exited || !cancelRun && !out.Drained {
				t.Fatalf("status: %+v", out)
			}
			pid, events, ok := strings.Cut(string(out.Events), "\n")
			if !ok {
				t.Fatal("missing descendant PID")
			}
			n, err := strconv.Atoi(pid)
			if err != nil || n <= 0 {
				t.Fatalf("PID: %q", pid)
			}
			if err := syscall.Kill(n, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("descendant survived Wait: %d %v", n, err)
			}
			want := bytes.Repeat([]byte{0, 255, 10}, 70000)
			stderrOK := bytes.Equal(out.Stderr, want)
			if cancelRun {
				// Docker also reports its helper's interrupted exit on stderr.
				stderrOK = bytes.HasPrefix(out.Stderr, want)
			}
			if !bytes.Equal(out.Stdout, want) || !stderrOK || !bytes.Equal([]byte(events), want) {
				t.Fatalf("large channels changed: stdout=%d stderr=%d events=%d; wait=%v", len(out.Stdout), len(out.Stderr), len(events), err)
			}
			if _, err := prepared.Start(ctx); err == nil {
				t.Fatal("reused process")
			}
			if err := prepared.Close(ctx); (err != nil) != cancelRun {
				t.Fatalf("close lost outcome: %v", err)
			}
		})
	}
	if err := docker591Close(t, e); err == nil {
		t.Fatal("environment erased interrupted process error")
	}
	for _, call := range b.calls() {
		if call.Done || len(call.Args) == 0 || call.Args[0] != "exec" || !strings.Contains(strings.Join(call.Args, " "), "PROCESS_EVENTS") {
			continue
		}
		found := false
		for _, arg := range call.Args {
			found = found || arg == "PWD="+filepath.Join("/invariant/work", q.Directory)
		}
		if !found {
			t.Fatal("direct helper execution did not set its workload PWD")
		}
	}
	b.clean()
}

func TestDockerEnvironmentCleanupPreservesSharedQueue(t *testing.T) {
	b := newDocker591(t)
	queue, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	environment, err := (&DockerDependencyRunner{}).OpenChecks(t.Context(), request484(t, "go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := docker591Close(t, environment); err != nil {
		t.Fatal(err)
	}
	// The watcher still owns this endpoint. Removing a part's storage must
	// leave the shared queue available to other registered builds and clients.
	if info, err := os.Stat(filepath.Join(queue.dir, "queue")); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("environment cleanup removed the live queue: %v", err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(queue.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("queue Close retained its endpoint: %v", err)
	}
	b.clean()
}
