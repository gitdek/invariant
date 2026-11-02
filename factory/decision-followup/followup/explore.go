package followup

import "strconv"

// These finite instance sizes belong to the explorer. Production receives its
// repair allowance through New; environment bounds never limit its policy.
const (
	Followups         = 2
	SpecLines         = 1
	ReadmePassages    = 1
	RepairsPerRequest = 2
	MaxRenewals       = 1
	MaxHeadMoves      = 1
	Exercises         = 4
)

const (
	oneWay = iota
	twoWay
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

// Runs and Result describe actual execution, including a result that has not
// yet been delivered. They are environment facts, not workflow observations.
type effectState struct {
	Operation
	Runs   int
	Result int
}

type tokenState struct {
	ID   int
	Kind int
	Head int
}

type eventState struct {
	Kind             string
	PreviousRepairs  int
	PreviousHold     int
	PreviousHead     int
	PreviousCI       int
	PreviousCIHead   int
	PreviousFresh    bool
	PreviousConflict bool
	PreviousEffect   effectState
	PreviousResume   bool
	Token            tokenState
}

// State has one field for every TLA+ variable, including environment facts and
// normalized transition evidence. Its arrays and value records are comparable
// so exhaustive exploration can deduplicate complete states without loss.
type State struct {
	PlanMerged    bool
	DecisionOwed  bool
	Door          int
	Executed      bool
	Phase         int
	Draft         draftState
	Followup      int
	Created       [Followups]bool
	Bundle        bundleState
	CI            int
	CIFresh       bool
	Conflict      bool
	RepairsUsed   int
	Hold          int
	Event         eventState
	Head          int
	CIHead        int
	RepairError   int
	FailureCause  Causes
	Effect        effectState
	ResumePending bool
	Renewals      int
	HeadMoves     int
	Exercise      string
}

// Init uses the same constructor as production and adds only environment data.
func Init() State {
	s := copyWorkflow(State{}, New(RepairsPerRequest))
	s.Exercise = "simple"
	s.Event = event(s, "initial")
	return s
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
		RepairLimit: RepairsPerRequest, Head: s.Head, CIHead: s.CIHead,
		RepairError: s.RepairError, FailureCause: s.FailureCause,
		Effect: s.Effect.Operation, ResumePending: s.ResumePending,
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
	s.Head = w.Head
	s.CIHead = w.CIHead
	s.RepairError = w.RepairError
	s.FailureCause = w.FailureCause
	s.Effect.Operation = w.Effect
	s.ResumePending = w.ResumePending
	return s
}

func active(e effectState) bool {
	return e.Stage == StageAuthorized || e.Stage == StagePending || e.Stage == StageLost
}

// event follows EventWithToken and PriorEffect exactly: irrelevant evidence is
// normalized, while rejection/outcome events retain all relevant prior facts.
func event(s State, kind string) eventState {
	e := eventState{Kind: kind, PreviousRepairs: s.RepairsUsed, PreviousHold: s.Hold}
	switch kind {
	case "repairPublished", "repairSuperseded", "repairFailed", "repairNoChange",
		"mergeRefusal", "externalMerge", "revision", "rejectCI", "rejectOutcome":
		e.PreviousHead = s.Head
		e.PreviousCI = s.CI
		e.PreviousCIHead = s.CIHead
		e.PreviousFresh = s.CIFresh
		e.PreviousConflict = s.Conflict
	}
	switch kind {
	case "repair", "mergeAuthorize":
		e.PreviousEffect.ID = s.Effect.ID
		if active(s.Effect) {
			e.PreviousEffect.Stage = StageAuthorized
		}
	case "resume":
		if active(s.Effect) {
			e.PreviousEffect.Stage = StageAuthorized
		}
	case "repairPublished", "repairSuperseded", "repairFailed", "repairNoChange",
		"merge", "mergeRefusal", "executeRepair", "executeMerge", "lostReply",
		"externalMerge", "rejectOutcome", "rejectCI":
		e.PreviousEffect = s.Effect
	}
	switch kind {
	case "resume", "deferResume", "externalMerge", "merge":
		e.PreviousResume = s.ResumePending
	}
	return e
}

func after(s State, w Workflow, ran bool, kind string) State {
	if !ran {
		return s
	}
	next := copyWorkflow(s, w)
	next.Event = event(s, kind)
	if kind == "repair" || kind == "mergeAuthorize" {
		next.Effect.Runs = 0
		next.Effect.Result = ResultNone
	}
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

func exerciseName(i int) string {
	names := [...]string{"repairs", "conflict", "revision", "merge"}
	if i < len(names) {
		return names[i]
	}
	return "exercise" + strconv.Itoa(i+1)
}

// predecessor keeps disabled zero-head and zero-operation attempts in Nat,
// matching the argument expressions in Next without unary-negative literals.
func predecessor(n int) int {
	if n == 0 {
		return 0
	}
	return n - 1
}

// Try emits every argument instance in Next, including disabled calls. Only
// environment occurrence restrictions are applied here; production methods
// decide policy eligibility. A disabled attempt reports the original state.
func Try(s State, tried func(step string, args []any, next State)) {
	for _, plan := range [...]struct {
		owed     bool
		door     int
		executed bool
	}{{false, oneWay, false}, {true, oneWay, true}, {true, twoWay, true}, {true, twoWay, false}} {
		next := s
		if !s.PlanMerged {
			next.PlanMerged = true
			next.DecisionOwed = plan.owed
			next.Door = plan.door
			next.Executed = plan.executed
			next.Event = event(s, "planMerge")
		}
		tried("PlanMerges", []any{plan.owed, doorName(plan.door), plan.executed}, next)
	}

	w := s.workflow()
	next, ran := w.StartDraft(s.PlanMerged, s.DecisionOwed)
	kind := "draftStart"
	if s.Phase == DraftFailed {
		kind = "draftRetry"
	}
	tried("StartDraft", []any{}, after(s, next, ran, kind))
	next, ran = w.DraftFails()
	tried("DraftFails", []any{}, after(s, next, ran, "draftFailure"))
	for i := -1; i < Exercises; i++ {
		needsOwner := i == -1
		chosenExercise := "simple"
		if !needsOwner {
			chosenExercise = exerciseName(i)
		}
		next, ran = w.DraftSucceeds(s.Door == twoWay, s.Executed, needsOwner)
		ran = ran && (needsOwner || s.Door == twoWay && s.Executed)
		result := after(s, next, ran, "draftSuccess")
		if ran {
			result.Draft.Sources = [3]bool{true, true, true}
			result.Exercise = chosenExercise
		}
		tried("DraftSucceeds", []any{needsOwner, chosenExercise}, result)
	}

	next, ran = w.StartCreation()
	kind = "createStart"
	if s.Phase == CreateFailed {
		kind = "createRetry"
	}
	tried("StartCreation", []any{}, after(s, next, ran, kind))
	next, ran = w.CreationFails()
	tried("CreationFails", []any{}, after(s, next, ran, "createFailure"))
	// CHOOSE uses the first model value, while the full domain remains available
	// for the model's duplicate-creation counterexample.
	next, ran = w.CreationSucceeds(1)
	result := after(s, next, ran, "create")
	if ran {
		result.Created[0] = true
		result.Bundle = completeBundle()
	}
	tried("CreationSucceeds", []any{modelValue("f", 1)}, result)

	remoteUnmerged := s.Effect.Result != ResultMerged
	next, ran = w.RequestCI()
	tried("RequestCI", []any{}, after(s, next, ran && remoteUnmerged, "requestCI"))
	for _, check := range []int{CIPassed, CIFailed} {
		next, ran = w.CheckCompletes(s.Head, check)
		enabledEnvironment := remoteUnmerged && (check == CIPassed || s.Exercise == "repairs" || s.Exercise == "revision")
		tried("CheckCompletes", []any{s.Head, ciName(check)}, after(s, next, ran && enabledEnvironment, "check"))
	}

	next, ran = w.ObserveRevision()
	ran = ran && s.Exercise == "revision" && s.HeadMoves < MaxHeadMoves && remoteUnmerged
	result = after(s, next, ran, "revision")
	if ran {
		result.HeadMoves++
	}
	tried("ObserveRevision", []any{}, result)
	next, ran = w.ConflictAppears()
	enabledEnvironment := s.Exercise == "conflict" && s.Head == 1 && s.CI == CIPassed && remoteUnmerged
	tried("ConflictAppears", []any{}, after(s, next, ran && enabledEnvironment, "conflict"))
	next, ran = w.Repair()
	tried("Repair", []any{}, after(s, next, ran, "repair"))
	for _, execution := range []int{ResultPublished, ResultFailed, ResultNoChange} {
		next, ran = w.ExecuteRepair(execution)
		result = after(s, next, ran, "executeRepair")
		if ran {
			result.Effect.Runs++
			result.Effect.Result = execution
		}
		tried("ExecuteRepair", []any{resultName(execution)}, result)
	}
	next, ran = w.AutoMerge()
	tried("AutoMerge", []any{}, after(s, next, ran, "mergeAuthorize"))
	for _, execution := range []int{ResultMerged, ResultRefused} {
		next, ran = w.ExecuteMerge(execution)
		ran = ran && (execution == ResultMerged || s.Exercise == "conflict" || s.Exercise == "revision" || s.Exercise == "merge")
		result = after(s, next, ran, "executeMerge")
		if ran {
			result.Effect.Runs++
			result.Effect.Result = execution
		}
		tried("ExecuteMerge", []any{resultName(execution)}, result)
	}
	next, ran = w.LoseReply()
	tried("LoseReply", []any{}, after(s, next, ran && s.Exercise != "simple", "lostReply"))

	next, ran = w.RepairOutcome(s.Effect.ID, s.Effect.Head, s.Effect.Result)
	ran = ran && remoteUnmerged
	kind = "repairSuperseded"
	switch {
	case s.Effect.Result == ResultFailed:
		kind = "repairFailed"
	case s.Effect.Result == ResultNoChange:
		kind = "repairNoChange"
	case s.Head == s.Effect.Head:
		kind = "repairPublished"
	}
	result = after(s, next, ran, kind)
	if ran {
		result.Event.Token = tokenState{s.Effect.ID, EffectRepair, s.Effect.Head}
	}
	tried("RepairOutcome", []any{s.Effect.ID, s.Effect.Head, resultName(s.Effect.Result)}, result)
	next, ran = w.MergeOutcome(s.Effect.ID, s.Effect.Head, s.Effect.Result)
	kind = "mergeRefusal"
	if s.Effect.Result == ResultMerged {
		kind = "merge"
	}
	result = after(s, next, ran, kind)
	if ran {
		result.Event.Token = tokenState{s.Effect.ID, EffectMerge, s.Effect.Head}
	}
	tried("MergeOutcome", []any{s.Effect.ID, s.Effect.Head, resultName(s.Effect.Result)}, result)

	next, ran = w.Block()
	tried("Block", []any{}, after(s, next, ran, "block"))
	next, ran = w.Close()
	tried("PersonCloses", []any{}, after(s, next, ran && s.Exercise != "simple" && remoteUnmerged, "close"))
	canRenew := s.Renewals < MaxRenewals && (s.Exercise == "repairs" || s.Exercise == "conflict" || s.Exercise == "merge")
	next, ran = w.Retry()
	ran = ran && canRenew
	result = after(s, next, ran, "retry")
	if ran {
		result.Renewals++
	}
	tried("PersonRetries", []any{}, result)
	next, ran = w.Resume()
	ran = ran && canRenew
	result = after(s, next, ran, "resume")
	if ran {
		result.Renewals++
	}
	tried("PersonResumes", []any{}, result)
	next, ran = w.DeferResume()
	tried("DeferResume", []any{}, after(s, next, ran && canRenew, "deferResume"))
	next, ran = w.ApplyDeferredResume()
	ran = ran && s.Renewals < MaxRenewals
	result = after(s, next, ran, "resume")
	if ran {
		result.Renewals++
	}
	tried("ApplyDeferredResume", []any{}, result)
	next, ran = w.ObserveExternalMerge()
	tried("ObserveExternalMerge", []any{}, after(s, next, ran && s.Exercise != "simple" && remoteUnmerged, "externalMerge"))
	for _, revision := range []int{predecessor(s.Head), s.Head} {
		next, ran = w.RejectCI(revision)
		ran = ran && s.Exercise != "simple"
		result = after(s, next, ran, "rejectCI")
		if ran {
			result.Event.Token = tokenState{Head: revision}
		}
		tried("RejectCI", []any{revision}, result)
	}
	otherKind := EffectRepair
	if s.Effect.Kind == EffectRepair {
		otherKind = EffectMerge
	}
	for _, token := range []tokenState{
		{predecessor(s.Effect.ID), s.Effect.Kind, s.Effect.Head},
		{s.Effect.ID, otherKind, s.Effect.Head},
		{s.Effect.ID, s.Effect.Kind, predecessor(s.Effect.Head)},
		{s.Effect.ID, s.Effect.Kind, s.Effect.Head},
	} {
		next, ran = w.RejectOutcome(token.ID, token.Kind, token.Head)
		ran = ran && s.Exercise != "simple"
		result = after(s, next, ran, "rejectOutcome")
		if ran {
			result.Event.Token = token
		}
		tried("RejectOutcome", []any{token.ID, effectKindName(token.Kind), token.Head}, result)
	}
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

func ciName(ci int) string {
	return [...]string{"none", "pending", "passed", "failed"}[ci]
}

func effectKindName(kind int) string {
	return [...]string{"none", "repair", "merge"}[kind]
}

func resultName(result int) string {
	return [...]string{"none", "published", "failed", "noChange", "merged", "refused"}[result]
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

func abstractCauses(c Causes) map[string]any {
	values := make([]any, 0, 3)
	if c.CI {
		values = append(values, "ci")
	}
	if c.Conflict {
		values = append(values, "conflict")
	}
	if c.Execution {
		values = append(values, "execution")
	}
	return set(values)
}

func abstractEffect(e effectState) map[string]any {
	return map[string]any{
		"id": e.ID, "kind": effectKindName(e.Kind), "head": e.Head,
		"stage": [...]string{"idle", "authorized", "pending", "lost", "settled"}[e.Stage],
		"runs":  e.Runs, "result": resultName(e.Result), "cause": abstractCauses(e.Cause),
	}
}

// Abstract preserves every modeled field. Explicit TLA+ set, function and model
// value encodings retain types even for empty collections.
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
	holds := [...]string{"none", "exhausted", "closed", "mergeRefused"}
	return map[string]any{
		"planMerged": s.PlanMerged, "decisionOwed": s.DecisionOwed,
		"door": doorName(s.Door), "executed": s.Executed,
		"phase": [...]string{"waiting", "drafting", "draftFailed", "readyToCreate",
			"creating", "createFailed", "open", "needsRepair", "blocked", "closed", "merged",
			"repairing", "merging", "mergeRefused", "externallyMerged"}[s.Phase],
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
		"ci": ciName(s.CI), "ciFresh": s.CIFresh, "conflict": s.Conflict,
		"repairsUsed": s.RepairsUsed, "hold": holds[s.Hold],
		"head": s.Head, "ciHead": s.CIHead,
		"repairError":  [...]string{"none", "failed", "noChange"}[s.RepairError],
		"failureCause": abstractCauses(s.FailureCause), "effect": abstractEffect(s.Effect),
		"resumePending": s.ResumePending, "renewals": s.Renewals,
		"headMoves": s.HeadMoves, "exercise": s.Exercise,
		"event": map[string]any{
			"kind": s.Event.Kind, "previousRepairs": s.Event.PreviousRepairs,
			"previousHold": holds[s.Event.PreviousHold], "previousHead": s.Event.PreviousHead,
			"previousCI": ciName(s.Event.PreviousCI), "previousCIHead": s.Event.PreviousCIHead,
			"previousFresh": s.Event.PreviousFresh, "previousConflict": s.Event.PreviousConflict,
			"previousEffect": abstractEffect(s.Event.PreviousEffect), "previousResume": s.Event.PreviousResume,
			"token": map[string]any{
				"id": s.Event.Token.ID, "kind": effectKindName(s.Event.Token.Kind), "head": s.Event.Token.Head,
			},
		},
	}
}
