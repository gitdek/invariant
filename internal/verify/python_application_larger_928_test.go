package verify

// #928: one size larger, an evaluation error raised inside a standard
// operator is evidence TLC can't read, and fails as it does at the ratified
// bounds. TLC ends such a check with its general status, 255, which its
// system errors share, so the gate tells the two apart by TLC's own message
// code for the error, never by its words.
import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

// largerFault928 ends the larger run's step check as the pinned TLC ends a
// check whose recorded value a standard operator can't take. Its frames,
// words and status are the pinned TLC's for <= given TRUE, recorded from it
// for the issue's Add(n) model and standing in here for the counter's. The
// second fault gives those words to a system error's frame, with the same
// status: words decide nothing.
const largerFault928 = `    fault=os.environ.get('PA928_FAULT','')
    if limit==3 and module=='Invariant_Conformance' and fault:
        frame=2266 if fault=='standard operator' else 2161
        msg(frame,'The first argument of <= should be an integer, but instead it is:\nTRUE')
        msg(2121,'The behavior up to this point is:')
        msg(2217,'1: <Initial predicate>\n/\\ count = 2\n/\\ invariant_source = 3\n/\\ invariant_target = 4')
        msg(2103,'The error occurred when TLC was evaluating the nested\nexpressions at the following positions:\n0. Line 5, column 11 to line 5, column 50 in Counter\n1. Line 5, column 11 to line 5, column 28 in Counter')
        sys.exit(255)
`

func TestPythonApplicationLargerStandardOperatorErrorFails928(t *testing.T) {
	const reason = "The first argument of <= should be an integer, but instead it is: TRUE"
	tc, _ := largerRegressionFake881(t, largerFault928)
	t.Setenv("PA928_FAULT", "standard operator")
	r := pa342Unchanged(t, pa342New(t, true, false), tc)
	l, invocation := largerRecord881Review(t, r)
	if l.Result.States != 4 || l.Result.WantStates != 4 || l.Result.Depth != 4 || l.Result.WantDepth != 4 {
		t.Fatalf("the fixture must match TLC's count and depth: %+v", l.Result)
	}
	if r.Passed || !l.Result.Required || l.Result.Passed || !r.Larger.Required || r.Larger.Passed {
		t.Fatalf("an evaluation error inside a standard operator claimed nothing instead of failing: passed=%v %+v", r.Passed, l.Result)
	}
	if l.Conformance.OperationalError || l.Conformance.passed() {
		t.Fatalf("evidence TLC can't read is recorded as operational or passing: %+v", l.Conformance.Result)
	}
	for _, message := range []string{l.Result.Message, r.Larger.Message} {
		if !strings.HasPrefix(message, invocation+"conformance one size larger: TLC can't check what the run recorded: "+reason) || strings.ContainsAny(message, "\r\n") {
			t.Fatalf("want the invocation and TLC's reason on one line, got %q", message)
		}
	}
	if !strings.Contains(Feedback(r), reason) {
		t.Fatalf("feedback omits TLC's reason: %s", Feedback(r))
	}
}

// The same words and status in a system error's frame are an operational
// end: the check claims nothing, and the gate passes.
func TestPythonApplicationLargerOperatorWordsInSystemErrorClaimNothing928(t *testing.T) {
	tc, _ := largerRegressionFake881(t, largerFault928)
	t.Setenv("PA928_FAULT", "system error")
	r := pa342Unchanged(t, pa342New(t, true, false), tc)
	l, invocation := largerRecord881Review(t, r)
	if !r.Passed || l.Result.Required || l.Result.Passed || !l.Conformance.OperationalError {
		t.Fatalf("a system error must claim nothing and fail nothing, whatever its words: passed=%v %+v", r.Passed, l.Result)
	}
	if !strings.HasPrefix(l.Result.Message, invocation+"conformance one size larger was unavailable: ") || strings.ContainsAny(l.Result.Message, "\r\n") {
		t.Fatalf("want the invocation and the reason on one line, got %q", l.Result.Message)
	}
}

