package plumbing

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

type goTestLifecycle struct {
	parent                *goTestLifecycle
	name, terminal        string
	parallel, paused      bool
	children, pendingRuns int
	barrierReleased       bool
	failedChild           bool
	reason                strings.Builder
}

// CollectGoAcceptance validates observations against the host's independently
// discovered invocation. Diagnostics never supply evidence. An infrastructure
// error vetoes qualification without erasing otherwise complete evidence.
func CollectGoAcceptance(expected RepositoryAcceptanceInvocation, stream GoAcceptanceStream, streamErr error) (RepositoryAcceptanceEvidence, error) {
	evidence := RepositoryAcceptanceEvidence{ExitCode: stream.Output.ExitCode}
	b := expected.Binding
	if !nativeText(b.InvocationID) || !nativeText(b.Part) || b.Framework != "go" || !repositoryAcceptancePath(b.File) || !nativeText(b.Selector) || !nativeText(b.Package) || !executionDigest.MatchString(b.ExecutionDigest) || b.SourceDigest != dependencyHash([]byte(expected.Source)) || len(expected.Argv) == 0 || !sameGoInvocation(expected, stream.Invocation) {
		err := errors.New("Go runtime binding, source or arguments differ from the discovered invocation")
		evidence.Records = []RepositoryAcceptanceRecord{{Binding: b, Kind: "test", Status: "refused", Reason: err.Error()}}
		return evidence, errors.Join(streamErr, err)
	}
	evidence.Records = append(evidence.Records, RepositoryAcceptanceRecord{Binding: b, Kind: "selection"})
	events, err := goAcceptanceEvents(stream)
	if !stream.Output.Started {
		return evidence, errors.Join(streamErr, err, errors.New("Go runtime process did not start"))
	}
	root := strings.Split(b.Selector, "/")[0]
	tests := map[string]*goTestLifecycle{}
	var selected *goTestLifecycle
	running := -1
	for _, event := range events {
		test, eventErr := goAcceptanceEvent(tests, root, event)
		if eventErr != nil {
			err = errors.Join(err, eventErr)
			break // Preserve only the consistent, authenticated prefix.
		}
		if event.Name != b.Selector {
			continue
		}
		selected = test
		if event.Action == "start" {
			running = len(evidence.Records)
			evidence.Records = append(evidence.Records, RepositoryAcceptanceRecord{Binding: b, Kind: "test", Status: "running", Reason: "Go test " + b.Selector + " started"})
		}
		if test.terminal != "" && event.Action != "output" {
			status := map[string]string{"pass": "passed", "fail": "failed", "skip": "skipped"}[test.terminal]
			reason := "Go test " + b.Selector + " " + status
			if test.reason.Len() > 0 {
				reason += ":\n" + test.reason.String()
			}
			evidence.Records = append(evidence.Records, RepositoryAcceptanceRecord{Binding: b, Kind: "test", Status: status, Reason: reason})
		}
	}
	if selected == nil {
		err = errors.Join(err, fmt.Errorf("selected Go test %q did not start", b.Selector))
	} else if selected.terminal == "" {
		reason := "Go test " + b.Selector + " started but has no authentic terminal"
		if selected.reason.Len() > 0 {
			reason += ":\n" + selected.reason.String()
		}
		evidence.Records[running].Reason = reason
		err = errors.Join(err, errors.New(reason))
	}
	// Check each lifetime, not adjacency in the stream: parallel siblings may
	// interleave freely, but a parent cannot finish ahead of any of its children.
	for _, name := range slices.Sorted(maps.Keys(tests)) {
		if tests[name].terminal == "" {
			err = errors.Join(err, fmt.Errorf("Go test %q has an unfinished lifecycle", name))
		}
	}
	if !stream.Output.Exited || !stream.Output.Drained || stream.Output.ExitCode < 0 {
		err = errors.Join(err, errors.New("Go runtime did not exit and drain normally"))
	}
	if stream.Output.ExitCode != 0 && (selected == nil || selected.terminal != "fail") {
		err = errors.Join(err, errors.New("Go runtime exited unsuccessfully without an exact test failure"))
	}
	evidence.Complete = err == nil
	return evidence, errors.Join(streamErr, err)
}

func sameGoInvocation(a, b RepositoryAcceptanceInvocation) bool {
	return a.Binding == b.Binding && a.Source == b.Source && slices.Equal(a.Argv, b.Argv)
}

