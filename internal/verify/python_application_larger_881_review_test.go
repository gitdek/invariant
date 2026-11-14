package verify

// Review of #881's build. One size larger, a check that TLC ends because it
// can't read what the run recorded is invalid execution evidence and fails,
// as it does at the ratified bounds. Only an operational end claims nothing.
// These cases hold that line without Docker: the double answers with the
// pinned TLC's own frames, words and exit statuses, recorded from it for
// these modules, and the integration suite runs the same fixtures on it.
import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/tlc"
)

// largerPinnedTLC881 is how the pinned TLC ends on a checking module that
// holds a count it can't read. The counter's modules extend Naturals, which
// has no unary minus, so SANY rejects a negative count and TLC exits 150. A
// boolean where Next computes a number stops its evaluation, with exit 75.
// A number past its integers ends it with its general status, 255.
const largerPinnedTLC881 = `    if module.startswith('Invariant_'):
        for value in re.findall(r'count \|-> ([^\],]+)',body):
            if value.startswith('-'):
                msg(2220,'Starting SANY...')
                print('Parsing file /work/%s.tla'%module)
                print('Semantic processing of module %s'%module)
                print("Semantic errors:\n\n*** Errors: 2\n\nline 3, col 33 to line 3, col 33 of module %s\n\nCould not find declaration or definition of symbol '-.'.\n\n\nline 3, col 33 to line 3, col 34 of module %s\n\nCouldn't resolve prefix operator symbol ` + "`" + `-.'.\n\n\n"%(module,module))
                msg(2219,'SANY finished.'); msg(2185,'Starting... (2026-11-13 21:34:29)')
                msg(3002,'Parsing or semantic analysis failed.'); msg(2186,'Finished in 631ms at (2026-11-13 21:34:29)')
                sys.exit(150)
            if value in ('TRUE','FALSE'):
                msg(1000,'TLC threw an unexpected exception.\nThis was probably caused by an error in the spec or model.\nSee the User Output or TLC Console for clues to what happened.\nThe exception was a java.lang.RuntimeException\n: Attempted to check equality of integer 3 with non-integer:\n%s'%value)
                msg(2121,'The behavior up to this point is:')
                msg(2217,'1: <Initial predicate>\n/\\ invariant_source = 3\n/\\ invariant_done = FALSE\n/\\ invariant_target = 4\n/\\ count = 2')
                msg(2103,'The error occurred when TLC was evaluating the nested\nexpressions at the following positions:\n0. Line 15, column 8 to line 19, column 58 in %s\n1. Line 15, column 11 to line 15, column 25 in %s'%(module,module))
                sys.exit(75)
            if value.isdigit() and int(value)>2147483647:
                msg(2265,"TLC can't handle a number this big.\n%s"%value); sys.exit(255)
`

// largerFault881 ends one of the larger run's checks. The first three end its
// starting-state check as TLC and its runner do when nothing is wrong with
// the evidence, and "every step" its last check. "every step unreadable" ends
// that check as TLC ends one it can't evaluate. Only the memory diagnostic is
// TLC's own line: the rest stand in for it, with the status and the words the
// gate reads.
const largerFault881 = `    fault=os.environ['PA881_REVIEW_FAULT']
    if limit==3 and module=='Invariant_ConformanceInit':
        if fault=='memory':
            # The runner reads the diagnostic and writes the result itself.
            print('java.lang.OutOfMemoryError: Java heap space'); sys.exit(150)
        if fault=='disk':
            msg(2161,'TLC encountered the following error while trying to write the file /tmp/states/MC.st:\nNo space left on device'); sys.exit(255)
        if fault=='unavailable':
            print('TLC fixture unavailable'); sys.exit(73)
    if limit==3 and module=='Invariant_Tried' and fault=='every step':
        print('TLC fixture unavailable'); sys.exit(73)
    if limit==3 and module=='Invariant_Tried' and fault=='every step unreadable':
        msg(1000,'TLC threw an unexpected exception.\nThis was probably caused by an error in the spec or model.\nThe exception was a java.lang.RuntimeException\n: Attempted to apply the operator Inc to the non-integer argument "one"')
        msg(2103,'The error occurred when TLC was evaluating the nested\nexpressions at the following positions:\n0. Line 9, column 3 to line 9, column 40 in Invariant_Tried')
        sys.exit(75)
`