// The line a signal at a time. A standard operator's code with TLC's general
// status fails, with TLC's reason. A system error's code claims nothing, even
// in an operator's words, and no words take a failure back.
func TestPythonApplicationLargerStandardOperatorSignals928(t *testing.T) {
	const version = "TLC2 Version 2.19 of 08 August 2024 (rev: 5a47802)"
	failed := func(exit int, codes []int, message string) PythonApplicationModelCheck {
		return PythonApplicationModelCheck{Module: "Invariant_StrictAttempts", Result: tlc.Result{Outcome: tlc.Failed, Version: version, ExitCode: exit, Codes: codes, Message: message}}
	}
	const leq = "The first argument of <= should be an integer, but instead it is:\nTRUE\nThe error occurred when TLC was evaluating the nested\nexpressions at the following positions:"
	runner := failed(255, []int{2266, 2103}, leq)
	runner.Result.Version = ""
	erred := failed(255, []int{2266, 2103}, leq)
	erred.Error = "TLC check Invariant_StrictAttempts cut off: context deadline exceeded"
	operator := failed(255, []int{2266, 2103}, leq)
	const reason = "The first argument of <= should be an integer, but instead it is: TRUE The error occurred when TLC was evaluating the nested expressions at the following positions:"
	disk := failed(255, []int{2161}, "TLC encountered the following error while trying to write the file /tmp/states/MC.st:\nNo space left on device")
	cases := []struct {
		name       string
		check      PythonApplicationModelCheck
		unreadable bool
		reason     string
	}{
		{"<= given a boolean", operator, true, reason},
		{"Len given a boolean", failed(255, []int{2283, 2103}, "The argument of Len should be a sequence, but instead it is:\nTRUE"), true, "The argument of Len should be a sequence, but instead it is: TRUE"},
		{"Head given an empty sequence", failed(255, []int{2184, 2103}, "Attempted to apply Head to the empty sequence."), true, "Attempted to apply Head to the empty sequence."},
		{"a sum past TLC's integers", failed(255, []int{2178, 2103}, "Overflow when computing 2147483647+1"), true, "Overflow when computing 2147483647+1"},
		{"a value checked against Nat", failed(255, []int{2158, 2103}, "Attempted to check if the value:\nTRUE\nis an element of Nat."), true, "Attempted to check if the value: TRUE is an element of Nat."},
		// A recorded value can't talk its way out: TLC repeats it in its message.
		{"a disk's words in the message", failed(255, []int{2266, 2103}, "The first argument of <= should be an integer, but instead it is:\n\"No space left on device; java.lang.OutOfMemoryError\""), true,
			"The first argument of <= should be an integer, but instead it is: \"No space left on device; java.lang.OutOfMemoryError\""},
		{"a disk that filled", disk, false, ""},
		{"TLC's general error", failed(255, []int{1000}, "TLC threw an unexpected exception.\nThe exception was a java.lang.NullPointerException"), false, ""},
		{"a system error in an operator's words", failed(255, []int{1000}, leq), false, ""},
		{"an operator's code with another status", failed(137, []int{2266}, leq), false, ""},
		{"the runner's result", runner, false, ""},
		{"a result beside the runner's own error", erred, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, unreadable := pythonApplicationUnreadableEvidence(PythonApplicationConformance{Checks: []PythonApplicationModelCheck{c.check}})
			if unreadable != c.unreadable || reason != c.reason {
				t.Fatalf("unreadable=%v reason=%q, want %v %q", unreadable, reason, c.unreadable, c.reason)
			}
		})
	}
	// It stands beside an operational end, in either order, as other
	// evidence TLC can't read does.
	for _, checks := range [][]PythonApplicationModelCheck{{disk, operator}, {operator, runner}} {
		if got, unreadable := pythonApplicationUnreadableEvidence(PythonApplicationConformance{Checks: checks}); !unreadable || got != reason {
			t.Fatalf("an operator's error beside an operational end: unreadable=%v reason=%q", unreadable, got)
		}
	}
}
