package verify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
)

// Version 1 belongs to the gate. An execution adapter receives the complete
// path and must observe actual dispatch independently of the driver's result.
type BugReplayRequest struct {
	Version      int               `json:"version"`
	ID           string            `json:"id"`
	InvocationID string            `json:"invocation_id"`
	Bug          project.Statement `json:"bug"`
	Proposal     string            `json:"proposal"`
	Path         tlc.ReplayPath    `json:"path"`
}

type BugReplayCall struct {
	Index      int               `json:"index"`
	Action     string            `json:"action"`
	Args       []tlc.ReplayValue `json:"args"`
	Before     tlc.ReplayState   `json:"before"`
	After      tlc.ReplayState   `json:"after"`
	Dispatched bool              `json:"dispatched"`
	Outcome    string            `json:"outcome"`
	Error      string            `json:"error,omitempty"`
}

type BugReplayResult struct {
	Version      int             `json:"version"`
	RequestID    string          `json:"request_id"`
	InvocationID string          `json:"invocation_id"`
	Outcome      string          `json:"outcome"`
	Initial      tlc.ReplayState `json:"initial"`
	Calls        []BugReplayCall `json:"calls"`
}

// BugReplayObservation is factory-owned actual-call evidence. It cannot be
// taken from flags in a driver's result. This step supplies no execution
// adapter, and no Run option accepts these records as successful gate evidence.
type BugReplayObservation struct {
	RequestID    string          `json:"request_id"`
	InvocationID string          `json:"invocation_id"`
	Initial      tlc.ReplayState `json:"initial"`
	Calls        []BugReplayCall `json:"calls"`
	Started      bool            `json:"started"`
	Completed    bool            `json:"completed"`
	ExitCode     int             `json:"exit_code"`
}

type BugReplayEvidence struct {
	Required       bool                  `json:"required"`
	Status         string                `json:"status"`
	Passed         bool                  `json:"passed"`
	Request        *BugReplayRequest     `json:"request,omitempty"`
	Result         *BugReplayResult      `json:"result,omitempty"`
	Observation    *BugReplayObservation `json:"observation,omitempty"`
	Counterexample *tlc.Result           `json:"counterexample,omitempty"`
	Message        string                `json:"message,omitempty"`
}

func copyReplay[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}

func (q BugReplayRequest) identity() string {
	q.ID, q.InvocationID = "", ""
	b, _ := json.Marshal(q)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (q BugReplayRequest) validate() error {
	if q.Version != 1 || q.ID != q.identity() || q.InvocationID == "" || q.Proposal == "" ||
		q.Bug.Kind != project.Bug || q.Bug.CodeBehavior != "current" || q.Bug.Name != q.Path.Bug ||
		q.Bug.Expect != q.Path.Expect || q.Bug.SHA256 != q.Path.BugPin {
		return fmt.Errorf("replay-invalid-request: statement or identity does not match the checked path")
	}
	return q.Path.Validate()
}

func NewBugReplayRequest(bug project.Statement, proposal string, path tlc.ReplayPath) (BugReplayRequest, error) {
	if err := path.Validate(); err != nil {
		return BugReplayRequest{}, err
	}
	q := BugReplayRequest{Version: 1, Bug: bug, Proposal: proposal, Path: copyReplay(path)}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return BugReplayRequest{}, err
	}
	q.InvocationID = hex.EncodeToString(nonce[:])
	q.ID = q.identity()
	if err := q.validate(); err != nil {
		return BugReplayRequest{}, err
	}
	return q, nil
}

