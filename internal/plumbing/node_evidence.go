package plumbing

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
)

type nodeEvent struct {
	Binding RepositoryAcceptanceBinding
	Actor   string
	Seq     int
	Event   string
	IDs     []string
	Outcome string
	Reason  string
	Code    int
	MAC     string `json:",omitempty"`
}

func nodeRefusal(binding RepositoryAcceptanceBinding, reason string) RepositoryAcceptanceEvidence {
	return RepositoryAcceptanceEvidence{ExitCode: -1, Records: []RepositoryAcceptanceRecord{{Binding: binding, Kind: "setup", Status: "failed", Reason: reason}}}
}

// The parent consumes a one-use credential before loading candidate code. Only
// its authenticated records can complete a session; an actor string or a write
// to an inherited descriptor cannot give a worker that terminal authority.
func collectNodeAcceptance(in RepositoryAcceptanceInvocation, out RepositoryProcessOutput, key []byte) (RepositoryAcceptanceEvidence, error) {
	fail := func(reason string) (RepositoryAcceptanceEvidence, error) {
		return nodeRefusal(in.Binding, reason), errors.New(reason)
	}
	if len(key) != 32 {
		return fail("Node lifecycle has no invocation authentication key")
	}
	if !out.Started || !out.Exited || !out.Drained || out.ExitCode < 0 {
		return fail("Node process did not start, exit and drain normally")
	}
	if len(out.Events) == 0 || out.Events[len(out.Events)-1] != '\n' {
		return fail("Node supplied no complete private lifecycle stream")
	}
	sequence := map[string]int{"parent": 0, "worker": 0}
	var events []nodeEvent
	var names []string
	for _, line := range bytes.Split(bytes.TrimSuffix(out.Events, []byte("\n")), []byte("\n")) {
		var event nodeEvent
		if err := dependencyJSON(line, &event); err != nil {
			return fail("ambiguous Node lifecycle JSON: " + err.Error())
		}
		d := json.NewDecoder(bytes.NewReader(line))
		d.DisallowUnknownFields()
		if err := d.Decode(&event); err != nil || d.Decode(new(any)) != io.EOF {
			return fail("malformed Node lifecycle evidence")
		}
		if event.Actor == "parent" {
			// The observer appends MAC last, over the exact preceding JSON
			// object. No reserialization, duplicate fields or alternate JSON
			// spelling may authenticate a different record.
			suffix := []byte(`,"MAC":"` + event.MAC + `"}`)
			tag, err := hex.DecodeString(event.MAC)
			if err != nil || len(tag) != sha256.Size || !bytes.HasSuffix(line, suffix) {
				return fail("Node parent record has no authentic terminal authority")
			}
			mac := hmac.New(sha256.New, key)
			_, _ = mac.Write(line[:len(line)-len(suffix)])
			_, _ = mac.Write([]byte("}"))
			if !hmac.Equal(tag, mac.Sum(nil)) {
				return fail("Node parent record authentication failed")
			}
		} else if event.MAC != "" {
			return fail("Node worker cannot claim parent authentication")
		}
		seq, ok := sequence[event.Actor]
		if !ok || event.Binding != in.Binding || event.Seq != seq+1 {
			return fail("Node evidence has a replayed, conflicting or mismatched binding")
		}
		sequence[event.Actor]++
		if event.Event == "refused" {
			return fail("Node execution refused: " + event.Reason)
		}
		parent := slices.Contains([]string{"begin", "compile", "session_end", "finish"}, event.Event)
		if parent != (event.Actor == "parent") {
			return fail("Node lifecycle event came from the wrong observer")
		}
		events = append(events, event)
		names = append(names, event.Event)
	}
	order := strings.Join(names, ",")
	runtime := "begin,collection,body_start,body_end,hooks,suite_end,session_end,finish"
	compile := "begin,compile,session_end,finish"
	if order != runtime && order != compile {
		return fail("Node needs one selected call, hooks, suite and session completion")
	}
	n := len(events)
	if events[n-1].Code != out.ExitCode || events[n-2].Code != out.ExitCode {
		return fail("Node framework and process exits conflict")
	}
	record := func(kind, status, reason string) RepositoryAcceptanceRecord {
		return RepositoryAcceptanceRecord{Binding: in.Binding, Kind: kind, Status: status, Reason: reason}
	}
	evidence := RepositoryAcceptanceEvidence{Complete: true, ExitCode: out.ExitCode, Records: []RepositoryAcceptanceRecord{record("selection", "", "exact pinned Node selection")}}
	if order == compile {
		if out.ExitCode != 1 || events[n-2].Outcome != "failed" || events[1].Outcome != "attributed" || events[1].Reason == "" || !slices.Equal(events[1].IDs, []string{in.Binding.Selector}) {
			return fail("Node compile failure has no exact proposed local API attribution")
		}
		evidence.Records = append(evidence.Records, record("compile", "failed", events[1].Reason))
		return evidence, nil
	}
	if !slices.Equal(events[1].IDs, []string{in.Binding.Selector}) || events[4].Outcome != "passed" || events[5].Outcome != "passed" {
		return fail("Node selection or hooks did not complete uniquely")
	}
	status := ""
	switch events[3].Outcome {
	case "proposed_api":
		if out.ExitCode != 1 || events[n-2].Outcome != "failed" || events[3].Reason == "" {
			return fail("Node proposed API call conflicts with completed session")
		}
		evidence.Records = append(evidence.Records, record("compile", "failed", events[3].Reason))
		return evidence, nil
	case "passed":
		if out.ExitCode == 0 && events[n-2].Outcome == "passed" {
			status = "passed"
		}
	case "assertion":
		if out.ExitCode == 1 && events[n-2].Outcome == "failed" {
			status = "failed"
		}
	}
	if status == "" {
		return fail("Node call did not complete with a consistent runtime pass or assertion")
	}
	evidence.Records = append(evidence.Records, record("test", "running", "selected Node callback called"), record("test", status, events[3].Reason))
	return evidence, nil
}
