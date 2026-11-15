package verify

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

// PythonApplicationBugReplay is a completed collector execution, before TLC
// checks its refusal or alternative. A reproduced bug can complete execution.
type PythonApplicationBugReplay struct {
	Request     BugReplayRequest      `json:"request"`
	Result      *BugReplayResult      `json:"result,omitempty"`
	Observation *BugReplayObservation `json:"observation,omitempty"`
}

type PythonApplicationBugReplayEvidence struct {
	// Bug retains the obligation's name even when no path could be extracted.
	Bug      string                         `json:"bug,omitempty"`
	Key      PythonApplicationInvocationKey `json:"key"`
	Evidence BugReplayEvidence              `json:"evidence"`
}

// RunBugReplay uses the prepared declarations, source and image, with a fresh
// authenticated invocation. It executes the path but makes no model verdict.
func (s *PythonApplicationSession) RunBugReplay(ctx context.Context, key PythonApplicationInvocationKey, request BugReplayRequest) (*PythonApplicationExecutionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closed || s.project == nil {
		return nil, fmt.Errorf("replay-invalid-session: Python application session is not open")
	}
	if key.Kind != "conformance" || !slices.ContainsFunc(s.invocations, func(v PythonApplicationInvocation) bool { return v.Key == key }) {
		return nil, fmt.Errorf("replay-invalid-invocation: select a prepared conformance invocation")
	}
	if err := request.validate(); err != nil {
		return nil, err
	}
	q := copyReplay(request)
	p := s.project
	if q.Proposal != project.ProposalHash(p.Lock.Bounds, p.Lock.Statements) || !maps.Equal(q.Path.Bounds, p.Lock.Bounds) || q.Path.Module != p.ModuleName() ||
		!slices.ContainsFunc(p.Lock.Statements, func(b project.Statement) bool { return reflect.DeepEqual(b, q.Bug) }) ||
		!slices.ContainsFunc(p.Lock.Statements, func(b project.Statement) bool { return b.Kind == project.Spec && b.Name == q.Path.Specification }) ||
		!slices.ContainsFunc(p.Lock.Statements, func(b project.Statement) bool { return b.Kind == project.Invariant && b.Name == q.Path.Expect }) {
		return nil, fmt.Errorf("replay-invalid-request: statement, proposal or bounds differ from the prepared session")
	}
	if err := s.fresh(); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(p.ModulePath())
	if err != nil {
		return nil, err
	}
	includes, err := tla.Siblings(filepath.Dir(p.ModulePath()), filepath.Base(p.ModulePath()))
	if err != nil {
		return nil, err
	}
	hashes := map[string]string{}
	for name, src := range includes {
		hashes[name] = pythonApplicationHash([]byte(src))
	}
	if q.Path.ModelSHA256 != pythonApplicationHash(body) || !maps.Equal(q.Path.IncludesSHA256, hashes) {
		return nil, fmt.Errorf("replay-invalid-request: model or includes differ from the prepared session")
	}
	q.InvocationID = rand.Text()
	return s.run(ctx, key, nil, &q)
}

// Replay rows use ordinary lifecycle bindings and a separate path checker;
// they never enter the exploration's prefix or coverage accounting.
type pythonApplicationReplay struct {
	request BugReplayRequest
	steps   pythonApplicationExplorer
	initial tlc.ReplayState
	calls   []BugReplayCall
	matched bool
	held    *PythonApplicationObservation
	entries map[string]string
}

func (x *pythonApplicationReplay) declare(line []byte) error {
	fields, err := pythonApplicationJSONFields(line, true, "type", "request_id", "invocation_id", "path_id", "steps")
	if err != nil {
		return err
	}
	for name, want := range map[string]string{"request_id": x.request.ID, "invocation_id": x.request.InvocationID, "path_id": x.request.Path.ID} {
		var got string
		if err := json.Unmarshal(fields[name], &got); err != nil || got != want {
			return fmt.Errorf("replay-invalid-stream: foreign %s", name)
		}
	}
	declaration, err := json.Marshal(map[string]any{"type": "exploration", "steps": fields["steps"]})
	if err != nil {
		return err
	}
	if err := x.steps.declare(declaration); err != nil {
		return err
	}
	x.entries = map[string]string{}
	return nil
}

func pythonApplicationReplayState(state map[string]any, module bool) (tlc.ReplayState, error) {
	out := tlc.ReplayState{}
	for name, raw := range state {
		value, err := pythonApplicationReplayValue(raw, module)
		if err != nil {
			return nil, fmt.Errorf("replay-unsupported-binding: %s: %w", name, err)
		}
		out[name] = value
	}
	return out, nil
}

