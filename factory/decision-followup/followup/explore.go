package followup

import "strconv"

// These are the finite instance sizes used by the refinement explorer. The
// production workflow accepts its repair limit through New.
const (
	Followups         = 2
	SpecLines         = 1
	ReadmePassages    = 1
	RepairsPerRequest = 2
)

const (
	oneWay = iota
	twoWay
)

const (
	eventInitial = iota
	eventPlanMerge
	eventDraftStart
	eventDraftRetry
	eventDraftFailure
	eventDraftSuccess
	eventCreateStart
	eventCreateRetry
	eventCreateFailure
	eventCreate
	eventCheck
	eventConflict
	eventRepair
	eventBlock
	eventClose
	eventRetry
	eventResume
	eventMerge
)

type draftState struct {
	Status         int
	DecidedByAgent bool
	NeedsOwner     bool
	Sources        [3]bool
}

type bundleState struct {
	Command         bool
	Journal         bool
	LogRow          bool
	SpecCitations   [SpecLines]bool
	ReadmeCitations [ReadmePassages]bool
	RolledUp        bool
	Files           [3]bool
}

type eventState struct {
	Kind            int
	PreviousRepairs int
}

// State contains exactly the modeled state, including the environment facts
// and observable history that are outside the production workflow. Arrays
// make the state comparable so the explorer can exhaustively deduplicate it.
type State struct {
	PlanMerged   bool
	DecisionOwed bool
	Door         int
	Executed     bool
	Phase        int
	Draft        draftState
	Followup     int
	Created      [Followups]bool
	Bundle       bundleState
	CI           int
	CIFresh      bool
	Conflict     bool
	RepairsUsed  int
	Hold         int
	Event        eventState
}

// Init starts the finite instance with the same constructor as production.
func Init() State {
	return copyWorkflow(State{}, New(RepairsPerRequest))
}

func (s State) workflow() Workflow {
	return Workflow{
		Phase: s.Phase,
		Draft: Decision{
			Status: s.Draft.Status, DecidedByAgent: s.Draft.DecidedByAgent,
			NeedsOwner: s.Draft.NeedsOwner,
		},
		Followup: s.Followup, CI: s.CI, CIFresh: s.CIFresh,
		Conflict: s.Conflict, RepairsUsed: s.RepairsUsed, Hold: s.Hold,
		RepairLimit: RepairsPerRequest,
	}
}

func copyWorkflow(s State, w Workflow) State {
	s.Phase = w.Phase
	s.Draft.Status = w.Draft.Status
	s.Draft.DecidedByAgent = w.Draft.DecidedByAgent
	s.Draft.NeedsOwner = w.Draft.NeedsOwner
	s.Followup = w.Followup
	s.CI = w.CI
	s.CIFresh = w.CIFresh
	s.Conflict = w.Conflict
	s.RepairsUsed = w.RepairsUsed
	s.Hold = w.Hold
	return s
}

func after(s State, w Workflow, ran bool, kind int) State {
	if !ran {
		return s
	}
	next := copyWorkflow(s, w)
	next.Event = eventState{Kind: kind, PreviousRepairs: s.RepairsUsed}
	return next
}

func completeBundle() bundleState {
	bundle := bundleState{
		Command: true, Journal: true, LogRow: true, RolledUp: true,
		Files: [3]bool{true, true, true},
	}
	for i := range bundle.SpecCitations {
		bundle.SpecCitations[i] = true
	}
	for i := range bundle.ReadmeCitations {
		bundle.ReadmeCitations[i] = true
	}
	return bundle
}

