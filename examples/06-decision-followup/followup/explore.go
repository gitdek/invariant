package followup

import "strconv"

// These are the concrete parameter bounds used by the refinement explorer.
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
	initialEvent = iota
	planMergeEvent
	draftStartEvent
	draftRetryEvent
	draftFailureEvent
	draftSuccessEvent
	createStartEvent
	createRetryEvent
	createFailureEvent
	createEvent
	checkEvent
	conflictEvent
	repairEvent
	blockEvent
	closeEvent
	retryEvent
	resumeEvent
	mergeEvent
)

const (
	noBundleValue = iota
	completeBundleValue
	invalidBundleValue
)

// RecordedBundle retains the output produced by successful creation. Fixed
// arrays make the complete explorer state comparable.
type RecordedBundle struct {
	Command         int
	Journal         int
	LogRow          int
	SpecCitations   [SpecLines]bool
	ReadmeCitations [ReadmePassages]bool
	RolledUp        bool
	DecisionsFile   bool
	SpecFile        bool
	ReadmeFile      bool
}

type RecordedEvent struct {
	Kind            int
	PreviousRepairs int
}

// State contains exactly the model's variables. Environmental facts and
// historical observations belong here, outside the production workflow.
type State struct {
	PlanMerged   bool
	DecisionOwed bool
	Door         int
	Executed     bool
	Phase        int
	Draft        Draft
	Followup     int
	Created      [Followups]bool
	Bundle       RecordedBundle
	CI           int
	CIFresh      bool
	Conflict     bool
	RepairsUsed  int
	Hold         int
	Event        RecordedEvent
}

// Init constructs the model's single deterministic initial state through New.
func Init() State {
	return State{}.withWorkflow(New(RepairsPerRequest))
}

func (s State) workflow() Workflow {
	return Workflow{
		Phase:       s.Phase,
		Draft:       s.Draft,
		Followup:    s.Followup,
		CI:          s.CI,
		CIFresh:     s.CIFresh,
		Conflict:    s.Conflict,
		RepairsUsed: s.RepairsUsed,
		RepairLimit: RepairsPerRequest,
		Hold:        s.Hold,
	}
}

func (s State) withWorkflow(w Workflow) State {
	s.Phase = w.Phase
	s.Draft = w.Draft
	s.Followup = w.Followup
	s.CI = w.CI
	s.CIFresh = w.CIFresh
	s.Conflict = w.Conflict
	s.RepairsUsed = w.RepairsUsed
	s.Hold = w.Hold
	return s
}

func (s State) observed(w Workflow, ok bool, event int) State {
	if !ok {
		return s
	}
	next := s.withWorkflow(w)
	next.Event = RecordedEvent{Kind: event, PreviousRepairs: s.RepairsUsed}
	return next
}

// Try attempts every action and every bounded argument in every state. Only
// production operations decide whether production transitions are accepted.
func Try(s State, tried func(step string, args []any, next State)) {
	w := s.workflow()
	for _, owed := range []bool{false, true} {
		for _, planDoor := range []int{oneWay, twoWay} {
			for _, wasExecuted := range []bool{false, true} {
				next := s
				if !s.PlanMerged {
					next.PlanMerged = true
					next.DecisionOwed = owed
					next.Door = planDoor
					next.Executed = wasExecuted
					next.Event = RecordedEvent{Kind: planMergeEvent, PreviousRepairs: s.RepairsUsed}
				}
				tried("PlanMerges", []any{owed, doorName(planDoor), wasExecuted}, next)
			}
		}
	}

	next, ok := w.StartDraft(s.PlanMerged, s.DecisionOwed)
	kind := draftStartEvent
	if s.Phase == DraftFailed {
		kind = draftRetryEvent
	}
	tried("StartDraft", nil, s.observed(next, ok, kind))

	next, ok = w.DraftFails()
	tried("DraftFails", nil, s.observed(next, ok, draftFailureEvent))

	for _, needsOwner := range []bool{false, true} {
		next, ok = w.DraftSucceeds(s.Door == twoWay, s.Executed, needsOwner)
		tried("DraftSucceeds", []any{needsOwner}, s.observed(next, ok, draftSuccessEvent))
	}

	next, ok = w.StartCreation()
	kind = createStartEvent
	if s.Phase == CreateFailed {
		kind = createRetryEvent
	}
	tried("StartCreation", nil, s.observed(next, ok, kind))

	next, ok = w.CreationFails()
	tried("CreationFails", nil, s.observed(next, ok, createFailureEvent))

	for id := 1; id <= Followups; id++ {
		created, bundle, accepted := w.CreationSucceeds(id, SpecLines, ReadmePassages)
		observed := s.observed(created, accepted, createEvent)
		if accepted {
			if created.Followup >= 1 && created.Followup <= Followups {
				observed.Created[created.Followup-1] = true
			}
			observed.Bundle = recordBundle(bundle)
		}
		tried("CreationSucceeds", []any{modelValue("f", id)}, observed)
	}

	for _, passed := range []bool{true, false} {
		result := "failed"
		if passed {
			result = "passed"
		}
		for _, hasConflict := range []bool{false, true} {
			next, ok = w.CheckCompletes(passed, hasConflict)
			tried("CheckCompletes", []any{result, hasConflict}, s.observed(next, ok, checkEvent))
		}
	}

	next, ok = w.ConflictAppears()
	tried("ConflictAppears", nil, s.observed(next, ok, conflictEvent))
	next, ok = w.Repair()
	tried("Repair", nil, s.observed(next, ok, repairEvent))
	next, ok = w.Block()
	tried("Block", nil, s.observed(next, ok, blockEvent))
	next, ok = w.Close()
	tried("PersonCloses", nil, s.observed(next, ok, closeEvent))
	next, ok = w.Retry()
	tried("PersonRetries", nil, s.observed(next, ok, retryEvent))
	next, ok = w.Resume()
	tried("PersonResumes", nil, s.observed(next, ok, resumeEvent))
	next, ok = w.AutoMerge()
	tried("AutoMerge", nil, s.observed(next, ok, mergeEvent))

	// Done is the model's environmental stutter. An unavailable action is also
	// reported unchanged, so both outcomes have the same concrete observation.
	tried("Done", nil, s)
}

