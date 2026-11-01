package synth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Codex ends its turn itself at the first reply that reaches the model's
// output limit, so that failure is its loop guard's stop, and a build runs
// it once more at the fallback effort (D-0257). These tests replay that
// failure through a stand-in for codex, in the events codex-cli 0.163.0's
// source gives it.

// outputLimitMessage is what codex says of a turn a reply's output limit
// ended.
const outputLimitMessage = "Incomplete response returned, reason: max_output_tokens"

// codexOutputLimit is how codex exec ends a run once a reply reaches the
// model's output limit: the Responses API's incomplete response, reason
// max_output_tokens, is an error Codex never retries, so it fails the turn
// there, says why in an error event and in the failed turn, and exits 1. The
// reply itself shows nothing: an agent message or a tool call shows only
// once the model has finished writing it, reasoning only as a completed item
// with a summary, and a failed turn reports no tokens.
const codexOutputLimit = `{"type":"thread.started","thread_id":"t1"}
{"type":"turn.started"}
{"type":"item.started","item":{"id":"item_0","type":"command_execution","command":"ls","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"ls","aggregated_output":"model.tla\n","exit_code":0,"status":"completed"}}
{"type":"error","message":"Incomplete response returned, reason: max_output_tokens"}
{"type":"turn.failed","error":{"message":"Incomplete response returned, reason: max_output_tokens"}}
`

// atTheLimit is a run of the stand-in that a reply's output limit ends.
var atTheLimit = providerAttempt{out: codexOutputLimit, exit: 1}

// standInEfforts is the effort codex was told on each of the stand-in's
// runs, in order.
func standInEfforts(t *testing.T, dir string) []string {
	t.Helper()
	var efforts []string
	for n := 1; n <= providerCount(t, dir); n++ {
		b, err := os.ReadFile(filepath.Join(dir, "args."+strconv.Itoa(n)))
		if err != nil {
			t.Fatal(err)
		}
		effort := ""
		for _, arg := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(arg, "model_reasoning_effort="); ok {
				effort = strings.Trim(v, `"`)
			}
		}
		efforts = append(efforts, effort)
	}
	return efforts
}

// standInWorkspace is where the stand-in's nth run ran.
func standInWorkspace(t *testing.T, dir string, n int) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "workspace."+strconv.Itoa(n)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// buildJob is a build's job: a workspace of its own, and Invariant's server.
func buildJob(t *testing.T) Job {
	return Job{Workspace: t.TempDir(), Prompt: "build", GateServer: []string{"/bin/invariant"}}
}

// A Codex build whose reply reaches the model's output limit runs once more
// at the fallback effort, in the same workspace, and says it fell back and
// why, quoting Codex's reason. The fallback's success is the build's.
func TestACodexBuildStoppedAtTheOutputLimitRunsOnceMoreAtTheFallbackEffort(t *testing.T) {
	b, dir := providerBackend(t, "codex", atTheLimit, providerSuccess("codex"))
	u, fb, err := RunAgent(providerContext(t), b, buildJob(t), 30*time.Second, "medium")
	if err != nil || u.Outcome != "success" {
		t.Errorf("usage %+v, error %v; want the fallback's success", u, err)
	}
	if got := standInEfforts(t, dir); !slices.Equal(got, []string{"high", "medium"}) {
		t.Fatalf("codex ran at %q; want once at its own effort, high, then once more at the fallback's, medium", got)
	}
	if standInWorkspace(t, dir, 2) != standInWorkspace(t, dir, 1) {
		t.Errorf("the fallback ran in %s, not in the first run's workspace, %s", standInWorkspace(t, dir, 2), standInWorkspace(t, dir, 1))
	}
	switch {
	case fb == nil:
		t.Error("the build doesn't say it fell back")
	case fb.Effort != "medium" || !strings.Contains(fb.Why, outputLimitMessage) || !strings.Contains(fb.Why, ErrOutputLimit.Error()):
		t.Errorf("the build says it fell back to %q because %q; want medium, because of Codex's reason, %q", fb.Effort, fb.Why, outputLimitMessage)
	}
}

