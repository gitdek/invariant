package plumbing

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDockerProcessFramingPreservesPrefix(t *testing.T) {
	frame := func(kind byte, data []byte) []byte {
		header := make([]byte, 5)
		header[0] = kind
		binary.BigEndian.PutUint32(header[1:], uint32(len(data)))
		return append(header, data...)
	}
	opaque := append([]byte{0, 255}, []byte(dockerProcessProtocol+"--- FAIL: TestReady\n")...)
	prefix := append([]byte(dockerProcessProtocol), frame('s', nil)...)
	prefix = append(prefix, frame('v', opaque)...)
	prefix = append(prefix, frame('o', opaque)...)
	prefix = append(prefix, frame('e', opaque)...)
	prefix = append(prefix, frame('x', []byte("7"))...)
	for _, suffix := range [][]byte{frame('d', nil), frame('d', nil)[:4], frame('s', nil), frame('x', []byte("0")), frame('?', nil), {'o', 255, 255, 255, 255}} {
		p := &dockerRepositoryProcess{done: make(chan struct{}), output: RepositoryProcessOutput{ExitCode: -1}, client: &dockerCheckClient{done: make(chan struct{})}}
		// Every byte boundary can be a Docker read boundary, including framing.
		for _, b := range append(bytes.Clone(prefix), suffix...) {
			if _, err := p.Write([]byte{b}); err != nil {
				t.Fatal(err)
			}
		}
		close(p.client.done)
		p.collect()
		out := p.Snapshot()
		if !out.Started || !out.Exited || out.ExitCode != 7 || !bytes.Equal(out.Events, opaque) || !bytes.Equal(out.Stdout, opaque) || !bytes.Equal(out.Stderr, opaque) {
			t.Fatalf("lost opaque prefix: %+v", out)
		}
		valid := bytes.Equal(suffix, frame('d', nil))
		if (p.err == nil) != valid || out.Drained != valid {
			t.Fatalf("wrong framing verdict for %v: %v %+v", suffix, p.err, out)
		}
	}
}

func TestDockerNonGoKeepsCommandCapability(t *testing.T) {
	_, logs := docker484(t)
	for _, language := range []string{"python", "typescript"} {
		e, err := (&DockerDependencyRunner{}).OpenChecks(t.Context(), request484(t, language))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := e.(RepositoryProcessTransport); ok {
			t.Fatal("non-Go environment acquired a Go process capability")
		}
		if _, ok := e.(RepositoryAcceptanceEnvironment); ok {
			t.Fatal("non-Go environment acquired a Go collector")
		}
		if out, err := e.Run(t.Context(), []string{"fixture-check"}); err != nil || !strings.Contains(string(out), "container diagnostic") {
			t.Fatalf("command behavior changed: %v %s", err, out)
		}
		close484(t, e)
	}
	for _, args := range invocations484(t, logs) {
		for _, arg := range args {
			if strings.Contains(arg, "/invariant/process-") {
				t.Fatal("non-Go command staged a helper")
			}
		}
	}
}

func TestDockerProcessJoinsSilentDescendants(t *testing.T) {
	b := newDocker591(t)
	for _, mode := range []string{"exit", "cancel", "close", "environment close"} {
		t.Run(mode, func(t *testing.T) {
			q := request484(t, "go")
			env, err := (&DockerDependencyRunner{}).OpenChecks(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = docker591Close(t, env) })
			e := env.(RepositoryProcessTransport)
			source := `package main
import("fmt";"os";"os/exec";"time")
func main(){
 if os.Args[1]=="child" { for { time.Sleep(time.Hour) } }
 child:=exec.Command(os.Args[0],"child")
 if err:=child.Start();err!=nil{panic(err)}
 if err:=os.WriteFile("owned.pid",[]byte(fmt.Sprint(child.Process.Pid)),0600);err!=nil{panic(err)}
 fmt.Print("owned descendant started")
 if os.Args[1]!="exit" { for { time.Sleep(time.Hour) } }
}
`
			main, err := e.Stage(t.Context(), "ownership/main.go", []byte(source))
			if err != nil {
				t.Fatal(err)
			}
			binary, err := e.Stage(t.Context(), "ownership/probe", nil)
			if err != nil {
				t.Fatal(err)
			}
			if out, err := e.Run(t.Context(), []string{"go", "build", "-o", binary, main}); err != nil {
				t.Fatalf("compile ownership probe: %v %s", err, out)
			}
			prepared, err := e.Prepare(t.Context(), RepositoryProcessSpec{Argv: []string{binary, mode}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			process, err := prepared.Start(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ready, done := context.WithTimeout(t.Context(), 30*time.Second)
			defer done()
			docker591Await(t, ready, func() bool { return strings.Contains(string(process.Snapshot().Stdout), "owned descendant started") }, "owned process start")
			// Only the fixture translates the container namespace. This PID is
			// observed before cancellation removes the entire private container.
			data, err := os.ReadFile(filepath.Join(b.root, "containers", q.Token, "invariant", "work", q.Directory, "owned.pid"))
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(string(data))
			if err != nil || pid <= 0 {
				t.Fatalf("invalid child PID: %q", data)
			}
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			if mode == "cancel" {
				cancel()
			} else if mode == "close" || mode == "environment close" {
				cleanup, stop := goProcessCleanupContext(t.Context())
				if mode == "close" {
					err = prepared.Close(cleanup)
				} else {
					err = e.Close(cleanup)
				}
				stop()
				if err == nil {
					t.Fatal("closing a live process silently completed")
				}
			}
			out, err := process.Wait(ctx)
			if mode == "exit" && (err != nil || !out.Drained || !out.Exited || out.ExitCode != 0) || mode != "exit" && err == nil {
				t.Fatalf("wrong ownership outcome: %v %+v", err, out)
			}
			if !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				t.Fatal("Wait returned before the silent descendant was reaped")
			}
			closeErr := docker591Close(t, e)
			if mode == "exit" && closeErr != nil {
				t.Fatal(closeErr)
			}
			b.clean()
		})
	}
}
