package plumbing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

type pytestEvent struct {
	Binding    RepositoryAcceptanceBinding
	Seq        int
	Event      string
	IDs        []string
	Node       string
	When       string
	Actual     string
	Outcome    string
	Xfail      bool
	Attributed bool
	Reason     string
	Code       int
}

func pytestRefusal(binding RepositoryAcceptanceBinding, kind, reason string) RepositoryAcceptanceEvidence {
	status := "refused"
	if kind == "setup" {
		status = "failed"
	}
	return RepositoryAcceptanceEvidence{ExitCode: -1, Records: []RepositoryAcceptanceRecord{{Binding: binding, Kind: kind, Status: status, Reason: reason}}}
}

// Only this private stream supplies pytest verdicts. Every event repeats the
// host binding and sequence; console bytes, process success and reports without
// the actual call cannot establish an individual outcome.
func collectPytestAcceptance(in RepositoryAcceptanceInvocation, out RepositoryProcessOutput) (RepositoryAcceptanceEvidence, error) {
	refuse := func(kind, reason string) (RepositoryAcceptanceEvidence, error) {
		return pytestRefusal(in.Binding, kind, reason), errors.New(reason)
	}
	if !out.Started || !out.Exited || !out.Drained || out.ExitCode < 0 {
		return refuse("setup", "pytest process did not start, exit and drain completely")
	}
	if len(out.Events) == 0 || out.Events[len(out.Events)-1] != '\n' {
		return refuse("setup", "pytest supplied no complete private event stream")
	}
	var events []pytestEvent
	for _, line := range bytes.Split(bytes.TrimSuffix(out.Events, []byte("\n")), []byte("\n")) {
		var event pytestEvent
		d := json.NewDecoder(bytes.NewReader(line))
		d.DisallowUnknownFields()
		if err := d.Decode(&event); err != nil {
			return refuse("test", "malformed pytest evidence: "+err.Error())
		}
		if d.Decode(new(any)) != io.EOF || event.Binding != in.Binding || event.Seq != len(events)+1 {
			return refuse("test", "pytest evidence has conflicting, replayed or mismatched bindings")
		}
		events = append(events, event)
	}
	if len(events) < 2 || events[0].Event != "begin" {
		return refuse("setup", "pytest evidence has no authentic beginning")
	}
	var names []string
	var collected []pytestEvent
	var imports []pytestEvent
	var phases []pytestEvent
	var body pytestEvent
	for i, event := range events {
		names = append(names, event.Event)
		switch event.Event {
		case "begin":
			if i != 0 {
				return refuse("test", "duplicate pytest beginning")
			}
		case "setup":
			return refuse("setup", event.Reason)
		case "refused":
			return refuse("test", event.Reason)
		case "collection":
			collected = append(collected, event)
		case "collect_error":
			imports = append(imports, event)
		case "phase", "body_start", "body_end", "protocol_start", "protocol_end":
			if event.Node != in.Binding.Selector {
				return refuse("test", "pytest runtime node differs from the exact selection")
			}
			if event.Event == "phase" {
				phases = append(phases, event)
			}
			if event.Event == "body_end" {
				body = event
			}
		case "session_start", "session_end":
		case "finish":
			if i != len(events)-1 {
				return refuse("test", "pytest evidence continues after completion")
			}
		default:
			return refuse("test", "unknown pytest event "+event.Event)
		}
	}
	if len(events) < 4 || events[len(events)-1].Event != "finish" || events[len(events)-2].Event != "session_end" {
		return refuse("setup", "pytest did not complete its framework session")
	}
	if events[len(events)-1].Code != out.ExitCode || events[len(events)-2].Code != out.ExitCode {
		return refuse("test", "pytest session and process exits conflict")
	}
	record := func(kind, status, reason string) RepositoryAcceptanceRecord {
		return RepositoryAcceptanceRecord{Binding: in.Binding, Kind: kind, Status: status, Reason: reason}
	}
	evidence := RepositoryAcceptanceEvidence{Complete: true, ExitCode: out.ExitCode, Records: []RepositoryAcceptanceRecord{record("selection", "", "exact pinned pytest selection")}}
	if len(imports) != 0 {
		order := strings.Join(names, ",")
		if len(imports) != 1 || !imports[0].Attributed || out.ExitCode != 2 && out.ExitCode != 4 ||
			order != "begin,session_start,collect_error,session_end,finish" && order != "begin,session_start,collect_error,collection,session_end,finish" ||
			len(collected) > 1 || len(collected) == 1 && len(collected[0].IDs) != 0 || imports[0].Reason == "" {
			return refuse("setup", "collection failure has no unambiguous selected missing production API attribution")
		}
		evidence.Records = append(evidence.Records, record("compile", "failed", imports[0].Reason))
		return evidence, nil
	}
	if len(collected) != 1 || !slices.Equal(collected[0].IDs, []string{in.Binding.Selector}) {
		return refuse("test", "pytest must collect exactly one literal matching item")
	}
	for _, phase := range phases {
		if phase.When != "call" && (phase.Actual != "passed" || phase.Outcome != "passed") {
			return refuse("setup", "pytest fixture or environment failure: "+phase.Reason)
		}
		if phase.Xfail || phase.Actual == "skipped" || phase.Actual == "xfail" {
			return refuse("test", "pytest skip, xfail or xpass cannot supply acceptance")
		}
	}
	expected := "begin,session_start,collection,protocol_start,phase,body_start,body_end,phase,phase,protocol_end,session_end,finish"
	if strings.Join(names, ",") != expected || len(phases) != 3 || phases[0].When != "setup" || phases[1].When != "call" || phases[2].When != "teardown" {
		return refuse("test", "pytest needs one real call and complete setup, teardown and protocol")
	}
	call := phases[1]
	if body.Actual != call.Actual {
		return refuse("test", "pytest report conflicts with the actual selected call")
	}
	status := ""
	switch call.Actual {
	case "passed":
		if call.Outcome == "passed" && out.ExitCode == 0 {
			status = "passed"
		}
	case "assertion":
		if call.Outcome == "failed" && out.ExitCode == 1 {
			status = "failed"
		}
	default:
		return refuse("setup", "pytest call did not complete normally or with an assertion: "+call.Reason)
	}
	if status == "" {
		return refuse("test", "pytest report or process status conflicts with observed execution")
	}
	evidence.Records = append(evidence.Records, record("test", "running", "selected pytest function called"), record("test", status, fmt.Sprintf("pytest %s: %s", status, call.Reason)))
	return evidence, nil
}
