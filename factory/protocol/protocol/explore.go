package protocol

import "fmt"

// The model's bounds, each set of model values as its size.
const (
	Actors           = 3
	Writers          = 2
	Bots             = 1
	Questions        = 2
	Proposals        = 2
	Heads            = 2
	PullRequests     = 3
	Decisions        = 1
	BaseRefs         = 2
	TrustedBases     = 1
	WorkflowViewRows = 9
	WorkflowStepRows = 10
)

const (
	SourcePR        = 1
	FollowPR        = 2
	UnrelatedPR     = 3
	TrustedBase     = 1
	OtherBase       = 2
	WorkflowInitial = 0
)

// Values of DirectedBy: NoActor, or the actor's index plus one. alice is the
// director: invbot writes but is a bot, and mallory has no write access.
const (
	ByAlice   = 1
	ByMallory = 2
	ByInvbot  = 3
)

// Proposals, as locks.
const (
	P1 = 1
	P2 = 2
)

// Head commits.
const (
	H1 = 1
	H2 = 2
)

// State mirrors the spec's variables one to one. Open[i] is question f(i+1);
// Gate[i] is CI's result on head h(i+1).
type State struct {
	Kind         int8
	Open         [Questions]bool
	Proposal     int8
	Amends       int8
	Base         int8
	Ratified     int8
	RatifiedBase int8
	Head         int8
	Gate         [Heads]int8
	PrLock       int8
	Scope        int8
	MergedBy     int8
	MergedHead   int8
	DirectedBy   int8
	Stops        int8
	Failure      int8
	RebuiltAfter int8
	SourceActor  int8
	Source       sourceObservation
	Follow       followObservation
	Permit       workflowReceipt
	Audit        workflowAudit
}

// These compact, comparable records hold the explorer's environment history.
// Tokens and identities are expanded into the implementation's unbounded values
// at its boundary; no finite Workflow policy is part of the implementation.
type sourceHistory struct {
	Kind, Proposal, Ratified, RatifiedBase, Amends int8
	Open                                           [Questions]bool
	Head, PrLock, MergedBy, MergedHead, Merger     int8
}

type sourceObservation struct {
	Present, IsPlan, Owes     bool
	History                   sourceHistory
	PR, Decision, CandidatePR int8
	Claim                     [3]int8
}

type followObservation struct {
	PR, Head, CIHead, Gate            int8
	Decisions, Graph                  bool
	ScopeHead                         int8
	Paths                             FollowupArtifacts
	Tied                              bool
	Target, Core, MergedHead, Outcome int8
	Resumed                           bool
	PlanRatification, ProjectLock     int8
}

type workflowContext struct {
	Authority                       [3]int8
	SourcePR, PR, Head, Core, Actor int8
	Authenticated                   bool
	CIHead, Gate                    int8
	Decisions, Graph                bool
	ScopeHead                       int8
	Paths                           FollowupArtifacts
	Tied                            bool
	Target                          int8
}

type workflowReceipt struct {
	Present    bool
	Transition int8
	Context    workflowContext
}

type workflowAudit struct {
	Present                                                      bool
	Receipt                                                      workflowReceipt
	Request                                                      workflowContext
	BeforePR, AfterPR, BeforeCore, AfterCore, Operation, Outcome int8
}

type workflowTransition struct {
	Before, Operation, Outcome, After int8
}

// This fixture is exactly WorkflowViewRows and WorkflowStepRows in the pinned
// checking bounds. Receiving a row models external acceptance for its complete
// request context; the table is never consulted by the production operations.
var workflowViews = [...]WorkflowView{
	{Token: 0, Phase: CorePhaseInitial, Allowance: 1},
	{Token: 1, Phase: CorePhaseOpen, Allowance: 1},
	{Token: 2, Phase: CorePhaseOpen, Allowance: 1, Repairs: 1},
	{Token: 3, Phase: CorePhaseOpen, Allowance: 1, Repairs: 2},
	{Token: 4, Phase: CorePhaseBlocked, Allowance: 1, Repairs: 2},
	{Token: 5, Phase: CorePhaseClosed, Hold: true, Allowance: 1},
	{Token: 6, Phase: CorePhaseOpen, Allowance: 2},
	{Token: 7, Phase: CorePhaseMerged, Allowance: 1},
	{Token: 8, Phase: CorePhaseMerged, Allowance: 2},
}

