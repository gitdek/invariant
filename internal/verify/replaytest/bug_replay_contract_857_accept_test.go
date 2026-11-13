package replaytest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

// Version 1 request/result contract, shared by subsequent adapters:
// NewBugReplayRequest(statement, proposalHash, checkedPath) returns an owned
// request with Version, ID (stable content identity), InvocationID (fresh),
// Bug (the complete ratified statement), Proposal and Path. Only current bugs
// and valid bound paths qualify. The path and expected verdict come from the
// gate; neither a driver nor a random search may replace them.
//
// BugReplayResult has Version, RequestID, InvocationID, Outcome, Initial,
// Calls. Outcome is complete, refused, reproduced or failed. Each
// BugReplayCall has Index, Action, Args, Before, After, Dispatched, Outcome
// (returned, refused or failed) and Error. A refusal here is a normally
// completed, explicitly observed operation refusal; an exception is Error,
// never a successful refusal. BugReplayObservation independently supplies
// RequestID, InvocationID, Initial, Calls, Started, Completed and ExitCode.
// These observations must be factory-owned actual-dispatch evidence supplied
// by an execution adapter, never driver-declared result flags. CheckBugReplay
// validates the result against this independent transcript and the request,
// and asks TLC to check a non-forbidden alternative against the same snapshot
// in Invariant_ReplayResult.tla with Invariant_ReplayAllowed. It returns
// BugReplayEvidence with Required, Status, Passed, Request, Result and Message.
// Evaluating supplied contract records DOES NOT attach gate evidence or turn
// Report.Passed green; this step's real gate has no execution adapter.
//
// Success needs the exact initial state and every prefix call, in order,
// followed by an actually dispatched offending call. Its forbidden result
// is failure (reproduced); a normal refusal or returned alternative must be
// model-allowed and checked. No skipped prefix, early stop, stale invocation,
// missing dispatch, forged refusal, arbitrary exception or changed state is
// success. The full TLC suffix remains retained when its offending call is
// earlier than the final violation; code need reproduce only through the
// offending call, since rejecting it makes that forbidden suffix impossible.

func clone857[T any](t *testing.T, x T) T {
	t.Helper()
	b, e := json.Marshal(x)
	if e != nil {
		t.Fatal(e)
	}
	var y T
	if e = json.Unmarshal(b, &y); e != nil {
		t.Fatal(e)
	}
	return y
}

func successfulResult857(t *testing.T, q verify.BugReplayRequest) (verify.BugReplayResult, verify.BugReplayObservation) {
	t.Helper()
	r := verify.BugReplayResult{Version: 1, RequestID: q.ID, InvocationID: q.InvocationID, Outcome: "refused", Initial: clone857(t, q.Path.States[0])}
	for i, step := range q.Path.Steps[:q.Path.Offending+1] {
		c := verify.BugReplayCall{Index: i, Action: step.Action, Args: clone857(t, step.Args), Before: clone857(t, q.Path.States[step.From]), After: clone857(t, q.Path.States[step.To]), Dispatched: true, Outcome: "returned"}
		if i == q.Path.Offending {
			c.After = clone857(t, c.Before)
			c.Outcome = "refused"
		}
		r.Calls = append(r.Calls, c)
	}
	o := verify.BugReplayObservation{RequestID: q.ID, InvocationID: q.InvocationID, Initial: clone857(t, r.Initial), Calls: clone857(t, r.Calls), Started: true, Completed: true, ExitCode: 0}
	return r, o
}