// Read values directly, without a JSON/TLA string round trip that could
// change escapes or round integers. Modules use plain lists and records;
// Step arguments retain the conformance encoding's explicit tags.
func pythonApplicationReplayValue(raw any, module bool) (tlc.ReplayValue, error) {
	v := tlc.ReplayValue{}
	bad := func() (tlc.ReplayValue, error) {
		return v, fmt.Errorf("replay-unsupported-binding: invalid state or Step value")
	}
	sequence := func(items []any) error {
		for _, item := range items {
			x, err := pythonApplicationReplayValue(item, module)
			if err != nil {
				return err
			}
			v.Elements = append(v.Elements, x)
		}
		return nil
	}
	switch value := raw.(type) {
	case string:
		v.Kind, v.Atom = "string", value
	case bool:
		v.Kind, v.Atom = "bool", "FALSE"
		if value {
			v.Atom = "TRUE"
		}
	case json.Number:
		if !pythonRecordingInteger.MatchString(string(value)) {
			return bad()
		}
		v.Kind, v.Atom = "integer", string(value)
	case []any:
		if !module {
			return bad()
		}
		v.Kind = "sequence"
		if err := sequence(value); err != nil {
			return v, err
		}
	case map[string]any:
		if !module && len(value) == 1 {
			for tag, inner := range value {
				switch tag {
				case "$mv":
					atom, ok := inner.(string)
					if !ok {
						return bad()
					}
					return (tlc.ReplayValue{Kind: "model", Atom: atom}).Canonical()
				case "$seq", "$set":
					items, ok := inner.([]any)
					if !ok {
						return bad()
					}
					v.Kind = map[string]string{"$seq": "sequence", "$set": "set"}[tag]
					if err := sequence(items); err != nil {
						return v, err
					}
					return v.Canonical()
				case "$fn":
					items, ok := inner.([]any)
					if !ok {
						return bad()
					}
					v.Kind = "function"
					for _, item := range items {
						pair, ok := item.([]any)
						if !ok || len(pair) != 2 {
							return bad()
						}
						key, err := pythonApplicationReplayValue(pair[0], false)
						if err != nil {
							return v, err
						}
						x, err := pythonApplicationReplayValue(pair[1], false)
						if err != nil {
							return v, err
						}
						v.Entries = append(v.Entries, tlc.ReplayEntry{Key: key, Value: x})
					}
					return v.Canonical()
				}
			}
		}
		v.Kind = "record"
		for name, item := range value {
			x, err := pythonApplicationReplayValue(item, module)
			if err != nil {
				return v, err
			}
			v.Entries = append(v.Entries, tlc.ReplayEntry{Key: tlc.ReplayValue{Kind: "string", Atom: name}, Value: x})
		}
	default:
		return bad()
	}
	return v.Canonical()
}

func (x *pythonApplicationReplay) observe(row PythonApplicationObservation) error {
	q := x.request
	if !x.steps.declared || row.Replay != nil || len(row.Calls) > 0 {
		return fmt.Errorf("replay-invalid-stream: replay cannot supply exploration evidence")
	}
	if row.Kind == "initial" {
		if x.matched || row.Start == nil || *row.Start != row.Run-1 || row.Outcome != "" || row.Refusal != nil {
			return fmt.Errorf("replay-invalid-stream: unexpected initial state")
		}
		initial, err := pythonApplicationReplayState(row.State, row.Form == "module")
		if err != nil {
			return err
		}
		x.matched = initial.Equal(q.Path.States[0])
		if x.matched {
			x.initial = initial
		}
		return nil
	}
	i := len(x.calls)
	if !x.matched || row.Kind != "transition" || i > q.Path.Offending || row.Called == nil || row.Sequence != i+1 {
		return fmt.Errorf("replay-invalid-stream: missing or extra requested operation")
	}
	step := q.Path.Steps[i]
	if _, err := x.steps.instance(row); err != nil {
		return err
	}
	if entry := x.entries[row.Step]; entry != "" && entry != row.Entry {
		return fmt.Errorf("replay-invalid-stream: action %s changed application entry", row.Step)
	}
	x.entries[row.Step] = row.Entry
	args := make([]tlc.ReplayValue, len(row.Args))
	for j, arg := range row.Args {
		value, err := pythonApplicationReplayValue(arg, false)
		if err != nil {
			return err
		}
		args[j] = value
	}
	before, err := pythonApplicationReplayState(row.Before, row.Form == "module")
	if err != nil {
		return err
	}
	after, err := pythonApplicationReplayState(row.After, row.Form == "module")
	if err != nil {
		return err
	}
	if row.Step != step.Action || !sameReplayValues(args, step.Args) || !before.Equal(q.Path.States[step.From]) {
		return fmt.Errorf("replay-invalid-stream: call %d differs from the requested action, arguments or state", i)
	}
	declaration := x.steps.refusals[row.Step]
	if err := pythonApplicationOutcome(row, declaration); err != nil {
		return err
	}
	if x.held != nil && *row.Called {
		return fmt.Errorf("replay-invalid-stream: another selected call started while held")
	}
	completing := false
	switch row.Operation {
	case "hold":
		if *row.Called {
			x.held = &row
		} else if row.Outcome != "refused" {
			return fmt.Errorf("replay-invalid-stream: hold has no selected start or declared refusal")
		}
	case "release":
		if *row.Called || x.held == nil || row.Run != x.held.Run || row.Instance != x.held.Instance || row.Target != x.held.Target || row.SourceSHA256 != x.held.SourceSHA256 {
			return fmt.Errorf("replay-invalid-stream: release has no matching held call")
		}
		completing, x.held = true, nil
	}
	if !*row.Called && !completing && !before.Equal(after) {
		return fmt.Errorf("replay-invalid-stream: state changed without a selected call")
	}
	outcome := "returned"
	if row.Outcome == "refused" || declaration == nil && before.Equal(after) {
		outcome = "refused"
	}
	if i < q.Path.Offending && (outcome != "returned" || !after.Equal(q.Path.States[step.To])) {
		return fmt.Errorf("replay-invalid-stream: setup call %d diverged from the path", i)
	}
	x.calls = append(x.calls, BugReplayCall{Index: i, Action: row.Step, Args: args, Before: before, After: after, Dispatched: true, Outcome: outcome})
	return nil
}