// CheckBugReplay evaluates supplied contract records, without attaching them
// to a report. Matching records alone do not substitute for the requested
// prefix: each setup call and the offending dispatch must actually occur.
func CheckBugReplay(ctx context.Context, runner tlc.Runner, dir string, q BugReplayRequest, result BugReplayResult, observed BugReplayObservation) (BugReplayEvidence, error) {
	// Refuse malformed values before JSON copying could replace invalid UTF-8.
	for _, record := range []BugReplayObservation{{Initial: result.Initial, Calls: result.Calls}, observed} {
		if err := replayRecordValues(record); err != nil {
			return BugReplayEvidence{Required: true, Status: "failed", Message: err.Error()}, err
		}
	}
	if err := q.validate(); err != nil {
		return BugReplayEvidence{Required: true, Status: "failed", Message: err.Error()}, err
	}
	q, result, observed = copyReplay(q), copyReplay(result), copyReplay(observed)
	e := BugReplayEvidence{Required: true, Status: "failed", Request: &q, Result: &result, Observation: &observed}
	fail := func(message string) (BugReplayEvidence, error) {
		e.Message = message
		return e, fmt.Errorf("replay-invalid-result: %s", message)
	}
	if result.Version != 1 || result.RequestID != q.ID || result.InvocationID != q.InvocationID ||
		observed.RequestID != q.ID || observed.InvocationID != q.InvocationID {
		return fail("stale request, invocation or contract version")
	}
	if !observed.Started || !observed.Completed || observed.ExitCode != 0 {
		return fail("execution did not start and finish successfully")
	}
	if !reflect.DeepEqual(result.Initial, observed.Initial) || !reflect.DeepEqual(result.Calls, observed.Calls) {
		return fail("result differs from independently observed calls")
	}
	if !reflect.DeepEqual(result.Initial, q.Path.States[0]) {
		return fail("initial state differs from the requested path")
	}
	if len(result.Calls) != q.Path.Offending+1 {
		return fail("the full prefix and offending call are required")
	}
	switch result.Outcome {
	case "complete", "refused", "reproduced", "failed":
	default:
		return fail("unknown outcome")
	}
	for i, call := range result.Calls {
		step := q.Path.Steps[i]
		if call.Index != i || call.Action != step.Action || !reflect.DeepEqual(call.Args, step.Args) ||
			!reflect.DeepEqual(call.Before, q.Path.States[step.From]) || !call.Dispatched || call.Error != "" {
			return fail(fmt.Sprintf("call %d was not the requested, actually dispatched operation", i))
		}
		if call.Outcome != "returned" && call.Outcome != "refused" {
			return fail(fmt.Sprintf("call %d did not return normally", i))
		}
		if i < q.Path.Offending && (call.Outcome != "returned" || !reflect.DeepEqual(call.After, q.Path.States[step.To])) {
			return fail(fmt.Sprintf("setup call %d diverged from the path", i))
		}
	}
	last := result.Calls[q.Path.Offending]
	forbidden := q.Path.States[q.Path.Steps[q.Path.Offending].To]
	if result.Outcome == "reproduced" || reflect.DeepEqual(last.After, forbidden) {
		e.Status, e.Message = "reproduced", "The offending operation reproduced the forbidden successor."
		return e, nil
	}
	if result.Outcome == "failed" || (result.Outcome == "refused") != (last.Outcome == "refused") {
		return fail("result outcome does not describe the offending call")
	}
	if err := runner.CheckReplayAlternative(ctx, dir, q.Path, last.Before, last.After, last.Outcome == "refused"); err != nil {
		return fail(err.Error())
	}
	e.Status, e.Passed = result.Outcome, true
	return e, nil
}

func replayRecordValues(record BugReplayObservation) error {
	states := []tlc.ReplayState{record.Initial}
	for _, call := range record.Calls {
		states = append(states, call.Before, call.After)
		for _, value := range call.Args {
			if _, err := value.TLA(); err != nil {
				return fmt.Errorf("replay-invalid-result: %w", err)
			}
		}
	}
	for _, state := range states {
		for _, value := range state {
			if _, err := value.TLA(); err != nil {
				return fmt.Errorf("replay-invalid-result: %w", err)
			}
		}
	}
	return nil
}

func driverBugReplay(p *project.Project, b project.Statement) bool {
	return RequiresBugReplay(p.Manifest.Language, b)
}

