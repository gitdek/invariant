package gobra

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// passed.txt is a real Gobra run over examples/02-twophase-commit/twophase
// with --overflow.
func TestParsePassed(t *testing.T) {
	r := Parse(fixture(t, "passed.txt"), 0)
	if !r.Passed || len(r.Errors) != 0 {
		t.Fatalf("Passed = %v, Errors = %v; want a clean pass", r.Passed, r.Errors)
	}
}

// failed.txt is the same package with RMPrepare no longer sending its
// Prepared message.
func TestParseFailed(t *testing.T) {
	r := Parse(fixture(t, "failed.txt"), 1)
	want := []string{"twophase.go:97:9: Postcondition might not hold."}
	if r.Passed || !reflect.DeepEqual(r.Errors, want) {
		t.Fatalf("Passed = %v, Errors = %q; want %q", r.Passed, r.Errors, want)
	}
}

func TestParseNoSummary(t *testing.T) {
	r := Parse("Exception in thread main", 1)
	if r.Passed || len(r.Errors) != 1 {
		t.Fatalf("Passed = %v, Errors = %v; want one error explaining there was no result", r.Passed, r.Errors)
	}
}

// An abort, as Java prints an exception: its headline, then its frames.
// The frames and Gobra's summary are the ones an agent saw when Gobra
// aborted on #194's build, in the last 15 lines, which were all it was
// shown, and which named no cause.
const aborted = `00:04:21.102 [main] INFO  viper.gobra.Gobra - Verifying package .. - merges
java.lang.RuntimeException: the cause the agent never saw
	at viper.silicon.supporters.functions.DefaultFunctionVerificationUnitProvider$functionsSupporter$.checkSpecificationWelldefinedness(FunctionVerificationUnit.scala:239)
Caused by: scala.MatchError: the deeper cause (of class viper.silver.ast.SeqLength)
	at viper.silicon.rules.executionFlowController$.$anonfun$locally$1(ExecutionFlowController.scala:100)
	at viper.silicon.rules.executionFlowController$.locallyWithResult(ExecutionFlowController.scala:63)
	at viper.silicon.rules.executionFlowController$.locally(ExecutionFlowController.scala:100)
	at viper.silicon.supporters.functions.DefaultFunctionVerificationUnitProvider$functionsSupporter$.checkSpecificationWelldefinedness(FunctionVerificationUnit.scala:239)
	at viper.silicon.supporters.functions.DefaultFunctionVerificationUnitProvider$functionsSupporter$.handleFunction(FunctionVerificationUnit.scala:186)
	at viper.silicon.supporters.functions.DefaultFunctionVerificationUnitProvider$functionsSupporter$.verify(FunctionVerificationUnit.scala:172)
	at viper.silicon.verifier.DefaultMainVerifier.$anonfun$verify$2(DefaultMainVerifier.scala:233)
	at scala.collection.immutable.List.flatMap(List.scala:283)
	at viper.silicon.verifier.DefaultMainVerifier.verify(DefaultMainVerifier.scala:229)
	at viper.silicon.Silicon.viper$silicon$Silicon$$runVerifier(Silicon.scala:245)
	at viper.silicon.Silicon$$anon$1.call(Silicon.scala:199)
	at viper.silicon.Silicon$$anon$1.call(Silicon.scala:198)
	at java.base/java.util.concurrent.FutureTask.run(FutureTask.java:317)
	... 3 common frames omitted
00:04:25.730 [main] ERROR viper.gobra.Gobra - The verification of 1 package was aborted by an exception: .. - merges
`

// When Gobra aborts, the error shows what it said, the exception's
// headlines and its own summary, and none of the frames between them.
func TestParseAborted(t *testing.T) {
	r := Parse(aborted, 1)
	if r.Passed || len(r.Errors) != 1 {
		t.Fatalf("Passed = %v, Errors = %q; want one error", r.Passed, r.Errors)
	}
	for _, want := range []string{"the cause the agent never saw", "Caused by: scala.MatchError: the deeper cause", "aborted by an exception: .. - merges"} {
		if !strings.Contains(r.Errors[0], want) {
			t.Errorf("the error leaves out %q:\n%s", want, r.Errors[0])
		}
	}
	if strings.Contains(r.Errors[0], "\tat ") || strings.Contains(r.Errors[0], "frames omitted") {
		t.Errorf("the error shows the stack's frames:\n%s", r.Errors[0])
	}
}

func TestFunctions(t *testing.T) {
	dir := "../../examples/02-twophase-commit/twophase"
	files, skipped, err := sources(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || skipped[0] != "explore.go" {
		t.Errorf("skipped = %v; want only explore.go, which has no %s header", skipped, Header)
	}
	all, withContract, err := functions(dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 10 || !reflect.DeepEqual(all, withContract) {
		t.Errorf("functions = %v, with contracts = %v; want all 10 to carry a contract", all, withContract)
	}
}