// Re-decode the private channel and compare both representations, including
// canonical framing. A truncated or changed suffix cannot erase a valid start.
func goAcceptanceEvents(stream GoAcceptanceStream) ([]GoFrameworkEvent, error) {
	events, err := decodeGoTesting(stream.Output.Events)
	if !slices.Equal(events, stream.Events) {
		err = errors.Join(err, errors.New("decoded Go events differ from the private channel"))
	}
	offset := len(goTestingProtocol)
	for i, event := range events {
		if i >= len(stream.Events) || event != stream.Events[i] {
			return events[:i], err
		}
		var frame bytes.Buffer
		for _, value := range []string{event.Name, event.Parent, event.Action, event.Text} {
			fmt.Fprintf(&frame, "%d:%s", len(value), value)
		}
		if !bytes.HasPrefix(stream.Output.Events[offset:], frame.Bytes()) {
			return events[:i], errors.Join(err, errors.New("noncanonical Go testing event framing"))
		}
		offset += frame.Len()
	}
	return events, err
}

func goAcceptanceEvent(tests map[string]*goTestLifecycle, root string, event GoFrameworkEvent) (*goTestLifecycle, error) {
	refuse := func(reason string) (*goTestLifecycle, error) {
		return nil, fmt.Errorf("Go test %q %s: %s", event.Name, event.Action, reason)
	}
	test := tests[event.Name]
	if event.Action == "start" {
		if test != nil {
			return refuse("duplicate start")
		}
		var parent *goTestLifecycle
		if event.Parent == "" {
			if event.Name != root {
				return refuse("root differs from the discovered declaration")
			}
		} else {
			parent = tests[event.Parent]
			if parent == nil || parent.terminal != "" || !strings.HasPrefix(event.Name, event.Parent+"/") {
				return refuse("child has no live actual parent")
			}
			if parent.barrierReleased {
				return refuse("child starts after parent barrier release")
			}
		}
		if event.Text != "" {
			return refuse("start contains unexpected text")
		}
		test = &goTestLifecycle{name: event.Name, parent: parent}
		tests[event.Name] = test
		if parent != nil {
			parent.children++
			parent.pendingRuns++
		}
		return test, nil
	}
	if test == nil {
		return refuse("event has no unfinished start")
	}
	parent := ""
	if test.parent != nil {
		parent = test.parent.name
	}
	if test.terminal != "" {
		// testing writes a finished subtest's late output, as through a saved
		// Output writer, to its nearest unfinished ancestor, and panics when
		// none is left, so only that output may follow a terminal.
		live := test.parent
		for live != nil && live.terminal != "" {
			live = live.parent
		}
		if event.Action != "output" || live == nil || parent != event.Parent {
			return refuse("event has no unfinished start")
		}
		for at := live; at != nil; at = at.parent {
			at.reason.WriteString(event.Text)
		}
		return test, nil
	}
	if parent != event.Parent {
		return refuse("actual parent changed")
	}
	switch event.Action {
	case "pause":
		if test.parallel || event.Text != "" {
			return refuse("duplicate or malformed pause")
		}
		// A test calls Parallel inside its function, and its function's return
		// is what releases its parallel children, so it can't pause after that.
		if test.barrierReleased {
			return refuse("pause after its parallel children were released")
		}
		test.parallel, test.paused = true, true
		if test.parent != nil {
			test.parent.pendingRuns--
		}
	case "continue":
		if !test.paused || event.Text != "" {
			return refuse("resume without a pending pause")
		}
		// Run releases its caller at the first pause or a nonparallel terminal.
		// Every sibling Run must return before the parent opens its barrier.
		if test.parent != nil {
			if test.parent.paused {
				return refuse("resume precedes parent resume")
			}
			if test.parent.pendingRuns != 0 {
				return refuse("resume precedes sibling Run return at parent barrier")
			}
			test.parent.barrierReleased = true
		}
		test.paused = false
	case "output":
	case "pass", "fail", "skip":
		if test.paused || test.children != 0 {
			return refuse("terminal precedes resume or child completion")
		}
		if test.failedChild && event.Action != "fail" {
			return refuse("terminal contradicts a failed child")
		}
		if event.Text != "" && event.Action != "fail" {
			return refuse("ordinary terminal contains unexpected text")
		}
		test.terminal = event.Action
		if test.parent != nil {
			test.parent.children--
			if !test.parallel {
				test.parent.pendingRuns--
			}
			test.parent.failedChild = test.parent.failedChild || event.Action == "fail"
		}
	default:
		return refuse("unknown lifecycle action")
	}
	// testing propagates failure and logs through actual parents. Slash-bearing
	// case names do not introduce extra parents or substitute for the selection.
	if event.Text != "" {
		for at := test; at != nil; at = at.parent {
			at.reason.WriteString(event.Text)
		}
	}
	return test, nil
}