const (
	fixtureAccepted = 3
	fixtureObserved = 4
)

var workflowSteps = [...]workflowTransition{
	{0, CoreOpen, fixtureAccepted, 1},
	{1, CoreMerge, OutcomeOK, 7},
	{1, CoreClose, fixtureObserved, 5},
	{5, CoreResume, fixtureAccepted, 1},
	{1, CoreRepair, fixtureObserved, 2},
	{2, CoreRepair, fixtureObserved, 3},
	{3, CoreClassify, fixtureObserved, 4},
	{4, CoreRetry, fixtureAccepted, 6},
	{6, CoreMerge, OutcomeOK, 8},
	{1, CoreEffect, OutcomeFailed, 1},
}

var (
	kindNames       = []string{"none", "asked", "proposed", "stuck", "unsupported", "ratified", "pr_open", "failed", "merged", "closed"}
	gateNames       = []string{"none", "pending", "pass", "fail"}
	scopeNames      = []string{"one", "many"}
	mergedByNames   = []string{"nobody", "factory", "other"}
	failureNames    = []string{"none", "stopped", "limit", "gate", "ci", "unmergeable"}
	coreNames       = []string{"c0", "c1", "c2", "c3", "cb", "cc", "cr", "cm", "crm"}
	operationNames  = []string{"open", "classify", "repair", "effect", "close", "retry", "resume", "merge"}
	outcomeNames    = []string{"none", "succeeded", "failed", "accepted", "observed"}
	followGateNames = []string{"none", "pass", "fail", "skip"}
	prNames         = []string{"NoPR", "sourcePR", "followPR", "unrelatedPR"}
	baseNames       = []string{"", "trustedBase", "otherBase"}
)

// actor is the a-th of the model's actors. The first Writers-Bots are
// directors, and the last Bots are bots with write access.
func actor(a int) Actor {
	bot := a >= Actors-Bots
	return Actor{ID: a + 1, Writer: a < Writers-Bots || bot, Bot: bot}
}

func actorName(a int) string {
	switch a {
	case 0:
		return "alice"
	case 1:
		return "mallory"
	case 2:
		return "invbot"
	}
	return fmt.Sprintf("a%d", a+1)
}

// Encoding caches are used by the sequential explorer only. Once published,
// their maps and every nested value are immutable, like cached action arguments.
// The keys are complete value records, so later states cannot change a snapshot.
var modelValues = map[string]map[string]any{}
var sourceValues = map[sourceObservation]map[string]any{}
var followValues = map[followObservation]map[string]any{}
var receiptValues = map[workflowReceipt]map[string]any{}
var auditValues = map[workflowAudit]map[string]any{}
var contextValues = map[workflowContext]map[string]any{}

func cachedValue[K comparable](cache map[K]map[string]any, key K, encode func(K) map[string]any) map[string]any {
	if value, ok := cache[key]; ok {
		return value
	}
	value := encode(key)
	cache[key] = value
	return value
}

func mv(name string) map[string]any {
	return cachedValue(modelValues, name, func(name string) map[string]any {
		return map[string]any{"$mv": name}
	})
}

func lockValue(l int) map[string]any {
	if l == NoP {
		return mv("NoP")
	}
	return mv(fmt.Sprintf("p%d", l))
}

func headValue(h int) map[string]any {
	if h == NoHead {
		return mv("NoHead")
	}
	return mv(fmt.Sprintf("h%d", h))
}

func directedValue(d int) map[string]any {
	if d == NoActor {
		return mv("NoActor")
	}
	return mv(actorName(d - 1))
}

func openSet(open [Questions]bool) map[string]any {
	set := []any{}
	for q := 0; q < Questions; q++ {
		if open[q] {
			set = append(set, mv(fmt.Sprintf("f%d", q+1)))
		}
	}
	return map[string]any{"$set": set}
}

// draftChoice is one of the model's Drafts.
type draftChoice struct {
	kind int
	open [Questions]bool
	p    int
}

// drafts lists every draft the factory can post.
func drafts() []draftChoice {
	var out []draftChoice
	for m := 1; m < 1<<Questions; m++ {
		c := draftChoice{kind: KindAsked, p: NoP}
		for q := 0; q < Questions; q++ {
			c.open[q] = m&(1<<q) != 0
		}
		out = append(out, c)
	}
	for p := 1; p <= Proposals; p++ {
		out = append(out, draftChoice{kind: KindProposed, p: p})
	}
	out = append(out, draftChoice{kind: KindStuck, p: NoP}, draftChoice{kind: KindUnsupported, p: NoP})
	return out
}

