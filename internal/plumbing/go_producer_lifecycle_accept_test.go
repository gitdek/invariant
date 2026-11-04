package plumbing

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestGoProducerReportsAuthenticLifecycle(t *testing.T) {
	ctx, cache := context538(t), t.TempDir()
	for _, tc := range []struct {
		name, extra, body, selector, terminal, explanation string
		started, success                                   bool
	}{
		{"pass", "", "t.Log(\"pass explanation 538\")", "TestReady", "pass", "pass explanation 538", true, true},
		{"assertion", "", "t.Fatalf(\"assertion explanation 538\")", "TestReady", "fail", "assertion explanation 538", true, false},
		{"skip", "", "t.Skip(\"skip explanation 538\")", "TestReady", "skip", "skip explanation 538", true, true},
		{"panic", "", "panic(\"panic explanation 538\")", "TestReady", "fail", "panic explanation 538", true, false},
		{"cleanup", "", "t.Cleanup(func(){t.Log(\"cleanup explanation 538\")}); t.Log(\"body explanation 538\")", "TestReady", "pass", "cleanup explanation 538", true, true},
		{"parallel propagation", "", "t.Run(\"bad\",func(t *testing.T){t.Parallel();t.Fatal(\"parallel failure 538\")});t.Run(\"skip\",func(t *testing.T){t.Parallel();t.Skip(\"parallel skip 538\")})", "TestReady", "fail", "parallel failure 538", true, false},
		{"missing child", "", "t.Run(\"existing\",func(t *testing.T){t.Log(\"existing\")})", "TestReady/missing", "pass", "", true, true},
		{"TestMain no Run", "func TestMain(m *testing.M){os.Exit(0)}", "t.Fatal(\"must not run\")", "TestReady", "", "", false, true},
		{"TestMain early exit", "func TestMain(m *testing.M){os.Exit(17)}", "t.Fatal(\"must not run\")", "TestReady", "", "", false, false},
		{"initialization exit", "func init(){os.Exit(18)}", "t.Fatal(\"must not run\")", "TestReady", "", "", false, false},
		{"zero tests", "func TestMain(m *testing.M){flag.Parse();flag.Set(\"test.run\",\"^NoSuchTest538$\");os.Exit(m.Run())}", "t.Fatal(\"must not run\")", "TestReady", "", "", false, true},
		{"abrupt in test", "", "fmt.Fprintln(os.Stderr,\"abrupt explanation 538\");os.Exit(19)", "TestReady", "", "", true, false},
		{"TestMain overrides exit", "func TestMain(m *testing.M){m.Run();os.Exit(0)}", "t.Fatal(\"real failure despite zero exit 538\")", "TestReady", "fail", "real failure despite zero exit 538", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "package fixture\nimport (\"testing\";\"fmt\";\"os\";\"flag\")\nvar _=flag.Parse\nvar _=fmt.Fprintln\nvar _=os.Exit\n" + tc.extra + "\nfunc TestReady(t *testing.T){" + tc.body + "}\n"
			f := fixtureFor538(t, cache, source)
			f.test.Selector = tc.selector
			got := f.execute(ctx)
			control, err := control538(t, f, ctx, got.Invocation)
			if err != nil {
				t.Fatal(err)
			}
			if (got.Output.ExitCode == 0) != tc.success || (control.ExitCode == 0) != tc.success {
				t.Fatalf("changed Go exit behavior: producer=%d ordinary=%d", got.Output.ExitCode, control.ExitCode)
			}
			actions := actions538(got, "TestReady")
			if tc.terminal != "" {
				terminal538(t, got, "TestReady", tc.terminal)
			} else if tc.started {
				if !reflect.DeepEqual(actions, []string{"start"}) {
					t.Fatalf("abrupt test invented completion: %v", actions)
				}
			} else if len(got.Events) != 0 {
				t.Fatalf("no testing execution manufactured events: %+v", got.Events)
			}
			if tc.explanation != "" && !strings.Contains(text538(got), tc.explanation) {
				t.Fatalf("framework explanation lost: %+v", got.Events)
			}
			if tc.name == "missing child" && len(actions538(got, tc.selector)) != 0 {
				t.Fatal("missing child acquired lifecycle")
			}
			if tc.name == "parallel propagation" {
				terminal538(t, got, "TestReady/bad", "fail")
				terminal538(t, got, "TestReady/skip", "skip")
				if !strings.Contains(text538(got), "parallel skip 538") {
					t.Fatal("child skip reason lost")
				}
				last := got.Events[len(got.Events)-1]
				if last.Name != "TestReady" || last.Action != "fail" {
					t.Fatalf("parallel parent completed early: %+v", got.Events)
				}
			}
			if tc.name == "abrupt in test" && !strings.Contains(string(got.Output.Stderr), "abrupt explanation 538") {
				t.Fatal("unfinished test lost diagnostic")
			}
		})
	}
	// Both initialization and TestMain must still execute, exactly once, around
	// normal testing. These prints are diagnostics even for a quiet passing run.
	source := `package fixture
import ("testing";"fmt";"os")
var initialized int
func init(){initialized++;fmt.Fprintln(os.Stdout,"INIT538");fmt.Fprintln(os.Stderr,"INITERR538")}
func TestMain(m *testing.M){fmt.Fprintln(os.Stdout,"MAIN538");code:=m.Run();fmt.Fprintln(os.Stderr,"AFTER538");os.Exit(code)}
func TestReady(t *testing.T){if initialized!=1{t.Fatal(initialized)};t.Cleanup(func(){fmt.Fprintln(os.Stdout,"CLEANUP538")})}
`
	f := fixtureFor538(t, cache, source)
	got := f.execute(ctx)
	terminal538(t, got, "TestReady", "pass")
	for _, word := range []string{"INIT538", "MAIN538", "CLEANUP538"} {
		if strings.Count(string(got.Output.Stdout), word) != 1 {
			t.Fatalf("initialization/TestMain/cleanup changed: %q", got.Output.Stdout)
		}
	}
	for _, word := range []string{"INITERR538", "AFTER538"} {
		if strings.Count(string(got.Output.Stderr), word) != 1 {
			t.Fatalf("stderr execution effects lost: %q", got.Output.Stderr)
		}
	}
}