// largerRecord881Review returns the one larger record and the words that
// name its invocation, which every message about it starts with.
func largerRecord881Review(t *testing.T, r *Report) (PythonApplicationLarger, string) {
	t.Helper()
	_, base := pa342Explored(t, r)
	if !base.passed() || len(r.Application.Larger) != 1 || r.Larger == nil {
		t.Fatalf("want passing base evidence and one larger record: %s", Feedback(r))
	}
	l := r.Application.Larger[0]
	if l.Conformance == nil || l.Execution == nil || !l.Execution.Success {
		t.Fatalf("the larger run didn't reach its attempt check: %+v", l)
	}
	return l, fmt.Sprintf("target %s, check %s, invocation %s: ", l.Key.Target, l.Key.Check, l.Key.ID)
}

func TestPythonApplicationLargerUnreadableEvidenceFails881(t *testing.T) {
	cases := []struct{ name, source, reason string }{
		// The plan's own fixture: one size larger the run starts at -1.
		{"wrong start", "", "Could not find declaration or definition of symbol '-.'"},
		{"step to -1", "def increment(count):\n    return count + 1 if count < 2 else -1\n", "Could not find declaration or definition of symbol '-.'"},
		{"step to True", "def increment(count):\n    return count + 1 if count < 2 else True\n", "Attempted to check equality of integer 3 with non-integer: TRUE"},
		{"step to 2**40", "def increment(count):\n    return count + 1 if count < 2 else 2**40\n", "TLC can't handle a number this big. 1099511627776"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tc, _ := largerRegressionFake881(t, largerPinnedTLC881)
			f := pa342New(t, true, false)
			if c.source == "" {
				f = largerFixture881(t, "wrong start", true)
			} else {
				pa342Write(t, f.root, pa342Source, c.source)
			}
			r := pa342Unchanged(t, f, tc)
			l, invocation := largerRecord881Review(t, r)
			if l.Result.States != 4 || l.Result.WantStates != 4 || l.Result.Depth != 4 || l.Result.WantDepth != 4 {
				t.Fatalf("the fixture must match TLC's count and depth: %+v", l.Result)
			}
			if r.Passed || !l.Result.Required || l.Result.Passed || !r.Larger.Required || r.Larger.Passed {
				t.Fatalf("evidence TLC can't read claimed nothing instead of failing: passed=%v %+v", r.Passed, l.Result)
			}
			if l.Conformance.OperationalError || l.Conformance.passed() {
				t.Fatalf("unreadable evidence is recorded as operational or passing: %+v", l.Conformance.Result)
			}
			for _, message := range []string{l.Result.Message, r.Larger.Message} {
				if !strings.HasPrefix(message, invocation+"conformance one size larger: TLC can't check what the run recorded: ") || !strings.Contains(message, c.reason) || strings.ContainsAny(message, "\r\n") {
					t.Fatalf("want the invocation and TLC's reason on one line, got %q", message)
				}
			}
			if !strings.Contains(Feedback(r), c.reason) {
				t.Fatalf("feedback omits TLC's reason: %s", Feedback(r))
			}
		})
	}
}