// draft is c as the code takes it, with questions of its own.
func (c draftChoice) draft() Draft {
	d := Draft{Kind: c.kind, Open: make([]bool, Questions), P: c.p}
	for q := 0; q < Questions; q++ {
		d.Open[q] = c.open[q]
		if c.open[q] {
			d.NOpen++
		}
	}
	return d
}

func (c draftChoice) value() map[string]any {
	return map[string]any{"kind": kindNames[c.kind], "open": openSet(c.open), "p": lockValue(c.p)}
}

// toIssue makes the code's value from s.
func toIssue(s State) *Issue {
	i := NewIssue(Questions)
	for q := 0; q < Questions; q++ {
		i.Open[q] = s.Open[q]
		if s.Open[q] {
			i.NOpen++
		}
	}
	i.Kind = int(s.Kind)
	i.Proposal = int(s.Proposal)
	i.Amends = int(s.Amends)
	i.Base = int(s.Base)
	i.Ratified = int(s.Ratified)
	i.RatifiedBase = int(s.RatifiedBase)
	i.Head = int(s.Head)
	if s.Head != NoHead {
		i.HeadGate = int(s.Gate[s.Head-1])
	}
	i.PrLock = int(s.PrLock)
	i.Scope = int(s.Scope)
	i.MergedBy = int(s.MergedBy)
	i.MergedHead = int(s.MergedHead)
	i.DirectedBy = int(s.DirectedBy)
	i.Stops = int(s.Stops)
	i.Failure = int(s.Failure)
	i.RebuiltAfter = int(s.RebuiltAfter)
	i.FollowupPR = int(s.Follow.PR)
	return i
}

// readBack is s after the code's step to i. CI's results on heads other than
// the current one are the explorer's own: a pull request forgotten clears
// them all.
func readBack(s State, i *Issue) State {
	t := s
	t.Kind = int8(i.Kind)
	for q := 0; q < Questions; q++ {
		t.Open[q] = i.Open[q]
	}
	t.Proposal = int8(i.Proposal)
	t.Amends = int8(i.Amends)
	t.Base = int8(i.Base)
	t.Ratified = int8(i.Ratified)
	t.RatifiedBase = int8(i.RatifiedBase)
	t.Head = int8(i.Head)
	if i.Head == NoHead {
		t.Gate = [Heads]int8{}
	} else {
		t.Gate[i.Head-1] = int8(i.HeadGate)
	}
	t.PrLock = int8(i.PrLock)
	t.Scope = int8(i.Scope)
	t.MergedBy = int8(i.MergedBy)
	t.MergedHead = int8(i.MergedHead)
	t.DirectedBy = int8(i.DirectedBy)
	t.Stops = int8(i.Stops)
	t.Failure = int8(i.Failure)
	t.RebuiltAfter = int8(i.RebuiltAfter)
	return t
}

// Init is the model's initial state.
func Init() State {
	return readBack(State{Follow: emptyFollow()}, NewIssue(Questions))
}

func emptyFollow() followObservation {
	return fromFollowup(NewFollowup(workflowViews[WorkflowInitial], TrustedBase), false)
}

func toFollowup(f followObservation) Followup {
	return Followup{PR: int(f.PR), Head: int(f.Head), CIHead: int(f.CIHead), Gate: int(f.Gate),
		Decisions: f.Decisions, Graph: f.Graph, ScopeHead: int(f.ScopeHead), Paths: f.Paths,
		Tied: f.Tied, Target: int(f.Target), Core: workflowViews[f.Core], MergedHead: int(f.MergedHead),
		Outcome: int(f.Outcome), PlanRatification: int(f.PlanRatification), ProjectLock: int(f.ProjectLock)}
}

func fromFollowup(f Followup, resumed bool) followObservation {
	return followObservation{PR: int8(f.PR), Head: int8(f.Head), CIHead: int8(f.CIHead), Gate: int8(f.Gate),
		Decisions: f.Decisions, Graph: f.Graph, ScopeHead: int8(f.ScopeHead), Paths: f.Paths,
		Tied: f.Tied, Target: int8(f.Target), Core: int8(f.Core.Token), MergedHead: int8(f.MergedHead),
		Outcome: int8(f.Outcome), Resumed: resumed, PlanRatification: int8(f.PlanRatification), ProjectLock: int8(f.ProjectLock)}
}

