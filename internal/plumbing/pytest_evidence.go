package plumbing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

type pytestEvent struct {
	Binding             RepositoryAcceptanceBinding
	Seq                 int
	Event               string
	Node, Phase         string
	Outcome, Report     string
	Reason              string
	Nodes               []string
	Calls               int
	Xfail               bool
	ExitCode            int
	SourceDigest        string
	ConfigDigest        string
	InitialConfigDigest string
}

func pytestRefusal(binding RepositoryAcceptanceBinding, kind, reason string) RepositoryAcceptanceEvidence {
	status := "refused"
	if kind == "setup" {
		status = "failed"
	}
	return RepositoryAcceptanceEvidence{ExitCode: -1, Records: []RepositoryAcceptanceRecord{{Binding: binding, Kind: kind, Status: status, Reason: reason}}}
}

// A private stream still needs the whole protocol: exact collection, the real
// call, matching reports, completed teardown, session, process and source. A
// prefix never supplies base failure or candidate completion.
func collectPytestAcceptance(binding RepositoryAcceptanceBinding, output RepositoryProcessOutput, processErr error) (RepositoryAcceptanceEvidence, error) {
	fail := func(kind, reason string) (RepositoryAcceptanceEvidence, error) {
		return pytestRefusal(binding, kind, reason), errors.New(reason)
	}
	if processErr != nil {
		return fail("setup", "pytest process or source check failed: "+processErr.Error())
	}
	if !output.Started || !output.Exited || !output.Drained || output.ExitCode < 0 {
		return fail("test", "pytest process did not start, exit and drain completely")
	}
	if len(output.Events) == 0 || output.Events[len(output.Events)-1] != '\n' {
		return fail("test", "pytest supplied no complete private event stream")
	}
	var events []pytestEvent
	for _, line := range bytes.Split(output.Events[:len(output.Events)-1], []byte{'\n'}) {
		var event pytestEvent
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			return fail("test", "invalid pytest framework event")
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fail("test", "trailing pytest event data")
		}
		if event.Binding != binding || event.Seq != len(events)+1 {
			return fail("test", "pytest evidence does not match this invocation, identity, source and configuration")
		}
		events = append(events, event)
	}
	if events[0].Event != "begin" {
		return fail("test", "pytest evidence has no invocation start")
	}
	for _, event := range events {
		if event.Event == "setup" {
			return fail("setup", "pytest setup: "+event.Reason)
		}
	}
	if len(events) < 5 || events[1].Event != "session_start" {
		return fail("setup", "pytest configuration, imports or session did not complete")
	}
	collection := events[2]
	finish, session := events[len(events)-1], events[len(events)-2]
	if collection.Event != "collection" || session.Event != "session_finish" || finish.Event != "finish" || session.ExitCode != finish.ExitCode || finish.ExitCode != output.ExitCode {
		return fail("test", "pytest collection, session and process completion are absent or conflicting")
	}
	if finish.SourceDigest != binding.SourceDigest || !executionDigest.MatchString(finish.ConfigDigest) || finish.ConfigDigest != finish.InitialConfigDigest {
		return fail("test", "pytest source or configuration changed during execution")
	}
	evidence := RepositoryAcceptanceEvidence{Complete: true, ExitCode: output.ExitCode,
		Records: []RepositoryAcceptanceRecord{{Binding: binding, Kind: "selection"}}}
	if collection.Outcome == "setup" {
		return fail("setup", collection.Reason)
	}
	if collection.Outcome == "compile" {
		if len(events) != 5 || len(collection.Nodes) != 0 || !nativeText(collection.Reason) || !slices.Contains([]int{1, 2, 4}, output.ExitCode) {
			return fail("test", "pytest import attribution lacks completed independent collection failure")
		}
		evidence.Records = append(evidence.Records, RepositoryAcceptanceRecord{Binding: binding, Kind: "compile", Status: "failed", Reason: collection.Reason})
		return evidence, nil
	}
	if collection.Outcome != "runtime" || !slices.Equal(collection.Nodes, []string{binding.Selector}) {
		return fail("test", "pytest must collect exactly the literal selected node, without groups, duplicates or deselection")
	}
	// Setup and teardown failures veto even a genuine assertion failure. Inspect
	// their observations before the successful-protocol length check, since a
	// setup failure correctly prevents the call phase from starting.
	for _, event := range events[3 : len(events)-2] {
		if event.Xfail || event.Outcome == "skipped" {
			return fail("test", "pytest skip, xfail or xpass cannot qualify acceptance")
		}
		if event.Event == "phase" && (event.Phase != "call" && event.Outcome != "passed" || event.Outcome == "setup") {
			return fail("setup", "pytest fixture or environment failure in "+event.Phase)
		}
	}
	if len(events) != 14 {
		return fail("test", "pytest needs one real call and completed setup, reports and teardown")
	}
	status, kind, reason := "passed", "test", ""
	for i, phase := range []string{"setup", "call", "teardown"} {
		observed := events[3+i*3]
		if observed.Event != "phase" || observed.Phase != phase || observed.Node != binding.Selector {
			return fail("test", "pytest phase order or selection conflicts")
		}
		wantReport := "passed"
		if phase == "call" {
			if observed.Calls != 1 {
				return fail("test", "pytest did not call the pinned function exactly once")
			}
			if observed.Outcome == "compile" && nativeText(observed.Reason) {
				status, wantReport, kind, reason = "failed", "failed", "compile", observed.Reason
			} else if observed.Outcome == "assertion" {
				status, wantReport = "failed", "failed"
			} else if observed.Outcome != "passed" {
				return fail("setup", "pytest call failed without a completed assertion")
			}
		} else if observed.Outcome != "passed" {
			return fail("setup", "pytest "+phase+" did not pass")
		}
		for j, eventName := range []string{"report", "logreport"} {
			report := events[4+i*3+j]
			if report.Event != eventName || report.Node != binding.Selector || report.Phase != phase || report.Outcome != observed.Outcome || report.Report != wantReport {
				return fail("test", "pytest report conflicts with the observed framework call")
			}
		}
	}
	if status == "passed" && output.ExitCode != 0 || status == "failed" && output.ExitCode != 1 {
		return fail("test", "pytest outcome conflicts with its completed process exit")
	}
	if kind == "compile" {
		evidence.Records = append(evidence.Records, RepositoryAcceptanceRecord{Binding: binding, Kind: kind, Status: status, Reason: reason})
		return evidence, nil
	}
	evidence.Records = append(evidence.Records,
		RepositoryAcceptanceRecord{Binding: binding, Kind: "test", Status: "running"},
		RepositoryAcceptanceRecord{Binding: binding, Kind: "test", Status: status, Reason: fmt.Sprintf("exact pytest call %s; teardown and session completed", status)})
	return evidence, nil
}
