package plumbing

import (
	"strings"
	"testing"
)

func TestGoProducerPreservesExitZeroPanic(t *testing.T) {
	ctx := context538(t)
	source := `package fixture
import ("testing"; "os"; "fmt")
func TestReady(t *testing.T) {
 t.Cleanup(func(){fmt.Fprintln(os.Stdout,"EXIT_ZERO_CLEANUP538");t.Log("exit zero cleanup hook 538")})
 os.Exit(0)
}
`
	f := fixtureFor538(t, t.TempDir(), source)
	got := f.execute(ctx)
	control, err := control538(t, f, ctx, got.Invocation)
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]RepositoryProcessOutput{"producer": got.Output, "ordinary Go": control} {
		if !out.Started || !out.Exited || !out.Drained || out.ExitCode == 0 {
			t.Fatalf("%s did not fail on os.Exit(0): %+v", name, out)
		}
		diagnostic := string(out.Stdout) + string(out.Stderr)
		if !strings.Contains(diagnostic, "panic: unexpected call to os.Exit(0) during test") || strings.Count(diagnostic, "EXIT_ZERO_CLEANUP538") != 1 {
			t.Fatalf("%s lost exit-zero panic or cleanup: %s", name, diagnostic)
		}
	}
	terminal538(t, got, "TestReady", "fail")
	if !strings.Contains(text538(got), "unexpected call to os.Exit(0) during test") || !strings.Contains(text538(got), "exit zero cleanup hook 538") {
		t.Fatalf("exit-zero failure lost framework explanation or cleanup: %+v", got.Events)
	}
}

func TestGoProducerAndLocalTransportPreservePWD(t *testing.T) {
	ctx := context538(t)
	t.Setenv("PWD", "/stale-parent-pwd538")
	source := `package fixture
import ("testing"; "os"; "os/exec"; "path/filepath"; "strings")
func TestReady(t *testing.T) {
 dir,err:=os.Getwd();if err!=nil{t.Fatal(err)}
 if pwd:=os.Getenv("PWD");pwd!=dir{t.Fatalf("PWD=%q, working directory=%q",pwd,dir)}
 if filepath.Base(dir)!="nested"{t.Fatalf("wrong package directory: %s",dir)}
 data,err:=os.ReadFile(filepath.Join(os.Getenv("PWD"),"fixture.txt"))
 if err!=nil||string(data)!="package data"{t.Fatalf("PWD-relative data: %q %v",data,err)}
 out,err:=exec.Command("go","run","./probe").CombinedOutput()
 if err!=nil||strings.TrimSpace(string(out))!="nested"{t.Fatalf("child PWD: %s %v",out,err)}
 t.Log("package and child PWD preserved 538")
}
`
	probe := `package main
import ("fmt"; "os"; "path/filepath")
func main(){
 dir,err:=os.Getwd();if err!=nil{panic(err)}
 if pwd:=os.Getenv("PWD");pwd!=dir{panic(fmt.Sprintf("PWD=%q, working directory=%q",pwd,dir))}
 fmt.Println(filepath.Base(dir))
}
`
	f := fixtureFor538(t, t.TempDir(), source)
	write538(t, f.root, "api/nested/fixture.txt", "package data")
	write538(t, f.root, "api/nested/probe/main.go", probe)
	env := openTransport538(t, f, ctx)
	helper, err := env.Stage(ctx, "pwd/main.go", []byte(probe))
	if err != nil {
		t.Fatal(err)
	}
	binary := helper + ".bin"
	if out, err := env.Run(ctx, []string{"go", "build", "-o", binary, helper}); err != nil {
		t.Fatalf("compile PWD probe: %v %s", err, out)
	}
	// Direct launches also need PWD, for the owning part and explicit directories.
	for _, dir := range []string{"", ".", "nested"} {
		p, err := env.Prepare(ctx, RepositoryProcessSpec{Argv: []string{binary}, Directory: dir})
		if err != nil {
			t.Fatal(err)
		}
		child, err := p.Start(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out, err := child.Wait(ctx)
		assertProcessesGone538(t, f.processes)
		want := "api"
		if dir == "nested" {
			want = "nested"
		}
		if err != nil || !out.Exited || !out.Drained || out.ExitCode != 0 || strings.TrimSpace(string(out.Stdout)) != want {
			t.Fatalf("directory %q lost PWD: %+v %v", dir, out, err)
		}
	}
	got := f.execute(ctx)
	control, err := control538(t, f, ctx, got.Invocation)
	if err != nil {
		t.Fatal(err)
	}
	if got.Output.ExitCode != 0 || control.ExitCode != 0 {
		t.Fatalf("package PWD differs from ordinary Go: producer=%+v ordinary=%+v", got.Output, control)
	}
	terminal538(t, got, "TestReady", "pass")
	if !strings.Contains(text538(got), "package and child PWD preserved 538") {
		t.Fatalf("package and child PWD checks did not finish: %+v", got.Events)
	}
}