// The line between evidence TLC can't read and an operational end, a signal
// at a time. Statuses are TLC's own, tlc2.output.EC.ExitStatus.
func TestPythonApplicationLargerUnreadableEvidenceSignals881(t *testing.T) {
	const version = "TLC2 Version 2.19 of 08 August 2024 (rev: 5a47802)"
	failed := func(exit int, message string) PythonApplicationModelCheck {
		return PythonApplicationModelCheck{Module: "Invariant_Conformance", Result: tlc.Result{Outcome: tlc.Failed, Version: version, ExitCode: exit, Message: message}}
	}
	passed := PythonApplicationModelCheck{Module: "Invariant_ConformanceInit", Result: tlc.Result{Outcome: tlc.Passed, Version: version}}
	cut := failed(0, "")
	cut.Result, cut.Error = tlc.Result{}, "TLC check Invariant_Conformance cut off: context deadline exceeded"
	stalled := failed(0, "TLC stopped making progress: no output for 2m0s")
	stalled.Result.Version = ""
	memory := failed(150, "TLC check Invariant_Conformance exhausted Java memory")
	memory.Result.Version = ""
	// A result the runner returned with an error of its own is no verdict.
	erred := failed(150, "Semantic errors:")
	erred.Error = "TLC check Invariant_Conformance cut off: context deadline exceeded"
	long := failed(75, strings.Repeat("TLC threw an unexpected exception.\n", 40))
	cases := []struct {
		name       string
		checks     []PythonApplicationModelCheck
		unreadable bool
		reason     string
	}{
		{"SANY can't parse the module", []PythonApplicationModelCheck{passed, failed(150, "Semantic errors:\n\n*** Errors: 2\n\nCould not find declaration or definition of symbol '-.'.")}, true, "Semantic errors: *** Errors: 2 Could not find declaration or definition of symbol '-.'."},
		{"a step can't be evaluated", []PythonApplicationModelCheck{passed, failed(75, "TLC threw an unexpected exception.\n: Attempted to check equality of integer 3 with non-integer:\nTRUE")}, true, "TLC threw an unexpected exception. : Attempted to check equality of integer 3 with non-integer: TRUE"},
		{"an invariant can't be evaluated", []PythonApplicationModelCheck{failed(76, "Evaluating invariant Init failed.")}, true, "Evaluating invariant Init failed."},
		{"a number past TLC's integers", []PythonApplicationModelCheck{passed, failed(255, "TLC can't handle a number this big.\n1099511627776")}, true, "TLC can't handle a number this big. 1099511627776"},
		{"TLC's general status for a system error", []PythonApplicationModelCheck{passed, failed(255, "TLC encountered the following error while trying to write the file /tmp/states/MC.st:\nNo space left on device")}, false, ""},
		// A recorded value can't talk its way out: TLC repeats it in its message.
		{"words a recorded value put in TLC's message", []PythonApplicationModelCheck{failed(75, "TLC threw an unexpected exception.\n: Attempted to check equality of integer 3 with non-integer:\n\"TLC stopped making progress: java.lang.OutOfMemoryError, StackOverflowError\"")}, true,
			"TLC threw an unexpected exception. : Attempted to check equality of integer 3 with non-integer: \"TLC stopped making progress: java.lang.OutOfMemoryError, StackOverflowError\""},
		{"the runner's result for a stall", []PythonApplicationModelCheck{passed, stalled}, false, ""},
		{"the runner's result for memory, whatever TLC's status", []PythonApplicationModelCheck{passed, memory}, false, ""},
		{"the runner's own error", []PythonApplicationModelCheck{passed, cut}, false, ""},
		{"a result beside the runner's own error", []PythonApplicationModelCheck{erred}, false, ""},
		// What TLC ended on evidence it can't read stands, whatever became of
		// another check, in either order.
		{"unreadable evidence, then the runner's own error", []PythonApplicationModelCheck{failed(150, "Semantic errors:"), cut}, true, "Semantic errors:"},
		{"an operational end, then unreadable evidence", []PythonApplicationModelCheck{failed(73, "TLC fixture unavailable"), stalled, failed(75, "TLC threw an unexpected exception.")}, true, "TLC threw an unexpected exception."},
		{"the runner's own error, then unreadable evidence", []PythonApplicationModelCheck{cut, erred, failed(76, "Evaluating invariant Init failed.")}, true, "Evaluating invariant Init failed."},
		{"a status that isn't TLC's verdict on the module", []PythonApplicationModelCheck{failed(73, "TLC fixture unavailable")}, false, ""},
		{"a configuration TLC can't read", []PythonApplicationModelCheck{failed(151, "TLC found an error in the configuration file")}, false, ""},
		{"an assumption of the model", []PythonApplicationModelCheck{failed(10, "Assumption line 4, col 8 is false.")}, false, ""},
		{"a container that was killed", []PythonApplicationModelCheck{failed(137, "")}, false, ""},
		{"unreadable evidence beside an operational end", []PythonApplicationModelCheck{failed(150, "Semantic errors:"), failed(73, "TLC fixture unavailable")}, true, "Semantic errors:"},
		{"the first of two unreadable checks gives the reason", []PythonApplicationModelCheck{failed(76, "Evaluating invariant Init failed."), failed(150, "Semantic errors:")}, true, "Evaluating invariant Init failed."},
		{"nothing failed", []PythonApplicationModelCheck{passed}, false, ""},
		{"no checks", nil, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, unreadable := pythonApplicationUnreadableEvidence(PythonApplicationConformance{Checks: c.checks})
			if unreadable != c.unreadable || reason != c.reason {
				t.Fatalf("unreadable=%v reason=%q, want %v %q", unreadable, reason, c.unreadable, c.reason)
			}
		})
	}
	reason, unreadable := pythonApplicationUnreadableEvidence(PythonApplicationConformance{Checks: []PythonApplicationModelCheck{long}})
	if !unreadable || len(reason) > 404 || !strings.HasSuffix(reason, " ...") || strings.ContainsAny(reason, "\r\n") || !strings.HasPrefix(reason, "TLC threw an unexpected exception. TLC threw") {
		t.Fatalf("a long reason must be cut to one short line: %d bytes, %q", len(reason), reason)
	}
}