func (x *pythonApplicationReplay) finish(r *PythonApplicationBugReplay) error {
	if !x.steps.declared || !x.matched || len(x.calls) != x.request.Path.Offending+1 {
		return fmt.Errorf("replay-incomplete: driver did not complete the required prefix and offending call")
	}
	outcome := "complete"
	if x.calls[len(x.calls)-1].Outcome == "refused" {
		outcome = "refused"
	}
	r.Result = &BugReplayResult{Version: 1, RequestID: x.request.ID, InvocationID: x.request.InvocationID, Outcome: outcome, Initial: x.initial, Calls: x.calls}
	r.Observation = &BugReplayObservation{RequestID: x.request.ID, InvocationID: x.request.InvocationID, Initial: copyReplay(x.initial), Calls: copyReplay(x.calls), Started: true, Completed: true}
	return nil
}

func runPythonApplicationBugReplays(ctx context.Context, s *PythonApplicationSession, r *Report, runner tlc.Runner) error {
	for i := range r.Bugs {
		bug := &r.Bugs[i]
		original := bug.Replay
		if original == nil || !original.Required {
			continue
		}
		var aggregate *BugReplayEvidence
		for _, v := range s.Invocations() {
			if v.Key.Kind != "conformance" {
				continue
			}
			e := copyReplay(*original)
			if e.Request != nil {
				x, err := s.RunBugReplay(ctx, v.Key, *e.Request)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if x != nil {
					r.Application.Executions = append(r.Application.Executions, *x)
					if x.BugReplay != nil {
						e.Request = &x.BugReplay.Request
					}
				}
				if err == nil && x != nil && x.Success && x.BugReplay != nil && x.BugReplay.Result != nil && x.BugReplay.Observation != nil {
					raw := x.BugReplay
					e, err = CheckBugReplay(ctx, runner, filepath.Dir(s.project.ModulePath()), raw.Request, *raw.Result, *raw.Observation)
					if ctx.Err() != nil {
						return ctx.Err()
					}
				} else {
					e.Status, e.Passed = "failed", false
					if err == nil {
						err = fmt.Errorf("replay-incomplete: no completed authenticated execution")
					}
				}
				if err != nil {
					e.Message = replayMessage(err, s.root)
					if x != nil && x.HarnessError != "" {
						e.Message = "replay-execution: " + x.HarnessError
					}
				}
			} else if e.Status == "pending" {
				e.Status, e.Message = "failed", "replay-unavailable: no executable current-bug path"
			}
			e.Counterexample = copyReplay(original.Counterexample)
			if !e.Passed {
				e.Message = fmt.Sprintf("%s, invocation %s: %s", bug.Name, v.Key.ID, e.Message)
			}
			r.Application.BugReplays = append(r.Application.BugReplays, PythonApplicationBugReplayEvidence{Bug: bug.Name, Key: v.Key, Evidence: e})
			if aggregate == nil || aggregate.Passed && !e.Passed {
				copy := copyReplay(e)
				aggregate = &copy
			}
		}
		if aggregate != nil {
			bug.Replay = aggregate
		} else {
			bug.Replay.Status, bug.Replay.Passed, bug.Replay.Message = "failed", false, "replay-incomplete: no selected conformance invocation"
		}
	}
	return nil
}
