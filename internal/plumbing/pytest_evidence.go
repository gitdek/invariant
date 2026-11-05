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
	Event   string
	Binding RepositoryAcceptanceBinding
	State   *pytestState `json:",omitempty"`
}

type pytestState struct {
	Collected                               []string
	Calls                                   int
	Phases                                  []pytestPhase
	Session                                 int
	ExitCode                                int
	Invalid                                 []string
	Kind, Reason                            string
	Import                                  *pytestImport
	SourceDigest, ConfigBefore, ConfigAfter string
}

type pytestPhase struct {
	Node, When, Raw, Outcome, Exception string
	Xfail                               bool
}

type pytestImport struct {
	Module, Name, File string
	Line               int
}

// The integration reports observations, not an authoritative verdict string.
// Require the exact lifecycle independently of console output or suite status.
func collectPytestAcceptance(in RepositoryAcceptanceInvocation, output RepositoryProcessOutput, runErr error) (RepositoryAcceptanceEvidence, error) {
	evidence := RepositoryAcceptanceEvidence{ExitCode: output.ExitCode}
	refuse := func(kind, reason string) (RepositoryAcceptanceEvidence, error) {
		evidence.Complete = false
		status := "refused"
		if kind == "setup" {
			status = "failed"
		}
		evidence.Records = []RepositoryAcceptanceRecord{{Binding: in.Binding, Kind: kind, Status: status, Reason: reason}}
		return evidence, errors.Join(runErr, errors.New(reason))
	}
	if runErr != nil || !output.Started || !output.Exited || !output.Drained || output.ExitCode < 0 {
		return refuse("setup", "pytest process and evidence channels did not complete")
	}
	lines := bytes.Split(output.Events, []byte{'\n'})
	if len(lines) != 3 || len(lines[2]) != 0 {
		return refuse("test", "pytest needs exactly one start and one complete framework record")
	}
	var events [2]pytestEvent
	for i, line := range lines[:2] {
		if err := pytestUniqueJSON(line); err != nil {
			return refuse("test", "conflicting pytest framework evidence: "+err.Error())
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&events[i]); err != nil {
			return refuse("test", "invalid pytest framework evidence: "+err.Error())
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return refuse("test", "extra pytest framework evidence")
		}
		if events[i].Binding != in.Binding {
			return refuse("test", "pytest evidence does not match the fresh invocation, full identity, source and configuration")
		}
	}
	state := events[1].State
	if events[0].Event != "start" || events[0].State != nil || events[1].Event != "finish" || state == nil || state.ExitCode != output.ExitCode {
		return refuse("test", "conflicting pytest framework lifecycle or process exit")
	}
	if state.Kind == "setup" {
		return refuse("setup", "pytest setup: "+state.Reason)
	}
	if len(state.Invalid) != 0 {
		return refuse("test", fmt.Sprintf("pytest refused: %v", state.Invalid))
	}
	if state.Session != 1 || state.SourceDigest != in.Binding.SourceDigest || !executionDigest.MatchString(state.ConfigBefore) || state.ConfigBefore != state.ConfigAfter {
		return refuse("test", "pytest session, pinned source or configuration did not complete unchanged")
	}
	selection := RepositoryAcceptanceRecord{Binding: in.Binding, Kind: "selection"}
	terminal := RepositoryAcceptanceRecord{Binding: in.Binding, Kind: "test"}
	if state.Kind == "compile" {
		api := state.Import
		if api == nil || !nativeText(api.Module) || !pytestIdentifier.MatchString(api.Name) || !repositoryAcceptancePath(api.File) || api.Line <= 0 || len(state.Collected) != 0 || len(state.Phases) != 0 || state.Calls != 0 || state.ExitCode != 2 && state.ExitCode != 4 || !nativeText(state.Reason) {
			return refuse("test", "missing API evidence lacks completed, unambiguous selected import attribution")
		}
		terminal.Kind, terminal.Status, terminal.Reason = "compile", "failed", state.Reason
		evidence.Complete = true
		evidence.Records = []RepositoryAcceptanceRecord{selection, terminal}
		return evidence, nil
	}
	for _, phase := range state.Phases {
		if phase.Node == in.Binding.Selector && (phase.When == "setup" || phase.When == "teardown") && phase.Raw == "failed" && phase.Outcome == "failed" && !phase.Xfail {
			return refuse("setup", "pytest fixture "+phase.When+" failed")
		}
	}
	if state.Kind != "test" || state.Import != nil || !slices.Equal(state.Collected, []string{in.Binding.Selector}) || state.Calls != 1 || len(state.Phases) != 3 {
		return refuse("test", "pytest needs exactly one collected item and its actual call with all three phases")
	}
	for i, when := range []string{"setup", "call", "teardown"} {
		phase := state.Phases[i]
		if phase.Node != in.Binding.Selector || phase.When != when || phase.Xfail || phase.Raw != phase.Outcome || phase.Outcome != "passed" && phase.Outcome != "failed" {
			return refuse("test", "pytest reported skipped, xfail, conflicting or out-of-order phases")
		}
		if when != "call" && (phase.Outcome != "passed" || phase.Exception != "") {
			return refuse("setup", "pytest fixture "+when+" failed")
		}
	}
	call := state.Phases[1]
	if call.Outcome == "passed" && call.Exception == "" && state.ExitCode == 0 {
		terminal.Status, terminal.Reason = "passed", "exact pytest call, teardown and session passed"
	} else if call.Outcome == "failed" && call.Exception == "builtins.AssertionError" && state.ExitCode == 1 {
		terminal.Status, terminal.Reason = "failed", "selected pytest call completed with an assertion failure"
	} else {
		return refuse("setup", "pytest call failed without a completed assertion failure")
	}
	evidence.Complete = true
	evidence.Records = []RepositoryAcceptanceRecord{selection, {Binding: in.Binding, Kind: "test", Status: "running"}, terminal}
	return evidence, nil
}

func pytestUniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate object field")
				}
				seen[name] = true
				if err := value(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
		case json.Delim('['):
			for decoder.More() {
				if err := value(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
		}
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("extra JSON value")
	}
	return nil
}