// Try submits every action and every finite argument choice, even when the
// production method refuses the step. Refused calls retain the entire state.
func Try(s State, tried func(step string, args []any, next State)) {
	for _, owed := range []bool{false, true} {
		for _, doorKind := range []int{oneWay, twoWay} {
			for _, wasExecuted := range []bool{false, true} {
				next := s
				if !s.PlanMerged {
					next.PlanMerged = true
					next.DecisionOwed = owed
					next.Door = doorKind
					next.Executed = wasExecuted
					next.Event = eventState{Kind: eventPlanMerge, PreviousRepairs: s.RepairsUsed}
				}
				tried("PlanMerges", []any{owed, doorName(doorKind), wasExecuted}, next)
			}
		}
	}

	w := s.workflow()
	next, ran := w.StartDraft(s.PlanMerged, s.DecisionOwed)
	kind := eventDraftStart
	if s.Phase == DraftFailed {
		kind = eventDraftRetry
	}
	tried("StartDraft", []any{}, after(s, next, ran, kind))

	next, ran = w.DraftFails()
	tried("DraftFails", []any{}, after(s, next, ran, eventDraftFailure))
	for _, needsOwner := range []bool{false, true} {
		next, ran = w.DraftSucceeds(s.Door == twoWay, s.Executed, needsOwner)
		result := after(s, next, ran, eventDraftSuccess)
		if ran {
			result.Draft.Sources = [3]bool{true, true, true}
		}
		tried("DraftSucceeds", []any{needsOwner}, result)
	}

	next, ran = w.StartCreation()
	kind = eventCreateStart
	if s.Phase == CreateFailed {
		kind = eventCreateRetry
	}
	tried("StartCreation", []any{}, after(s, next, ran, kind))

	next, ran = w.CreationFails()
	tried("CreationFails", []any{}, after(s, next, ran, eventCreateFailure))
	for id := 1; id <= Followups; id++ {
		next, ran = w.CreationSucceeds(id)
		result := after(s, next, ran, eventCreate)
		if ran {
			result.Created[id-1] = true
			result.Bundle = completeBundle()
		}
		tried("CreationSucceeds", []any{modelValue("f", id)}, result)
	}

	for _, passed := range []bool{true, false} {
		for _, hasConflict := range []bool{false, true} {
			next, ran = w.CheckCompletes(passed, hasConflict)
			result := "failed"
			if passed {
				result = "passed"
			}
			tried("CheckCompletes", []any{result, hasConflict}, after(s, next, ran, eventCheck))
		}
	}

	next, ran = w.ConflictAppears()
	tried("ConflictAppears", []any{}, after(s, next, ran, eventConflict))
	next, ran = w.Repair()
	tried("Repair", []any{}, after(s, next, ran, eventRepair))
	next, ran = w.Block()
	tried("Block", []any{}, after(s, next, ran, eventBlock))
	next, ran = w.Close()
	tried("PersonCloses", []any{}, after(s, next, ran, eventClose))
	next, ran = w.Retry()
	tried("PersonRetries", []any{}, after(s, next, ran, eventRetry))
	next, ran = w.Resume()
	tried("PersonResumes", []any{}, after(s, next, ran, eventResume))
	next, ran = w.AutoMerge()
	tried("AutoMerge", []any{}, after(s, next, ran, eventMerge))
	tried("Done", []any{}, s)
}

func modelValue(prefix string, id int) map[string]any {
	return map[string]any{"$mv": prefix + strconv.Itoa(id)}
}

func set(values []any) map[string]any {
	return map[string]any{"$set": values}
}

func doorName(door int) string {
	return [...]string{"one-way", "two-way"}[door]
}

func decisionID(present bool) string {
	if present {
		return "newDecision"
	}
	return "none"
}

func citations(prefix string, present []bool) map[string]any {
	entries := make([]any, 0, len(present))
	for i, cited := range present {
		entries = append(entries, []any{modelValue(prefix, i+1), decisionID(cited)})
	}
	return map[string]any{"$fn": entries}
}

// Abstract preserves every modeled field, with explicit TLA+ encodings for
// sets, functions, and model values so empty collections retain their types.
func Abstract(s State) map[string]any {
	created := make([]any, 0, Followups)
	for i, exists := range s.Created {
		if exists {
			created = append(created, modelValue("f", i+1))
		}
	}
	var followup any = "none"
	if s.Followup != 0 {
		followup = modelValue("f", s.Followup)
	}
	sources := make([]any, 0, len(s.Draft.Sources))
	for i, used := range s.Draft.Sources {
		if used {
			sources = append(sources, [...]string{"plan", "mergedChange", "review"}[i])
		}
	}
	files := make([]any, 0, len(s.Bundle.Files))
	for i, changed := range s.Bundle.Files {
		if changed {
			files = append(files, [...]string{"decisions/", "SPEC.md", "README"}[i])
		}
	}
	decidedBy := "none"
	if s.Draft.DecidedByAgent {
		decidedBy = "agent"
	}
	command := "none"
	if s.Bundle.Command {
		command = "invariant decisions decide"
	}
	return map[string]any{
		"planMerged": s.PlanMerged, "decisionOwed": s.DecisionOwed,
		"door": doorName(s.Door), "executed": s.Executed,
		"phase": [...]string{"waiting", "drafting", "draftFailed", "readyToCreate",
			"creating", "createFailed", "open", "needsRepair", "blocked", "closed", "merged"}[s.Phase],
		"draft": map[string]any{
			"status":    [...]string{"none", "proposed", "decided"}[s.Draft.Status],
			"decidedBy": decidedBy, "needsOwner": s.Draft.NeedsOwner, "sources": set(sources),
		},
		"followup": followup, "created": set(created),
		"bundle": map[string]any{
			"command": command, "journal": decisionID(s.Bundle.Journal),
			"logRow":          decisionID(s.Bundle.LogRow),
			"specCitations":   citations("s", s.Bundle.SpecCitations[:]),
			"readmeCitations": citations("r", s.Bundle.ReadmeCitations[:]),
			"rolledUp":        s.Bundle.RolledUp, "files": set(files),
		},
		"ci":      [...]string{"none", "pending", "passed", "failed"}[s.CI],
		"ciFresh": s.CIFresh, "conflict": s.Conflict, "repairsUsed": s.RepairsUsed,
		"hold": [...]string{"none", "exhausted", "closed"}[s.Hold],
		"event": map[string]any{
			"kind": [...]string{"initial", "planMerge", "draftStart", "draftRetry", "draftFailure",
				"draftSuccess", "createStart", "createRetry", "createFailure", "create",
				"check", "conflict", "repair", "block", "close", "retry", "resume", "merge"}[s.Event.Kind],
			"previousRepairs": s.Event.PreviousRepairs,
		},
	}
}