// Ends that look like unreadable evidence and aren't: the evidence is sound,
// so the larger check claims nothing and the gate passes.
func TestPythonApplicationLargerOperationalLookalikesClaimNothing881(t *testing.T) {
	cases := []struct{ fault, reason string }{
		{"memory", "exhausted Java memory"},
		{"disk", "No space left on device"},
	}
	for _, c := range cases {
		t.Run(c.fault, func(t *testing.T) {
			tc, _ := largerRegressionFake881(t, largerFault881)
			t.Setenv("PA881_REVIEW_FAULT", c.fault)
			r := pa342Unchanged(t, pa342New(t, true, false), tc)
			l, invocation := largerRecord881Review(t, r)
			if !r.Passed || l.Result.Required || l.Result.Passed || !l.Conformance.OperationalError {
				t.Fatalf("an operational end must claim nothing and fail nothing: passed=%v %+v", r.Passed, l.Result)
			}
			if !strings.HasPrefix(l.Result.Message, invocation+"conformance one size larger was unavailable: ") || !strings.Contains(l.Result.Message, c.reason) || strings.ContainsAny(l.Result.Message, "\r\n") {
				t.Fatalf("want the invocation and the reason on one line, got %q", l.Result.Message)
			}
		})
	}
}

// A count that disagrees is a completed disagreement. A later check that was
// unavailable takes nothing from it.
func TestPythonApplicationLargerCountDisagreementOutlivesUnavailable881(t *testing.T) {
	tc, _ := largerRegressionFake881(t, largerFault881)
	t.Setenv("PA881_REVIEW_FAULT", "unavailable")
	f := pa342New(t, true, false)
	// One size larger this counter stops at 2: three states where TLC has four.
	pa342Write(t, f.root, pa342Source, "def increment(count):\n    return count + (1 if count < 2 else 0)\n")
	r := pa342Unchanged(t, f, tc)
	l, invocation := largerRecord881Review(t, r)
	if !l.Conformance.OperationalError {
		t.Fatalf("the fixture's attempt check must be unavailable: %+v", l.Conformance)
	}
	if r.Passed || !l.Result.Required || l.Result.Passed || l.Result.States != 3 || l.Result.WantStates != 4 {
		t.Fatalf("an unavailable check erased a count disagreement: passed=%v %+v", r.Passed, l.Result)
	}
	if want := invocation + "one size larger, the code reaches 3 states in 3 levels, and the model reaches 4 in 4"; l.Result.Message != want {
		t.Fatalf("message = %q, want %q", l.Result.Message, want)
	}
}

func TestPythonApplicationLargerFailureNamesInvocation881(t *testing.T) {
	r := LargerCounterReport881(t, "wrong step", true)
	l, invocation := largerRecord881Review(t, r)
	if l.Key.Target == "" || l.Key.Check == "" || l.Key.ID != "explore" {
		t.Fatalf("fixture key: %+v", l.Key)
	}
	for name, message := range map[string]string{"invocation": l.Result.Message, "overall": r.Larger.Message} {
		if !strings.HasPrefix(message, invocation+"conformance one size larger: ") || strings.Count(message, invocation) != 1 {
			t.Errorf("%s message doesn't name its invocation once, first: %q", name, message)
		}
	}
	if !strings.Contains(Feedback(r), invocation) {
		t.Errorf("feedback doesn't name the invocation: %s", Feedback(r))
	}
}