func sourceHistoryOf(s State) sourceHistory {
	return sourceHistory{Kind: s.Kind, Proposal: s.Proposal, Ratified: s.Ratified,
		RatifiedBase: s.RatifiedBase, Amends: s.Amends, Open: s.Open, Head: s.Head,
		PrLock: s.PrLock, MergedBy: s.MergedBy, MergedHead: s.MergedHead, Merger: s.SourceActor}
}

func toSource(s sourceObservation) FollowupSource {
	return FollowupSource{Present: s.Present, IsPlan: s.IsPlan, Owes: s.Owes,
		Kind: int(s.History.Kind), Proposal: int(s.History.Proposal), Ratified: int(s.History.Ratified),
		PrLock: int(s.History.PrLock), Head: int(s.History.Head), MergedHead: int(s.History.MergedHead),
		PR: int(s.PR), Decision: int(s.Decision), CandidatePR: int(s.CandidatePR), Claim: toAuthority(s.Claim)}
}

func toAuthority(a [3]int8) Authority {
	return Authority{Plan: int(a[0]), SourcePR: int(a[1]), Decision: int(a[2])}
}

func contextFor(s State, a int, authenticated bool) workflowContext {
	f := s.Follow
	return workflowContext{Authority: s.Source.Claim, SourcePR: s.Source.PR, PR: s.Source.CandidatePR,
		Head: f.Head, Core: f.Core, Actor: int8(a), Authenticated: authenticated,
		CIHead: f.CIHead, Gate: f.Gate, Decisions: f.Decisions, Graph: f.Graph,
		ScopeHead: f.ScopeHead, Paths: f.Paths, Tied: f.Tied, Target: f.Target}
}

func toContext(c workflowContext) FollowupContext {
	return FollowupContext{Authority: toAuthority(c.Authority), SourcePR: int(c.SourcePR), PR: int(c.PR),
		Head: int(c.Head), Core: workflowViews[c.Core], Actor: int(c.Actor), Authenticated: c.Authenticated,
		CIHead: int(c.CIHead), Gate: int(c.Gate), Decisions: c.Decisions, Graph: c.Graph,
		ScopeHead: int(c.ScopeHead), Paths: c.Paths, Tied: c.Tied, Target: int(c.Target)}
}

func toReceipt(r workflowReceipt) WorkflowReceipt {
	if !r.Present {
		return WorkflowReceipt{}
	}
	t := workflowSteps[r.Transition]
	return WorkflowReceipt{Present: true, Accepted: true, Before: workflowViews[t.Before],
		Operation: int(t.Operation), Outcome: int(t.Outcome), After: workflowViews[t.After], Context: toContext(r.Context)}
}

func clearFollowup(s State) State {
	s.Source = sourceObservation{}
	s.Follow = emptyFollow()
	s.Permit = workflowReceipt{}
	s.Audit = workflowAudit{}
	return s
}

type ordinaryChoice struct {
	step   string
	args   []any
	op     func(*Issue) bool
	merger int8
}

// Immutable argument encodings are shared across states. Every operation is
// still called for every bounded choice, including choices that it refuses.
var ordinaryChoices = makeOrdinaryChoices()