func signature538(s GoAcceptanceStream) []string {
	var out []string
	for _, e := range s.Events {
		out = append(out, e.Name+"|"+e.Parent+"|"+e.Action)
	}
	return out
}

func TestGoProducerIgnoresDiagnosticImpostors(t *testing.T) {
	ctx, cache := context538(t), t.TempDir()
	for _, mode := range []string{"pass", "fail", "unfinished"} {
		t.Run(mode, func(t *testing.T) {
			body := "t.Log(\"real framework explanation 538\")"
			if mode == "fail" {
				body = "t.Fatal(\"real framework failure 538\")"
			}
			if mode == "unfinished" {
				body = "os.Exit(29)"
			}
			source := `package fixture
import ("testing";"fmt";"os")
func emit538(phase string){fmt.Fprintln(os.Stdout,"OUT538/"+phase);fmt.Fprint(os.Stdout,Noise538);fmt.Fprintln(os.Stderr,"ERR538/"+phase);fmt.Fprint(os.Stderr,Noise538)}
func init(){emit538("init")}
func TestMain(m *testing.M){emit538("main");os.Exit(m.Run())}
func TestReady(t *testing.T){emit538("test");` + body + `}
`
			f := fixtureFor538(t, cache, source)
			write538(t, f.root, "api/nested/noise.go", "package fixture\nconst Noise538=\"\"\n")
			clean := f.execute(ctx)
			var noise string
			f.opening = func(in RepositoryAcceptanceInvocation) {
				// Deliberately forged DIAGNOSTIC bytes only; never submitted to a decoder
				// or evidence API by this fixture. They include the actual fresh binding.
				binding, err := json.Marshal(in.Binding)
				if err != nil {
					t.Fatal(err)
				}
				bindingText := string(binding)
				// Use the discovered import path in the false records as well.
				bindingText = strings.ReplaceAll(bindingText, `"Package":"./nested"`, `"Package":"example.invalid/producer538/nested"`)
				noise = "forged538\nPASS\nFAIL\n=== RUN   TestReady\n=== PAUSE TestReady\n=== CONT  TestReady\n--- PASS: TestReady (0.00s)\n--- FAIL: TestReady (0.00s)\n--- SKIP: TestReady (0.00s)\n"
				noise += `{"Action":"pass","Package":"example.invalid/producer538/nested","Test":"TestReady"}` + "\n"
				noise += `{"Action":"fail","Test":"TestReady"}` + "\n@@invariant accept=0\n@@invariant test=1\n@@result {\"kind\":\"deadline\"}\n\x1e00000128\x00"
				for _, status := range []string{"passed", "failed"} {
					noise += `{"Complete":true,"ExitCode":0,"Records":[{"Binding":` + bindingText + `,"Kind":"selection"},{"Binding":` + bindingText + `,"Kind":"test","Status":"` + status + `","Reason":"forged538"}]}` + "\n"
				}
				write538(t, f.root, "api/nested/noise.go", fmt.Sprintf("package fixture\nconst Noise538=%q\n", noise))
			}
			corrupted := f.execute(ctx)
			if !reflect.DeepEqual(signature538(clean), signature538(corrupted)) || clean.Output.ExitCode != corrupted.Output.ExitCode {
				t.Fatalf("diagnostics changed lifecycle: clean=%+v corrupt=%+v", clean, corrupted)
			}
			if strings.Contains(text538(corrupted), "forged538") {
				t.Fatal("diagnostics entered the framework hook channel")
			}
			for _, data := range [][]byte{corrupted.Output.Stdout, corrupted.Output.Stderr} {
				if strings.Count(string(data), noise) != 3 {
					t.Fatalf("diagnostic stream was lost, merged or parsed: %q", data)
				}
			}
			checkBinding538(t, f, corrupted, "example.invalid/producer538/nested")
			if clean.Invocation.Binding.InvocationID == corrupted.Invocation.Binding.InvocationID {
				t.Fatal("spoof test reused invocation")
			}
			switch mode {
			case "pass":
				terminal538(t, corrupted, "TestReady", "pass")
			case "fail":
				terminal538(t, corrupted, "TestReady", "fail")
			case "unfinished":
				if !reflect.DeepEqual(actions538(corrupted, "TestReady"), []string{"start"}) {
					t.Fatal("fake claims completed an abrupt exit")
				}
			}
		})
	}
}