func TestReplayResultRequiresActualOrderedOffendingCall857(t *testing.T) {
	dir, tc, log := gateFixture857(t, "typescript", []string{"current"}, false)
	r := run857(t, dir, tc, true)
	if r.Bugs[0].Replay == nil || r.Bugs[0].Replay.Request == nil {
		t.Fatal("no prepared request")
	}
	q := *r.Bugs[0].Replay.Request
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	check := func(result verify.BugReplayResult, observed verify.BugReplayObservation) (verify.BugReplayEvidence, error) {
		return verify.CheckBugReplay(ctx, runner, filepath.Join(dir, ".invariant/specs"), q, result, observed)
	}
	result, observed := successfulResult857(t, q)
	good, err := check(result, observed)
	if err != nil || !good.Passed || good.Status != "refused" {
		t.Fatalf("observed, checked refusal did not satisfy replay contract: %+v %v", good, err)
	}
	// A normally returning actual call may choose another permitted successor.
	alternative := clone857(t, result)
	alternative.Outcome = "complete"
	last := len(alternative.Calls) - 1
	alternative.Calls[last].Outcome = "returned"
	alternative.Calls[last].After = clone857(t, q.Path.States[0])
	alternateObservation := clone857(t, observed)
	alternateObservation.Calls = clone857(t, alternative.Calls)
	good, err = check(alternative, alternateObservation)
	if err != nil || !good.Passed || good.Status != "complete" {
		t.Fatalf("model-allowed actual alternative refused: %+v %v", good, err)
	}
	// Match both records where appropriate: agreement between forged records
	// alone cannot replace the original requested path or actual dispatch.
	cases := []struct {
		name string
		edit func(*verify.BugReplayResult, *verify.BugReplayObservation)
	}{
		{"stale invocation", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.InvocationID = "old"
			o.InvocationID = "old"
		}},
		{"wrong request", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.RequestID = "other"
			o.RequestID = "other"
		}},
		{"wrong version", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) { r.Version = 2 }},
		{"unknown outcome", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) { r.Outcome = "success" }},
		{"changed initial state", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Initial = clone857(t, q.Path.States[1])
			o.Initial = clone857(t, r.Initial)
		}},
		{"skipped setup", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Calls = r.Calls[1:]
			o.Calls = clone857(t, r.Calls)
		}},
		{"reordered setup", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Calls[0], r.Calls[1] = r.Calls[1], r.Calls[0]
			o.Calls = clone857(t, r.Calls)
		}},
		{"missing dispatch", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Calls[last].Dispatched = false
			o.Calls = clone857(t, r.Calls)
		}},
		{"wrong action", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Calls[last].Action = "Done"
			o.Calls = clone857(t, r.Calls)
		}},
		{"wrong typed argument", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Calls[0].Args[0].Kind = "string"
			o.Calls = clone857(t, r.Calls)
		}},
		{"prefix diverged", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Calls[0].After = clone857(t, q.Path.States[0])
			o.Calls = clone857(t, r.Calls)
		}},
		{"premature stop", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Calls = r.Calls[:last]
			o.Calls = clone857(t, r.Calls)
		}},
		{"unfinished process", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) { o.Completed = false }},
		{"failed process", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) { o.ExitCode = 1 }},
		{"no actual invocation", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) { o.Started = false }},
		{"fabricated refusal", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			o.Calls[last].Outcome = "failed"
			o.Calls[last].Error = "ValueError"
		}},
		{"arbitrary exception", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Calls[last].Error = "unrelated network failure"
			o.Calls = clone857(t, r.Calls)
		}},
		{"reproduced bug", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Outcome = "reproduced"
			r.Calls[last].Outcome = "returned"
			r.Calls[last].After = clone857(t, q.Path.States[3])
			o.Calls = clone857(t, r.Calls)
		}},
		{"forbidden state called refusal", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Calls[last].After = clone857(t, q.Path.States[3])
			o.Calls = clone857(t, r.Calls)
		}},
		{"model-disallowed alternative", func(r *verify.BugReplayResult, o *verify.BugReplayObservation) {
			r.Outcome = "complete"
			r.Calls[last].Outcome = "returned"
			v, _ := tlc.ParseReplayValue("99")
			r.Calls[last].After["n"] = v
			o.Calls = clone857(t, r.Calls)
		}},
	}
	for _, tc := range cases {
		rc, oc := clone857(t, result), clone857(t, observed)
		tc.edit(&rc, &oc)
		e, err := check(rc, oc)
		if err == nil && e.Passed {
			t.Errorf("%s satisfied replay: %+v", tc.name, e)
		}
		if tc.name == "reproduced bug" && err == nil && e.Status != "reproduced" {
			t.Errorf("lost reproduced outcome: %+v", e)
		}
	}
	b, e := os.ReadFile(log)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "Invariant_ReplayResult.tla") {
		t.Fatal("allowed/refused code alternatives were never checked by TLC")
	}
	// Results cannot be checked against a different sibling snapshot.
	write857(t, dir, ".invariant/specs/Local.tla", "---- MODULE Local ----\nHelper == FALSE\n====\n")
	if e, err := check(result, observed); err == nil && e.Passed {
		t.Fatal("replay accepted against substituted model includes")
	}
}