// RequiresBugReplay says whether the gate has to replay the bug a statement
// names against the code of a project in language: a current bug, where a
// driver checks the code. Go keeps its proof, agreement and exploration.
func RequiresBugReplay(language string, s project.Statement) bool {
	return s.Kind == project.Bug && s.CodeBehavior == "current" && (language == "typescript" || language == "python")
}

// BugReplayExecutes says whether a gate can carry out a replay it requires.
// It can't yet: #857 delivers the obligation, TLC's path and the contract a
// replay answers to, and the adapters that execute one follow it. Until
// they do, a required replay ends replay-unavailable and the gate doesn't
// pass. What tells a person whether a proposal can be built reads this, so
// the gate and what's said of it change together.
const BugReplayExecutes = false

func prepareBugReplay(ctx context.Context, out *Bug, b project.Statement, p *project.Project, dir string, runner tlc.Runner, cfg tlc.Config, res tlc.Result, kind string) {
	if b.CodeBehavior != "current" {
		return
	}
	retained := copyReplay(res)
	e := &BugReplayEvidence{Required: driverBugReplay(p, b), Status: "pending", Counterexample: &retained}
	out.Replay = e
	if !e.Required {
		e.Status, e.Message = "not-replayed", "Go uses its existing proof, agreement and exploration checks."
		return
	}
	path, err := runner.ExtractReplay(ctx, dir, tlc.ReplaySpec{Module: p.ModuleName(), Bug: b.Name, Expect: b.Expect, ExpectKind: kind, Config: cfg}, retained)
	if err == nil {
		var q BugReplayRequest
		q, err = NewBugReplayRequest(b, project.ProposalHash(p.Lock.Bounds, p.Lock.Statements), path)
		if err == nil {
			e.Request = &q
		}
	}
	if err != nil {
		e.Status, e.Message = "replay-unavailable", strings.ReplaceAll(err.Error(), dir, "<model>")
		if strings.Contains(err.Error(), "replay-unsupported-temporal") {
			e.Status = "replay-unsupported-temporal"
		}
	}
}

// activateBugReplays is independent of conformance options, budgets and the
// driver's exhaustive declaration. There is no production adapter yet.
func activateBugReplays(r *Report) bool {
	ok := true
	for i := range r.Bugs {
		e := r.Bugs[i].Replay
		if e == nil || !e.Required {
			continue
		}
		if e.Status == "pending" && !BugReplayExecutes {
			e.Status, e.Message = "replay-unavailable", "No execution adapter is available for the mandatory current-bug replay."
		}
		ok = false
	}
	return ok
}

type BugReplayStatus struct {
	Status   string `json:"status"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Replayed bool   `json:"replayed"`
}

func (b Bug) ReplayStatus() BugReplayStatus {
	switch b.CodeBehavior {
	case "":
		return BugReplayStatus{Status: "not-classified", Label: "not replayed: not classified"}
	case "hypothetical":
		return BugReplayStatus{Status: "hypothetical", Label: "not replayed: hypothetical"}
	}
	if b.Replay == nil {
		return BugReplayStatus{Status: "not-replayed", Label: "current; not replayed"}
	}
	e := b.Replay
	replayed := e.Passed && e.Request != nil && e.Result != nil && e.Observation != nil && (e.Status == "complete" || e.Status == "refused")
	return BugReplayStatus{Status: e.Status, Label: "current; " + e.Status, Required: e.Required, Replayed: replayed}
}

func portableBugReplay(e *BugReplayEvidence) *BugReplayEvidence {
	if e == nil {
		return nil
	}
	out := copyReplay(*e)
	if out.Request != nil {
		out.Request.InvocationID = ""
	}
	if out.Result != nil {
		out.Result.InvocationID = ""
	}
	if out.Observation != nil {
		out.Observation.InvocationID = ""
	}
	if out.Counterexample != nil {
		// Printed includes SANY's temporary extraction paths. The trace and
		// checked path carry the stable model evidence independently.
		out.Counterexample.Printed = nil
	}
	return &out
}