func recordBundle(bundle Bundle) RecordedBundle {
	recorded := RecordedBundle{
		Command:       bundleValue(bundle.Command, "invariant decisions decide"),
		Journal:       bundleValue(bundle.Journal, "newDecision"),
		LogRow:        bundleValue(bundle.LogRow, "newDecision"),
		RolledUp:      bundle.RolledUp,
		DecisionsFile: bundle.DecisionsFile,
		SpecFile:      bundle.SpecFile,
		ReadmeFile:    bundle.ReadmeFile,
	}
	for i := range recorded.SpecCitations {
		recorded.SpecCitations[i] = i < len(bundle.SpecCitations) && bundle.SpecCitations[i] == "newDecision"
	}
	for i := range recorded.ReadmeCitations {
		recorded.ReadmeCitations[i] = i < len(bundle.ReadmeCitations) && bundle.ReadmeCitations[i] == "newDecision"
	}
	return recorded
}

func bundleValue(value, complete string) int {
	switch value {
	case "none":
		return noBundleValue
	case complete:
		return completeBundleValue
	default:
		return invalidBundleValue
	}
}

func bundleName(value int, complete string) string {
	switch value {
	case noBundleValue:
		return "none"
	case completeBundleValue:
		return complete
	default:
		return "invalid"
	}
}

func doorName(door int) string {
	switch door {
	case oneWay:
		return "one-way"
	case twoWay:
		return "two-way"
	default:
		return "invalid"
	}
}

func modelValue(prefix string, id int) map[string]any {
	return map[string]any{"$mv": prefix + strconv.Itoa(id)}
}

func set(values []any) map[string]any {
	return map[string]any{"$set": values}
}

func enumName(value int, names []string) string {
	if value < 0 || value >= len(names) {
		return "invalid"
	}
	return names[value]
}

// Abstract encodes the model's variables with explicit TLC sets, functions and
// model values. Its records and scalar values use ordinary JSON-compatible Go.
func Abstract(s State) map[string]any {
	created := []any{}
	for i, exists := range s.Created {
		if exists {
			created = append(created, modelValue("f", i+1))
		}
	}
	var followup any = "none"
	if s.Followup != 0 {
		followup = modelValue("f", s.Followup)
	}
	sources := []any{}
	if s.Draft.PlanSource {
		sources = append(sources, "plan")
	}
	if s.Draft.ChangeSource {
		sources = append(sources, "mergedChange")
	}
	if s.Draft.ReviewSource {
		sources = append(sources, "review")
	}
	decidedBy := "none"
	if s.Draft.DecidedByAgent {
		decidedBy = "agent"
	}
	files := []any{}
	if s.Bundle.DecisionsFile {
		files = append(files, "decisions/")
	}
	if s.Bundle.SpecFile {
		files = append(files, "SPEC.md")
	}
	if s.Bundle.ReadmeFile {
		files = append(files, "README")
	}
	specCitations := []any{}
	for i, cited := range s.Bundle.SpecCitations {
		value := "none"
		if cited {
			value = "newDecision"
		}
		specCitations = append(specCitations, []any{modelValue("s", i+1), value})
	}
	readmeCitations := []any{}
	for i, cited := range s.Bundle.ReadmeCitations {
		value := "none"
		if cited {
			value = "newDecision"
		}
		readmeCitations = append(readmeCitations, []any{modelValue("r", i+1), value})
	}
	return map[string]any{
		"planMerged":   s.PlanMerged,
		"decisionOwed": s.DecisionOwed,
		"door":         doorName(s.Door),
		"executed":     s.Executed,
		"phase": enumName(s.Phase, []string{
			"waiting", "drafting", "draftFailed", "readyToCreate", "creating",
			"createFailed", "open", "needsRepair", "blocked", "closed", "merged",
		}),
		"draft": map[string]any{
			"status":     enumName(s.Draft.Status, []string{"none", "proposed", "decided"}),
			"decidedBy":  decidedBy,
			"needsOwner": s.Draft.NeedsOwner,
			"sources":    set(sources),
		},
		"followup": followup,
		"created":  set(created),
		"bundle": map[string]any{
			"command":         bundleName(s.Bundle.Command, "invariant decisions decide"),
			"journal":         bundleName(s.Bundle.Journal, "newDecision"),
			"logRow":          bundleName(s.Bundle.LogRow, "newDecision"),
			"specCitations":   map[string]any{"$fn": specCitations},
			"readmeCitations": map[string]any{"$fn": readmeCitations},
			"rolledUp":        s.Bundle.RolledUp,
			"files":           set(files),
		},
		"ci":          enumName(s.CI, []string{"none", "pending", "passed", "failed"}),
		"ciFresh":     s.CIFresh,
		"conflict":    s.Conflict,
		"repairsUsed": s.RepairsUsed,
		"hold":        enumName(s.Hold, []string{"none", "exhausted", "closed"}),
		"event": map[string]any{
			"kind": enumName(s.Event.Kind, []string{
				"initial", "planMerge", "draftStart", "draftRetry", "draftFailure", "draftSuccess",
				"createStart", "createRetry", "createFailure", "create", "check", "conflict",
				"repair", "block", "close", "retry", "resume", "merge",
			}),
			"previousRepairs": s.Event.PreviousRepairs,
		},
	}
}