func makeOrdinaryChoices() []ordinaryChoice {
	var choices []ordinaryChoice
	do := func(step string, args []any, op func(*Issue) bool) {
		choices = append(choices, ordinaryChoice{step: step, args: args, op: op})
	}
	ds := drafts()
	for a := 0; a < Actors; a++ {
		act, av := actor(a), mv(actorName(a))
		for _, c := range ds {
			d := c.draft()
			do("Solve", []any{av, c.value()}, func(i *Issue) bool { return i.Solve(act, d) })
			do("Revise", []any{av, c.value()}, func(i *Issue) bool { return i.Revise(act, d) })
		}
		do("Retry", []any{av}, func(i *Issue) bool { return i.Retry(act) })
		do("Rebuild", []any{av}, func(i *Issue) bool { return i.Rebuild(act) })
		for q := 0; q < Questions; q++ {
			qv := mv(fmt.Sprintf("f%d", q+1))
			for _, c := range ds {
				d := c.draft()
				do("Choose", []any{av, qv, c.value()}, func(i *Issue) bool { return i.Choose(act, q, d) })
			}
		}
		for p := 1; p <= Proposals; p++ {
			do("Ratify", []any{av, lockValue(p)}, func(i *Issue) bool { return i.Ratify(act, p) })
		}
	}
	for h := 1; h <= Heads; h++ {
		do("Build", []any{headValue(h)}, func(i *Issue) bool { return i.Build(h) })
		do("BuildFailsGate", []any{headValue(h)}, func(i *Issue) bool { return i.BuildFailsGate(h) })
	}
	do("StopBuild", []any{}, (*Issue).StopBuild)
	do("RefuseBuild", []any{}, (*Issue).RefuseBuild)
	for h := 1; h <= Heads; h++ {
		for l := 0; l <= Proposals; l++ {
			for sc := ScopeOne; sc <= ScopeMany; sc++ {
				do("Push", []any{headValue(h), lockValue(l), scopeNames[sc]}, func(i *Issue) bool { return i.Push(h, l, sc) })
			}
		}
	}
	for _, r := range []int{GatePass, GateFail} {
		do("CIGate", []any{gateNames[r]}, func(i *Issue) bool { return i.CIGate(r) })
	}
	do("NoticeFail", []any{}, (*Issue).NoticeFail)
	do("NoticeUnmergeable", []any{}, (*Issue).NoticeUnmergeable)
	do("Merge", []any{}, (*Issue).Merge)
	for a := 0; a < Actors; a++ {
		choices = append(choices, ordinaryChoice{step: "OthersMergeAs", args: []any{mv(actorName(a))},
			op: (*Issue).OthersMerge, merger: int8(a + 1)})
	}
	do("OthersClose", []any{}, (*Issue).OthersClose)
	for l := 0; l <= Proposals; l++ {
		do("BaseMoves", []any{lockValue(l)}, func(i *Issue) bool { return i.BaseMoves(l) })
	}
	do("Finished", []any{}, (*Issue).Finished)
	return choices
}

// attempt tries every step Next names in s, with every argument, and says
// whether the code ran it.
func attempt(s State, tried func(step string, args []any, next State, ran bool)) {
	for _, c := range ordinaryChoices {
		i := toIssue(s)
		if !c.op(i) {
			tried(c.step, c.args, s, false)
			continue
		}
		next := clearFollowup(readBack(s, i))
		if c.merger != NoActor {
			next.SourceActor = c.merger
		}
		// Finished says UNCHANGED vars, so the ordinary wrapper's reset
		// cannot also discard a pending observation or receipt.
		if c.step == "Finished" && next != s {
			tried(c.step, c.args, s, false)
			continue
		}
		tried(c.step, c.args, next, true)
	}
	attemptFollowup(s, tried)
}

// observeSource is the bounded environment observation, not authorization.
// Ineligible and forged observations are deliberately delivered to the code.
func observeSource(s State, mode string) (State, bool) {
	if s.Source.Present || (s.Kind != KindPROpen && s.Kind != KindMerged) ||
		s.Proposal != P1 || s.Ratified != P1 || s.Amends != NoP || s.RatifiedBase != NoP ||
		s.Head != H1 || s.PrLock != s.Ratified || s.Scope != ScopeOne || s.Stops != 0 ||
		s.Failure != FailNone || s.RebuiltAfter != FailNone || s.Gate != [Heads]int8{GatePending, GateNone} {
		return s, false
	}
	if s.Kind == KindMerged && (s.MergedBy != ByOther || s.SourceActor == NoActor || !actor(int(s.SourceActor)-1).Director() || s.Base != s.Ratified) {
		return s, false
	}
	if s.Kind == KindPROpen && (s.SourceActor != NoActor || s.Base != NoP) {
		return s, false
	}
	t := s
	t.Source = sourceObservation{Present: true, History: sourceHistoryOf(s), IsPlan: mode != "not-plan",
		Owes: mode != "non-owing", PR: SourcePR, Decision: 1, CandidatePR: FollowPR,
		Claim: [3]int8{s.Ratified, SourcePR, 1}}
	if mode == "wrong-plan" {
		t.Source.Claim[0] = P2
	}
	if mode == "forged-pr" {
		t.Source.CandidatePR = UnrelatedPR
	}
	t.Follow = emptyFollow()
	t.Follow.Head = H1
	t.Follow.Paths = FollowupArtifacts{Decisions: true, Spec: true, Readme: true}
	t.Follow.Tied = true
	return t, true
}