// A fallback that the output limit stops too fails the build with Codex's
// reason, and the build still says it fell back. There's never a third run.
func TestACodexFallbackStoppedAtTheOutputLimitFailsTheBuildWithCodexsReason(t *testing.T) {
	b, dir := providerBackend(t, "codex", atTheLimit, atTheLimit, providerSuccess("codex"))
	u, fb, err := RunAgent(providerContext(t), b, buildJob(t), 30*time.Second, "medium")
	if err == nil || !errors.Is(err, ErrOutputLimit) || !strings.Contains(err.Error(), outputLimitMessage) || u.Outcome != "failed" {
		t.Errorf("usage %+v, error %v; want the build failed at the output limit, with Codex's reason", u, err)
	}
	if ProviderUnavailable(err) {
		t.Errorf("error %v; want the loop guard's stop, not the provider's failure", err)
	}
	if fb == nil || fb.Effort != "medium" || !strings.Contains(fb.Why, outputLimitMessage) {
		t.Errorf("fallback %+v; want the build to say it fell back to medium, because of Codex's reason", fb)
	}
	if got := standInEfforts(t, dir); !slices.Equal(got, []string{"high", "medium"}) {
		t.Errorf("codex ran at %q; want exactly twice, at high, then at medium", got)
	}
}

// Without a fallback effort, as -fallback-effort "" gives, or one that isn't
// below the build's own, a Codex build the output limit stops ends there,
// after one run, with Codex's reason.
func TestACodexBuildWithoutAFallbackEffortEndsAtTheOutputLimit(t *testing.T) {
	b, dir := providerBackend(t, "codex", atTheLimit, providerSuccess("codex"))
	_, fb, err := RunAgent(providerContext(t), b, buildJob(t), 30*time.Second, "")
	if err == nil || !errors.Is(err, ErrOutputLimit) || !strings.Contains(err.Error(), outputLimitMessage) || ProviderUnavailable(err) {
		t.Errorf("error %v; want the build failed at the output limit, with Codex's reason", err)
	}
	if fb != nil {
		t.Errorf("fallback %+v; want none", fb)
	}
	if n := providerCount(t, dir); n != 1 {
		t.Errorf("codex ran %d times; want once", n)
	}
}

// A provider's failure takes the outage's way, the same job tried again at
// the same effort, never the fallback, and the output limit is never the
// provider's failure (#261). What the attempt's failed turn says decides,
// over an earlier error event, and a fallback whose provider fails is tried
// again at the fallback's effort.
func TestCodexsOutagesAndItsOutputLimitTakeTheirOwnWays(t *testing.T) {
	capacity := providerCodexFailure(providerCapacity)
	limitThenCapacity := providerAttempt{out: `{"type":"turn.started"}
{"type":"error","message":"Incomplete response returned, reason: max_output_tokens"}
{"type":"turn.failed","error":{"message":"` + providerCapacity + `"}}
`, exit: 1}
	for _, c := range []struct {
		name     string
		attempts []providerAttempt
		efforts  []string
		fellBack bool
		outage   bool // the build failed because its provider stayed unavailable
	}{
		{"an outage, then success", []providerAttempt{capacity, providerSuccess("codex")}, []string{"high", "high"}, false, false},
		{"an outage that lasts", []providerAttempt{capacity, capacity, capacity, capacity, providerSuccess("codex")}, []string{"high", "high", "high", "high"}, false, true},
		{"a failed turn at capacity after an error at the limit", []providerAttempt{limitThenCapacity, providerSuccess("codex")}, []string{"high", "high"}, false, false},
		{"the output limit, then an outage in the fallback", []providerAttempt{atTheLimit, capacity, providerSuccess("codex")}, []string{"high", "medium", "medium"}, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, dir := providerBackend(t, "codex", c.attempts...)
			b = providerWithWait(b, providerNoWait)
			u, fb, err := RunAgent(providerContext(t), b, buildJob(t), 30*time.Second, "medium")
			if got := standInEfforts(t, dir); !slices.Equal(got, c.efforts) {
				t.Errorf("codex ran at %q; want %q", got, c.efforts)
			}
			if (fb != nil) != c.fellBack {
				t.Errorf("fallback %+v; want a fallback %v", fb, c.fellBack)
			}
			switch {
			case c.outage && (!ProviderUnavailable(err) || errors.Is(err, ErrOutputLimit)):
				t.Errorf("error %v; want the provider unavailable, and not the output limit", err)
			case !c.outage && (err != nil || u.Outcome != "success"):
				t.Errorf("usage %+v, error %v; want success", u, err)
			}
		})
	}
	// A run its provider stopped never takes the fallback, even when its
	// last attempt, cut off by the run's deadline, ended at the output limit.
	limit := fmt.Errorf("%w; codex stopped: %s", ErrOutputLimit, outputLimitMessage)
	stopped := &ProviderError{Backend: "codex", Attempts: 2, Last: errors.New(providerCapacity),
		Stopped: fmt.Errorf("its next attempt didn't finish (%w): %w", context.DeadlineExceeded, limit)}
	for err, want := range map[error]bool{limit: true, ErrThinkingLoop: true, stopped: false, errors.New(outputLimitMessage): false} {
		if got := loopGuardStop(err); got != want {
			t.Errorf("loopGuardStop(%v) = %v; want %v", err, got, want)
		}
	}
}