func TestPythonApplicationLargerUnavailableEveryStepSaysWhy881(t *testing.T) {
	tc, _ := largerRegressionFake881(t, largerFault881)
	t.Setenv("PA881_REVIEW_FAULT", "every step")
	r := pa342Unchanged(t, pa342New(t, true, false), tc)
	l, invocation := largerRecord881Review(t, r)
	c := l.Conformance
	if !r.Passed || l.Result.Required || l.Result.Passed || !c.OperationalError || !c.Result.Passed || c.Result.Tried == nil || c.Result.Tried.Passed {
		t.Fatalf("the fixture's every-step check alone must be unavailable: passed=%v %+v %+v", r.Passed, l.Result, c.Result)
	}
	want := invocation + "conformance one size larger was unavailable: TLC couldn't check the driver's attempts: "
	if !strings.HasPrefix(l.Result.Message, want) || !strings.Contains(l.Result.Message, "TLC fixture unavailable") || strings.ContainsAny(l.Result.Message, "\r\n") {
		t.Fatalf("an unavailable every-step check gives no reason: %q", l.Result.Message)
	}
}

// The every-step check's module alone holds the attempts' arguments, and the
// model at the next environment bounds. Its steps and starts have passed by
// then, so its result reads as passed with no message of its own. TLC ending
// it on what it can't evaluate fails all the same, as at the ratified bounds,
// and says why.
func TestPythonApplicationLargerUnreadableEveryStepFails881(t *testing.T) {
	tc, _ := largerRegressionFake881(t, largerFault881)
	t.Setenv("PA881_REVIEW_FAULT", "every step unreadable")
	r := pa342Unchanged(t, pa342New(t, true, false), tc)
	l, invocation := largerRecord881Review(t, r)
	c := l.Conformance
	if !c.Result.Passed || c.Result.Message != "" || c.Result.Tried == nil || c.Result.Tried.Passed {
		t.Fatalf("the fixture's every-step check alone must end: %+v", c.Result)
	}
	if r.Passed || !l.Result.Required || l.Result.Passed || c.OperationalError {
		t.Fatalf("an every-step check TLC couldn't evaluate claimed nothing: passed=%v %+v", r.Passed, l.Result)
	}
	want := invocation + "conformance one size larger: TLC can't check what the run recorded: TLC threw an unexpected exception. "
	if !strings.HasPrefix(l.Result.Message, want) || !strings.Contains(l.Result.Message, `Attempted to apply the operator Inc to the non-integer argument "one"`) || strings.ContainsAny(l.Result.Message, "\r\n") {
		t.Fatalf("message = %q", l.Result.Message)
	}
}

// Storage a recording can't be written to is nothing the run did, so it
// claims nothing. A recording that can't be made from the run's rows is the
// run's own, and fails.
func TestPythonApplicationLargerRecordingStorageIsOperational881(t *testing.T) {
	f := pa342New(t, true, false)
	s := &PythonApplicationSession{project: f.p, root: f.root}
	state := func(i int) map[string]any { return map[string]any{"count": json.Number(fmt.Sprint(i))} }
	no := false
	x := &PythonApplicationExecutionResult{Success: true, Complete: true, ObservationMode: "explored",
		Exploration: &PythonApplicationExploration{States: 2, Depth: 2},
		Observations: []PythonApplicationObservation{{Kind: "initial", State: state(0)},
			{Kind: "transition", Step: "Inc", Before: state(0), After: state(1), Replay: &no}}}
	work := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	check := func() PythonApplicationConformance {
		t.Helper()
		c, err := checkPythonApplicationConformance(ctx, s, x, true, work, "larger", pa342Model, map[string]string{"Local.tla": "---- MODULE Local ----\nHelper == TRUE\n====\n"},
			map[string]string{"Limit": "3"}, 4, tlc.Runner{CheckRoot: work})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	t.Setenv("TMPDIR", filepath.Join(work, "missing"))
	if c := check(); !c.OperationalError || c.Result.Passed || !strings.Contains(c.Result.Message, "missing") {
		t.Fatalf("a recording with nowhere to be written must be unavailable: %+v", c)
	}
	t.Setenv("TMPDIR", work)
	x.Observations = x.Observations[:1]
	if c := check(); c.OperationalError || c.Result.Passed || c.Result.Message != "no explored attempts" {
		t.Fatalf("a run that recorded no attempts is invalid evidence: %+v", c)
	}
	if c := (PythonApplicationConformance{Result: conformance.Result{Tried: &conformance.Tried{Message: "TLC couldn't check\nthe driver's attempts"}}}); pythonApplicationConformanceReason(c) != "TLC couldn't check the driver's attempts" {
		t.Fatalf("reason = %q", pythonApplicationConformanceReason(c))
	}
}