type textChoice struct {
	text string
	args []any
}

func textChoices(values ...string) []textChoice {
	var choices []textChoice
	for _, value := range values {
		choices = append(choices, textChoice{text: value, args: []any{value}})
	}
	return choices
}

var sourceModes = textChoices("eligible", "non-owing", "not-plan", "forged-pr", "wrong-plan")
var pushKinds = textChoices("narrow", "broad", "unrelated", "untrusted")

type ciChoice struct {
	gate             int
	decisions, graph bool
	args             []any
}

var ciChoices = makeCIChoices()

func makeCIChoices() []ciChoice {
	choices := []ciChoice{{gate: FollowGatePass, decisions: true, graph: true},
		{gate: FollowGateSkip, decisions: true, graph: true}, {gate: FollowGateFail, decisions: true, graph: true},
		{gate: FollowGatePass, decisions: false, graph: true}, {gate: FollowGateSkip, decisions: true, graph: false}}
	for index := range choices {
		c := &choices[index]
		c.args = []any{tuple(followGateNames[c.gate], c.decisions, c.graph)}
	}
	return choices
}

type receiptChoice struct {
	transition    int8
	actor         int
	authenticated bool
	args          []any
}

var receiptChoices = makeReceiptChoices()

func makeReceiptChoices() []receiptChoice {
	var choices []receiptChoice
	for index, transition := range workflowSteps {
		for a := 0; a <= Actors; a++ {
			for _, authenticated := range []bool{false, true} {
				choices = append(choices, receiptChoice{transition: int8(index), actor: a, authenticated: authenticated,
					args: []any{transitionValue(transition), directedValue(a), authenticated}})
			}
		}
	}
	return choices
}

func attemptFollowup(s State, tried func(step string, args []any, next State, ran bool)) {
	for _, mode := range sourceModes {
		next, ran := observeSource(s, mode.text)
		tried("ObserveSource", mode.args, next, ran)
	}

	// These operations run even when refused; only evidence delivery and its
	// bounded choices are modeled by the environment.
	do := func(step string, args []any, next Followup, ran bool) {
		if !ran {
			tried(step, args, s, false)
			return
		}
		t := s
		t.Follow = fromFollowup(next, s.Follow.Resumed)
		t.Audit = workflowAudit{}
		tried(step, args, t, true)
	}
	f := toFollowup(s.Follow)
	for _, result := range ciChoices {
		next, ran := ObserveFollowupCI(f, s.Source.Present, result.gate, result.decisions, result.graph)
		do("ObserveFollowupCI", result.args, next, ran)
	}
	next, ran := ObserveFollowupScope(f, s.Source.Present)
	do("ObserveFollowupScope", []any{}, next, ran)
	for _, kind := range pushKinds {
		paths := FollowupArtifacts{Decisions: true, Spec: true, Readme: true, ProjectCode: kind.text == "broad"}
		target := TrustedBase
		if kind.text == "untrusted" {
			target = OtherBase
		}
		next, ran := PushFollowup(f, s.Source.Present, H2, paths, kind.text != "unrelated", target)
		do("PushFollowup", kind.args, next, ran)
	}

	for _, c := range receiptChoices {
		transition := workflowSteps[c.transition]
		t := s
		ran := s.Source.Present && !s.Permit.Present && transition.Before == s.Follow.Core
		if transition.Operation == CoreRetry || transition.Operation == CoreResume {
			ran = ran && c.actor != NoActor
		} else {
			ran = ran && c.actor == NoActor && !c.authenticated
		}
		if ran {
			t.Permit = workflowReceipt{Present: true, Transition: c.transition, Context: contextFor(s, c.actor, c.authenticated)}
			t.Audit = workflowAudit{}
		}
		tried("ReceiveWorkflowStep", c.args, t, ran)
	}
	t := s
	ran = s.Source.Present && s.Permit.Present
	if ran {
		t.Permit = workflowReceipt{}
		t.Audit = workflowAudit{}
	}
	tried("DiscardWorkflowReceipt", []any{}, t, ran)

	a := Actor{}
	if s.Permit.Context.Actor != NoActor {
		a = actor(int(s.Permit.Context.Actor) - 1)
	}
	next, ran = ApplyFollowupStep(f, toSource(s.Source), toReceipt(s.Permit), a,
		s.Permit.Context.Authenticated, SourcePR, FollowPR, f.Target == TrustedBase, !s.Audit.Present)
	t = s
	if ran {
		transition := workflowSteps[s.Permit.Transition]
		resumed := s.Follow.Resumed || (transition.Operation == CoreResume && next.Core.Phase == CorePhaseOpen)
		t.Follow = fromFollowup(next, resumed)
		t.Audit = workflowAudit{Present: true, Receipt: s.Permit,
			Request:  contextFor(s, int(s.Permit.Context.Actor), s.Permit.Context.Authenticated),
			BeforePR: s.Follow.PR, AfterPR: t.Follow.PR, BeforeCore: s.Follow.Core, AfterCore: t.Follow.Core,
			Operation: transition.Operation, Outcome: transition.Outcome}
	}
	tried("ApplyFollowupStep", []any{}, t, ran)
	tried("Wait", []any{}, s, true)
}

