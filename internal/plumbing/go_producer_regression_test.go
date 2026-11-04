package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLocalProcessTransfersOpaqueBytesAndEffectiveEnvironment(t *testing.T) {
	ctx := context538(t)
	f := fixtureFor538(t, t.TempDir(), "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){}\n")
	env := openTransport538(t, f, ctx)
	helper, err := env.Stage(ctx, "channels/main.go", []byte(`package main
import ("bytes"; "encoding/json"; "fmt"; "os"; "sync")
func main() {
 f,err:=os.OpenFile(os.Getenv("CHANNEL_EVENTS"),os.O_WRONLY,0);if err!=nil { panic(err) }
 dir,_:=os.Getwd()
 header,_:=json.Marshal(struct { Args []string; GOENV, Dir string }{os.Args[1:],os.Getenv("GOENV"),dir})
 fmt.Fprintln(os.Stdout,string(header))
 var wg sync.WaitGroup
 for i,out:=range []*os.File{os.Stdout,os.Stderr,f} {
  wg.Add(1)
  go func(){defer wg.Done();if _,err:=out.Write(bytes.Repeat([]byte{0,byte(i),255,10},32768));err!=nil{panic(err)}}()
 }
 wg.Wait()
 os.Exit(5)
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := env.Run(ctx, []string{"go", "build", "-o", helper + ".bin", helper}); err != nil {
		t.Fatalf("compile: %v %s", err, out)
	}
	args := []string{"", "one two", "$(no_shell);`literal`", "line\nbreak", "--flag"}
	p, err := env.Prepare(ctx, RepositoryProcessSpec{Argv: append([]string{helper + ".bin"}, args...), EventEnv: "CHANNEL_EVENTS"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := p.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out, err := child.Wait(ctx)
	if err != nil || !out.Exited || !out.Drained || out.ExitCode != 5 {
		t.Fatalf("opaque process: %+v %v", out, err)
	}
	header, stdout, _ := bytes.Cut(out.Stdout, []byte{'\n'})
	var observed struct {
		Args       []string
		GOENV, Dir string
	}
	if err := json.Unmarshal(header, &observed); err != nil || !slices.Equal(observed.Args, args) || observed.GOENV != "off" {
		t.Fatalf("actual process arguments/environment: %s %v", header, err)
	}
	for i, data := range [][]byte{stdout, out.Stderr, out.Events} {
		if !bytes.Equal(data, bytes.Repeat([]byte{0, byte(i), 255, 10}, 32768)) {
			t.Fatalf("channel %d was changed or lost (%d bytes)", i, len(data))
		}
	}
	snapshot := child.Snapshot()
	snapshot.Events[0] = 42
	if child.Snapshot().Events[0] != 0 {
		t.Fatal("snapshot aliases event bytes")
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := env.Close(cleanup); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{helper, observed.Dir} {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("private resource survived Close: %s %v", name, err)
		}
	}
}

func TestGoProducerKeepsRuntimeParentsAndPackageInitialization(t *testing.T) {
	ctx := context538(t)
	f := fixtureFor538(t, t.TempDir(), `package fixture
import ("testing"; "os"; "fmt")
var data, readErr = os.ReadFile("relative-data")
func TestReady(t *testing.T) {
 if readErr != nil || string(data) != "from package directory" { t.Fatalf("initialization cwd: %q %v", data, readErr) }
 t.Run("a/b", func(t *testing.T) { fmt.Fprint(t.Output(), "partial framework log") })
}
`)
	write538(t, f.root, "api/go.mod", "module example.invalid/producer538\n\ngo 1.27.1\n")
	write538(t, f.root, "api/nested/relative-data", "from package directory")
	s := f.execute(ctx)
	terminal538(t, s, "TestReady", "pass")
	terminal538(t, s, "TestReady/a/b", "pass")
	for _, e := range s.Events {
		if e.Name == "TestReady/a/b" && e.Parent != "TestReady" {
			t.Fatalf("slash in runtime name invented a parent: %+v", e)
		}
	}
	if !strings.Contains(text538(s), "partial framework log") {
		t.Fatal("testing.Output's partial line did not reach the hook")
	}
}

func TestLocalProcessReleasesDescendantsAfterAnUnfinishedDrain(t *testing.T) {
	ctx := context538(t)
	f := fixtureFor538(t, t.TempDir(), "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){}\n")
	env := openTransport538(t, f, ctx)
	helper, err := env.Stage(ctx, "descendants/main.go", []byte(opaqueHelper538))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := env.Run(ctx, []string{"go", "build", "-o", helper + ".bin", helper}); err != nil {
		t.Fatalf("compile: %v %s", err, out)
	}
	for _, mode := range []string{"hold", "orphan"} {
		p, err := env.Prepare(ctx, RepositoryProcessSpec{Argv: []string{helper + ".bin", mode}, EventEnv: "OPAQUE538"})
		if err != nil {
			t.Fatal(err)
		}
		child, err := p.Start(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := awaitOutput538(t, ctx, child, "CHILD538=")
		pid := childPID538(t, out)
		wait, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		partial, err := child.Wait(wait)
		cancel()
		if err == nil || !partial.Started || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unfinished descendant drain became success: %+v %v", partial, err)
		}
		cleanup, done := context.WithTimeout(context.Background(), 2*time.Second)
		if err := p.Close(cleanup); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cleanup lost interruption: %v", err)
		}
		done()
		gone538(t, pid)
	}
}