func TestReplayIdentityAndFingerprintsBindStableEvidence857(t *testing.T) {
	dir, tc, _ := gateFixture857(t, "typescript", []string{"current"}, false)
	r := run857(t, dir, tc, true)
	if r.Bugs[0].Replay == nil || r.Bugs[0].Replay.Request == nil {
		t.Fatal("no prepared replay")
	}
	q := *r.Bugs[0].Replay.Request
	newRequest, err := verify.NewBugReplayRequest(q.Bug, q.Proposal, q.Path)
	if err != nil {
		t.Fatal(err)
	}
	if newRequest.ID != q.ID || newRequest.InvocationID == "" || newRequest.InvocationID == q.InvocationID {
		t.Fatal("stable request and fresh invocation identities were conflated")
	}
	// Caller mutation after construction must not alter retained requests.
	saved := clone857(t, newRequest)
	q.Path.States[0]["n"] = q.Path.States[1]["n"]
	if newRequest.Path.States[0]["n"].Atom != saved.Path.States[0]["n"].Atom {
		t.Fatal("request aliases caller-owned input")
	}
	q = saved
	result, _ := successfulResult857(t, q)
	r.Bugs[0].Replay.Request = &q
	r.Bugs[0].Replay.Result = &result
	base := verify.Fingerprint(*r)
	volatile := clone857(t, *r)
	volatile.Bugs[0].Trace = "/private/another/trace.json"
	volatile.Bugs[0].Replay.Request.InvocationID = "another-process"
	volatile.Bugs[0].Replay.Result.InvocationID = "another-process"
	volatile.GeneratedAt = "tomorrow"
	if verify.Fingerprint(volatile) != base {
		t.Fatal("private paths/invocation identities changed portable fingerprint")
	}
	for _, edit := range []func(*verify.Report){
		func(r *verify.Report) { r.Bugs[0].CodeBehavior = "hypothetical" },
		func(r *verify.Report) { r.Bugs[0].Replay.Request.Bug.Expect = "Other" },
		func(r *verify.Report) { r.Bugs[0].Replay.Request.Path.IncludesSHA256["Local.tla"] = "changed" },
		func(r *verify.Report) { r.Bugs[0].Replay.Request.Path.Bounds["Limit"] = "4" },
		func(r *verify.Report) { r.Bugs[0].Replay.Request.Path.Tool.JarSHA256 = "changed" },
		func(r *verify.Report) { r.Bugs[0].Replay.Request.Path.Steps[0].Args[0].Atom = "p2" },
		func(r *verify.Report) { r.Bugs[0].Replay.Result.Outcome = "reproduced" },
		func(r *verify.Report) { r.Bugs[0].Replay.Result.Calls[0].Dispatched = false },
	} {
		changed := clone857(t, *r)
		edit(&changed)
		if verify.Fingerprint(changed) == base {
			t.Fatal("stable replay evidence omitted from fingerprint")
		}
	}
	for _, class := range []string{"", "hypothetical"} {
		s := q.Bug
		s.CodeBehavior = class
		if _, e := verify.NewBugReplayRequest(s, q.Proposal, q.Path); e == nil {
			t.Errorf("%q bug created mandatory request", class)
		}
	}
	for _, edit := range []func(*project.Statement){
		func(s *project.Statement) { s.Name = "OtherBug" },
		func(s *project.Statement) { s.Expect = "OtherInvariant" },
		func(s *project.Statement) { s.SHA256 = "sha256:other-pin" },
	} {
		s := q.Bug
		edit(&s)
		if _, e := verify.NewBugReplayRequest(s, q.Proposal, q.Path); e == nil {
			t.Fatal("a path was reassigned to another ratified bug")
		}
	}
}