// Try invokes every bounded leaf operation, then reports the grouped actions
// named directly by Next. OrdinaryStep and FollowupStep are nondeterministic:
// every distinct successful outcome is reported, including successful selfloops.
// A refused internal branch does not refuse its group when another branch ran;
// an unchanged refusal is reported only when the entire group refused.
func Try(s State, tried func(step string, args []any, next State)) {
	var ordinary, followup []State
	attempt(s, func(step string, args []any, next State, ran bool) {
		if step == "ObserveSource" || step == "Wait" {
			tried(step, args, next)
			return
		}
		if !ran {
			return
		}
		group := &ordinary
		switch step {
		case "ObserveFollowupCI", "ObserveFollowupScope", "PushFollowup", "ReceiveWorkflowStep", "DiscardWorkflowReceipt", "ApplyFollowupStep":
			group = &followup
		}
		for _, previous := range *group {
			if previous == next {
				return
			}
		}
		*group = append(*group, next)
	})
	for _, group := range []struct {
		step string
		next []State
	}{{"OrdinaryStep", ordinary}, {"FollowupStep", followup}} {
		if len(group.next) == 0 {
			tried(group.step, []any{}, s)
			continue
		}
		for _, next := range group.next {
			tried(group.step, []any{}, next)
		}
	}
}

// Successors returns every state one step of Next reaches from s.
func Successors(s State) []State {
	var out []State
	attempt(s, func(_ string, _ []any, next State, ran bool) {
		if ran {
			out = append(out, next)
		}
	})
	return out
}

func artifactsValue(paths FollowupArtifacts) map[string]any {
	set := []any{}
	if paths.Decisions {
		set = append(set, "decisions/")
	}
	if paths.Spec {
		set = append(set, "SPEC.md")
	}
	if paths.Readme {
		set = append(set, "README.md")
	}
	if paths.ProjectCode {
		set = append(set, "project-code")
	}
	return map[string]any{"$set": set}
}

func tuple(values ...any) map[string]any {
	return map[string]any{"$seq": values}
}

func authorityValue(a [3]int8) map[string]any {
	return tuple(lockValue(int(a[0])), mv(prNames[a[1]]), mv(fmt.Sprintf("d%d", a[2])))
}

func transitionValue(t workflowTransition) map[string]any {
	return tuple(mv(coreNames[t.Before]), operationNames[t.Operation], outcomeNames[t.Outcome], mv(coreNames[t.After]))
}

func contextValue(c workflowContext) map[string]any {
	return cachedValue(contextValues, c, encodeContext)
}

func encodeContext(c workflowContext) map[string]any {
	return map[string]any{
		"authority": authorityValue(c.Authority), "sourcePR": mv(prNames[c.SourcePR]),
		"pr": mv(prNames[c.PR]), "head": headValue(int(c.Head)), "core": mv(coreNames[c.Core]),
		"actor": directedValue(int(c.Actor)), "authenticated": c.Authenticated,
		"ci":    tuple(headValue(int(c.CIHead)), followGateNames[c.Gate], c.Decisions, c.Graph),
		"scope": tuple(headValue(int(c.ScopeHead)), artifactsValue(c.Paths), c.Tied), "target": mv(baseNames[c.Target]),
	}
}

func receiptValue(r workflowReceipt) map[string]any {
	return cachedValue(receiptValues, r, encodeReceipt)
}

func encodeReceipt(r workflowReceipt) map[string]any {
	if !r.Present {
		return mv("NoPermit")
	}
	return map[string]any{"transition": transitionValue(workflowSteps[r.Transition]), "context": contextValue(r.Context)}
}