// Codex's failure is the output limit only when its reason says so, in the
// failed turn's words or else its last error event's, whatever comes before
// or after it; never another reason, and never the words on stderr or in a
// command's output (D-0257).
func TestCodexReadsTheOutputLimitFromItsReason(t *testing.T) {
	for failure, want := range map[string]bool{
		outputLimitMessage: true,
		"Error running remote compact task: Incomplete response returned, reason: max_output_tokens":  true,
		"Incomplete response returned, reason: max_output_tokens (request id: req_1)":                 true,
		"Incomplete response returned, reason: content_filter":                                        false,
		"stream disconnected before completion: Incomplete response returned, reason: content_filter": false,
		"Incomplete response returned, reason: max_output_tokens_soon":                                false,
		"invalid value for max_output_tokens":                                                         false,
		providerCapacity:                                                                              false,
		"We're currently experiencing high demand, which may cause temporary errors.":                 false,
		"": false,
	} {
		if got := outputLimit(failure); got != want {
			t.Errorf("outputLimit(%q) = %v; want %v", failure, got, want)
		}
		if want && transient(failure) {
			t.Errorf("transient(%q) = true; the output limit is never the provider's failure", failure)
		}
	}
	quoted := strings.Replace(codexRun, `"aggregated_output":"model.tla\n"`, `"aggregated_output":"`+outputLimitMessage+`\n"`, 1)
	for name, c := range map[string]struct {
		attempt providerAttempt
		want    bool
	}{
		"an error event at the limit, the turn unfinished": {providerAttempt{out: `{"type":"turn.started"}` + "\n" + `{"type":"error","message":"` + outputLimitMessage + `"}` + "\n", exit: 1}, true},
		"the reason on stderr alone":                       {providerAttempt{out: providerCodexFailure("turn failed").out, stderr: "Turn error: " + outputLimitMessage + "\n", exit: 1}, false},
		"the words in a command's output, the turn done":   {providerAttempt{out: quoted}, false},
	} {
		t.Run(name, func(t *testing.T) {
			b, _ := providerBackend(t, "codex", c.attempt)
			_, err := b.Run(providerContext(t), buildJob(t))
			if got := errors.Is(err, ErrOutputLimit); got != c.want {
				t.Errorf("error %v; want the output limit %v", err, c.want)
			}
		})
	}
}

// A Codex synthesis that the output limit stops falls back end to end: it
// runs Codex once more at the fallback effort, in the same workspace, its
// result says it fell back and why, and the fallback's success is the run's.
// Without the verifiers here, the final gate may not run, so the error can
// be the gate's, never the agent's.
func TestACodexSynthesisStoppedAtTheOutputLimitFallsBack(t *testing.T) {
	b, dir := providerBackend(t, "codex", atTheLimit, providerSuccess("codex"))
	out := filepath.Join(t.TempDir(), "out")
	r, err := Synthesize(context.Background(), Options{Project: example, Out: out, Backend: b, Binary: "invariant",
		KeepModel: true, GateRuns: 4, Timeout: time.Minute, FallbackEffort: "medium"})
	if err != nil && (errors.Is(err, ErrOutputLimit) || strings.Contains(err.Error(), "the agent's run failed")) {
		t.Errorf("the synthesis failed with its agent's error, though the fallback finished: %v", err)
	}
	if r == nil {
		t.Fatalf("the synthesis gave no result: %v", err)
	}
	if got := standInEfforts(t, dir); !slices.Equal(got, []string{"high", "medium"}) {
		t.Fatalf("codex ran at %q; want once at high, then once more at medium", got)
	}
	if standInWorkspace(t, dir, 2) != standInWorkspace(t, dir, 1) {
		t.Errorf("the fallback ran in %s, not in the first run's workspace, %s", standInWorkspace(t, dir, 2), standInWorkspace(t, dir, 1))
	}
	if r.Usage.Outcome != "success" {
		t.Errorf("the run's outcome is %q; want the fallback's success", r.Usage.Outcome)
	}
	if fb := r.Fallback; fb == nil || fb.Effort != "medium" || !strings.Contains(fb.Why, outputLimitMessage) {
		t.Errorf("the result says it fell back as %+v; want to medium, because of Codex's reason, %q", fb, outputLimitMessage)
	}
	if s := Summary(r); !strings.Contains(s, "ran once more, at medium effort") || !strings.Contains(s, outputLimitMessage) {
		t.Errorf("the synthesis's summary doesn't say it fell back to medium, and why:\n%s", s)
	}
}
