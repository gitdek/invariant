package plumbing

import (
	"slices"
	"strings"
	"testing"
)

func TestGoProducerPreservesSelfExecutingHelpers(t *testing.T) {
	ctx := context538(t)
	source := `package fixture
import ("testing"; "os"; "os/exec"; "fmt")
func init(){fmt.Fprintln(os.Stdout,"REEXEC_INIT538")}
func TestMain(m *testing.M){
 fmt.Fprintln(os.Stdout,"REEXEC_MAIN538")
 code:=m.Run()
 fmt.Fprintln(os.Stderr,"REEXEC_DONE538")
 os.Exit(code)
}
func TestReady(t *testing.T){
 t.Log("host before helpers 538")
 for _,selector:=range []string{"^$","^TestHelper538$","^TestHelperFailure538$"}{
  cmd:=exec.Command(os.Args[0],"-test.run="+selector,"-test.v")
  cmd.Stdout=os.Stdout;cmd.Stderr=os.Stderr
  err:=cmd.Run()
  if selector=="^TestHelperFailure538$"{
   if exit,ok:=err.(*exec.ExitError);!ok||exit.ExitCode()!=1{t.Fatalf("helper failure: %v",err)}
  }else if err!=nil{t.Fatalf("helper %s: %v",selector,err)}
 }
 t.Log("host after helpers 538")
}
func TestHelper538(t *testing.T){
 t.Log("helper log 538")
 t.Run("parallel",func(t *testing.T){t.Parallel();t.Log("helper parallel log 538")})
 t.Cleanup(func(){fmt.Fprintln(os.Stdout,"REEXEC_CLEANUP538")})
}
func TestHelperFailure538(t *testing.T){t.Fatal("expected helper failure 538")}
`
	f := fixtureFor538(t, t.TempDir(), source)
	f.part.Commands["test_one"] = command538([]string{"go", "test", "./...", "-run", "^{test}$", "-count=1", "-v"})
	got := f.execute(ctx)
	control, err := control538(t, f, ctx, got.Invocation)
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]RepositoryProcessOutput{"producer": got.Output, "ordinary Go": control} {
		if !out.Started || !out.Exited || !out.Drained || out.ExitCode != 0 {
			t.Fatalf("%s changed helper execution: %+v", name, out)
		}
		// The go test driver combines the binary's stderr into its stdout.
		diagnostic := string(out.Stdout) + string(out.Stderr)
		for _, marker := range []string{"REEXEC_INIT538", "REEXEC_MAIN538"} {
			if strings.Count(diagnostic, marker) != 4 {
				t.Fatalf("%s lost parent/helper initialization: %q", name, diagnostic)
			}
		}
		if strings.Count(diagnostic, "REEXEC_DONE538") != 4 {
			t.Fatalf("%s lost parent/helper TestMain: %q", name, diagnostic)
		}
		for _, marker := range []string{"helper log 538", "helper parallel log 538", "REEXEC_CLEANUP538", "expected helper failure 538"} {
			if strings.Count(diagnostic, marker) != 1 {
				t.Fatalf("%s lost helper diagnostics or cleanup: %q", name, diagnostic)
			}
		}
	}
	if strings.Count(string(got.Output.Stderr), "REEXEC_DONE538") != 4 || strings.Contains(string(got.Output.Stdout), "REEXEC_DONE538") {
		t.Fatalf("producer merged helper stderr into stdout: %+v", got.Output)
	}
	terminal538(t, got, "TestReady", "pass")
	for _, event := range got.Events {
		if event.Name != "TestReady" || event.Parent != "" {
			t.Fatalf("helper entered the host invocation: %+v", got.Events)
		}
	}
	if !slices.Equal(actions538(got, "TestReady"), []string{"start", "pass"}) || strings.Contains(text538(got), "helper log 538") || strings.Contains(text538(got), "expected helper failure 538") {
		t.Fatalf("helper diagnostics became host lifecycle: %+v", got.Events)
	}
	for _, log := range []string{"host before helpers 538", "host after helpers 538"} {
		if !strings.Contains(text538(got), log) {
			t.Fatalf("host lost its event channel around helpers: %+v", got.Events)
		}
	}
	checkBinding538(t, f, got, "example.invalid/producer538/nested")
}