func auditValue(a workflowAudit) map[string]any {
	return cachedValue(auditValues, a, encodeAudit)
}

func encodeAudit(a workflowAudit) map[string]any {
	if !a.Present {
		return mv("NoAudit")
	}
	return map[string]any{"receipt": receiptValue(a.Receipt), "request": contextValue(a.Request),
		"beforePR": mv(prNames[a.BeforePR]), "afterPR": mv(prNames[a.AfterPR]),
		"beforeCore": mv(coreNames[a.BeforeCore]), "afterCore": mv(coreNames[a.AfterCore]),
		"operation": operationNames[a.Operation], "outcome": outcomeNames[a.Outcome]}
}

func sourceValue(s sourceObservation) map[string]any {
	return cachedValue(sourceValues, s, encodeSource)
}

func encodeSource(s sourceObservation) map[string]any {
	if !s.Present {
		return mv("NoSource")
	}
	h := s.History
	return map[string]any{
		"history": map[string]any{"state": kindNames[h.Kind], "proposal": lockValue(int(h.Proposal)),
			"ratified": lockValue(int(h.Ratified)), "ratifiedBase": lockValue(int(h.RatifiedBase)),
			"amends": lockValue(int(h.Amends)), "open": openSet(h.Open), "head": headValue(int(h.Head)),
			"prLock": lockValue(int(h.PrLock)), "mergedBy": mergedByNames[h.MergedBy],
			"mergedHead": headValue(int(h.MergedHead)), "merger": directedValue(int(h.Merger))},
		"isPlan": s.IsPlan, "owes": s.Owes, "pr": mv(prNames[s.PR]), "decision": mv(fmt.Sprintf("d%d", s.Decision)),
		"claim": authorityValue(s.Claim), "candidatePR": mv(prNames[s.CandidatePR]),
	}
}

func followValue(f followObservation) map[string]any {
	return cachedValue(followValues, f, encodeFollow)
}

func encodeFollow(f followObservation) map[string]any {
	return map[string]any{"pr": mv(prNames[f.PR]), "head": headValue(int(f.Head)),
		"ciHead": headValue(int(f.CIHead)), "gate": followGateNames[f.Gate], "decisions": f.Decisions, "graph": f.Graph,
		"scopeHead": headValue(int(f.ScopeHead)), "paths": artifactsValue(f.Paths), "tied": f.Tied,
		"target": mv(baseNames[f.Target]), "core": mv(coreNames[f.Core]), "mergedHead": headValue(int(f.MergedHead)),
		"outcome": outcomeNames[f.Outcome], "resumed": f.Resumed,
		"planRatification": lockValue(int(f.PlanRatification)), "projectLock": lockValue(int(f.ProjectLock))}
}

// Abstract is s in the spec's vocabulary. The returned top-level map is fresh;
// nested encodings are shared, read-only values. Callers must not mutate them.
func Abstract(s State) map[string]any {
	gate := []any{}
	for h := 0; h < Heads; h++ {
		gate = append(gate, []any{headValue(h + 1), gateNames[s.Gate[h]]})
	}
	return map[string]any{
		"state":        kindNames[s.Kind],
		"open":         openSet(s.Open),
		"proposal":     lockValue(int(s.Proposal)),
		"amends":       lockValue(int(s.Amends)),
		"base":         lockValue(int(s.Base)),
		"ratified":     lockValue(int(s.Ratified)),
		"ratifiedBase": lockValue(int(s.RatifiedBase)),
		"head":         headValue(int(s.Head)),
		"gate":         map[string]any{"$fn": gate},
		"prLock":       lockValue(int(s.PrLock)),
		"scope":        scopeNames[s.Scope],
		"mergedBy":     mergedByNames[s.MergedBy],
		"mergedHead":   headValue(int(s.MergedHead)),
		"directedBy":   directedValue(int(s.DirectedBy)),
		"stops":        int(s.Stops),
		"failure":      failureNames[s.Failure],
		"rebuiltAfter": failureNames[s.RebuiltAfter],
		"sourceActor":  directedValue(int(s.SourceActor)),
		"source":       sourceValue(s.Source),
		"follow":       followValue(s.Follow),
		"permit":       receiptValue(s.Permit),
		"audit":        auditValue(s.Audit),
	}
}
