// +gobra

package protocol

import "testing"

// These identities intentionally exceed every explorer bound. A receipt here
// stands for external Workflow acceptance, never a runtime local classifier.
func followupTestState() (Followup, FollowupSource, Actor) {
	s := FollowupSource{Present: true, IsPlan: true, Owes: true, Kind: KindMerged,
		Proposal: 91001, Ratified: 91001, PrLock: 91001, Head: 92001, MergedHead: 92001,
		PR: 93001, Decision: 94001, CandidatePR: 95001,
		Claim: Authority{Plan: 91001, SourcePR: 93001, Decision: 94001}}
	f := NewFollowup(WorkflowView{Token: 96001, Phase: CorePhaseReady, Allowance: 97001}, 98001)
	f.PR, f.Head, f.CIHead, f.ScopeHead = s.CandidatePR, 99001, 99001, 99001
	f.Gate, f.Decisions, f.Graph = FollowGatePass, true, true
	f.Paths = FollowupArtifacts{Decisions: true, Spec: true, Readme: true}
	f.Tied = true
	return f, s, Actor{ID: 100001, Writer: true}
}

func followupTestReceipt(f Followup, s FollowupSource, actor Actor, authenticated bool, operation int, after WorkflowView, outcome int) WorkflowReceipt {
	return WorkflowReceipt{Present: true, Accepted: true, Before: f.Core,
		Operation: operation, Outcome: outcome, After: after,
		Context: FollowupContextFor(f, s, actor, authenticated)}
}

func TestFollowupSourceAuthorization(t *testing.T) {
	f, source, _ := followupTestState()
	actor := Actor{}
	f.PR = NoPR
	after := WorkflowView{Token: 96002, Phase: CorePhaseOpen, Allowance: 97001}
	for _, tc := range []struct {
		name   string
		change func(*FollowupSource)
	}{
		{"missing source", func(s *FollowupSource) { s.Present = false }},
		{"unmerged source", func(s *FollowupSource) { s.Kind = KindPROpen }},
		{"non owing source", func(s *FollowupSource) { s.Owes = false }},
		{"not plan", func(s *FollowupSource) { s.IsPlan = false }},
		{"wrong plan", func(s *FollowupSource) { s.Claim.Plan++ }},
		{"unratified proposal", func(s *FollowupSource) { s.Proposal++ }},
		{"wrong source lock", func(s *FollowupSource) { s.PrLock++ }},
		{"wrong merged head", func(s *FollowupSource) { s.MergedHead++ }},
		{"forged association", func(s *FollowupSource) { s.CandidatePR++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := source
			tc.change(&s)
			receipt := followupTestReceipt(f, s, actor, false, CoreOpen, after, OutcomeOK)
			next, ok := ApplyFollowupStep(f, s, receipt, actor, false, source.PR, source.CandidatePR, true, true)
			if ok || next != f {
				t.Fatal("invalid source or association changed follow-up")
			}
		})
	}
	before := source
	receipt := followupTestReceipt(f, source, actor, false, CoreOpen, after, OutcomeOK)
	next, ok := ApplyFollowupStep(f, source, receipt, actor, false, source.PR, source.CandidatePR, true, true)
	if !ok || next.PR != source.CandidatePR || next.Core != after || source != before {
		t.Fatal("eligible source did not open its independent follow-up without changing history")
	}
	if next.PlanRatification != NoP || next.ProjectLock != NoP || OrdinaryAllowed(next) {
		t.Fatal("follow-up fabricated ordinary authority or left ordinary operations enabled")
	}
}

func TestFollowupReceiptFreshness(t *testing.T) {
	f, s, _ := followupTestState()
	actor := Actor{}
	f.CIHead, f.ScopeHead = NoHead, NoHead
	receipt := followupTestReceipt(f, s, actor, false, CoreClassify,
		WorkflowView{Token: 96002, Phase: CorePhaseReady, Allowance: 97001}, OutcomeOK)
	ci, ciOK := ObserveFollowupCI(f, true, FollowGateSkip, true, true)
	scope, scopeOK := ObserveFollowupScope(f, true)
	head, headOK := PushFollowup(f, true, f.Head+101, f.Paths, true, f.Target)
	if !ciOK || !scopeOK || !headOK || head.CIHead != f.CIHead || head.ScopeHead != f.ScopeHead {
		t.Fatal("observations did not retain independent revision evidence")
	}
	for name, changed := range map[string]Followup{"CI": ci, "scope": scope, "head": head} {
		t.Run(name, func(t *testing.T) {
			if MatchingWorkflowReceipt(changed, s, receipt, actor, false, true) {
				t.Fatal("stale complete-context receipt matched")
			}
			next, ok := ApplyFollowupStep(changed, s, receipt, actor, false, s.PR, s.CandidatePR, true, true)
			if ok || next != changed {
				t.Fatal("stale acceptance changed follow-up")
			}
		})
	}
	for _, accepted := range []bool{false, true} {
		r := receipt
		r.Accepted = accepted
		next, ok := ApplyFollowupStep(f, s, r, actor, false, s.PR, s.CandidatePR, true, !accepted)
		if ok || next != f {
			t.Fatal("missing Workflow acceptance or an already applied receipt was accepted")
		}
	}
}

func TestFollowupObservationRefusals(t *testing.T) {
	f, _, _ := followupTestState()
	f.CIHead, f.ScopeHead = NoHead, NoHead
	for _, present := range []bool{false, true} {
		before := f
		if present {
			before.Core.Phase = CorePhaseMerged
		}
		ci, ciOK := ObserveFollowupCI(before, present, FollowGatePass, true, true)
		scope, scopeOK := ObserveFollowupScope(before, present)
		push, pushOK := PushFollowup(before, present, before.Head+101, before.Paths, true, before.Target)
		if ciOK || scopeOK || pushOK || ci != before || scope != before || push != before {
			t.Fatal("missing source or merged core admitted an observation")
		}
	}
	if next, ok := PushFollowup(f, true, f.Head, f.Paths, true, f.Target); ok || next != f {
		t.Fatal("a push of the current head changed follow-up")
	}
	f.PR = NoPR
	if next, ok := PushFollowup(f, true, f.Head+101, f.Paths, true, f.Target); ok || next != f {
		t.Fatal("a push opened an inactive follow-up")
	}
}

func TestFollowupMergeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*Followup)
		trusted bool
		allowed bool
	}{
		{"pass", func(*Followup) {}, true, true},
		{"admissible skip", func(f *Followup) { f.Gate = FollowGateSkip }, true, true},
		{"broad scope", func(f *Followup) { f.Paths.ProjectCode = true }, true, false},
		{"stale scope", func(f *Followup) { f.ScopeHead-- }, true, false},
		{"unrelated", func(f *Followup) { f.Tied = false }, true, false},
		{"stale CI", func(f *Followup) { f.CIHead-- }, true, false},
		{"no decision checks", func(f *Followup) { f.Decisions = false }, true, false},
		{"skip without graph", func(f *Followup) { f.Graph = false; f.Gate = FollowGateSkip }, true, false},
		{"failed gate", func(f *Followup) { f.Gate = FollowGateFail }, true, false},
		{"human hold", func(f *Followup) { f.Core.Hold = true }, true, false},
		{"untrusted base", func(*Followup) {}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, s, _ := followupTestState()
			actor := Actor{}
			tc.change(&f)
			after := WorkflowView{Token: 96002, Phase: CorePhaseMerged, Allowance: 97001}
			receipt := followupTestReceipt(f, s, actor, false, CoreMerge, after, OutcomeOK)
			next, ok := ApplyFollowupStep(f, s, receipt, actor, false, s.PR, s.CandidatePR, tc.trusted, true)
			if ok != tc.allowed || (!ok && next != f) || (ok && (next.MergedHead != f.Head || next.PR != f.PR)) {
				t.Fatal("merge violated current revision, scope, checks, hold, base or identity policy")
			}
		})
	}
}

func TestFollowupRetryResumeAndEffects(t *testing.T) {
	f, s, writer := followupTestState()
	f.Core.Phase, f.Core.Repairs = CorePhaseBlocked, 2
	renewed := WorkflowView{Token: 96002, Phase: CorePhaseOpen, Allowance: f.Core.Allowance + 113}
	for _, tc := range []struct {
		name          string
		actor         Actor
		authenticated bool
		after         WorkflowView
		allowed       bool
	}{
		{"writer", writer, true, renewed, true},
		{"unauthenticated", writer, false, renewed, false},
		{"bot", Actor{ID: writer.ID, Writer: true, Bot: true}, true, renewed, false},
		{"non writer", Actor{ID: writer.ID}, true, renewed, false},
		{"same allowance", writer, true, WorkflowView{Token: 96002, Phase: CorePhaseOpen, Allowance: f.Core.Allowance}, false},
		{"retained repair", writer, true, WorkflowView{Token: 96002, Phase: CorePhaseOpen, Allowance: renewed.Allowance, Repairs: 1}, false},
		{"third repair", writer, true, WorkflowView{Token: 96002, Phase: CorePhaseOpen, Allowance: renewed.Allowance, Repairs: 3}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := followupTestReceipt(f, s, tc.actor, tc.authenticated, CoreRetry, tc.after, OutcomeOK)
			next, ok := ApplyFollowupStep(f, s, r, tc.actor, tc.authenticated, s.PR, s.CandidatePR, true, true)
			if ok != tc.allowed || (!ok && next != f) {
				t.Fatal("retry violated command authorization or allowance policy")
			}
		})
	}
	f.Core.Phase, f.Core.Hold = CorePhaseClosed, true
	r := followupTestReceipt(f, s, writer, true, CoreResume, renewed, OutcomeOK)
	resumed, ok := ApplyFollowupStep(f, s, r, writer, true, s.PR, s.CandidatePR, true, true)
	if !ok || resumed.PR != f.PR || resumed.Core.Hold || resumed.Core.Phase != CorePhaseOpen {
		t.Fatal("closed follow-up did not resume its existing PR")
	}
	r = followupTestReceipt(f, s, writer, true, CoreRetry, renewed, OutcomeOK)
	if next, ok := ApplyFollowupStep(f, s, r, writer, true, s.PR, s.CandidatePR, true, true); ok || next != f {
		t.Fatal("retry incorrectly cleared a closed follow-up's hold")
	}
	closed := WorkflowView{Token: 96003, Phase: CorePhaseClosed, Hold: true, Allowance: renewed.Allowance}
	r = followupTestReceipt(resumed, s, Actor{}, false, CoreClose, closed, OutcomeOK)
	closedState, ok := ApplyFollowupStep(resumed, s, r, Actor{}, false, s.PR, s.CandidatePR, true, true)
	if !ok || !closedState.Core.Hold || closedState.PR != resumed.PR {
		t.Fatal("close did not preserve the human hold and stable identity")
	}
	for _, outcome := range []int{OutcomeFailed, 123456789} {
		after := resumed.Core
		after.Token++
		r = followupTestReceipt(resumed, s, Actor{}, false, CoreEffect, after, outcome)
		next, ok := ApplyFollowupStep(resumed, s, r, Actor{}, false, s.PR, s.CandidatePR, true, true)
		if !ok || next.Outcome != outcome || next.Core != after || next.PR != resumed.PR {
			t.Fatal("effect result was reclassified or promoted instead of copied exactly")
		}
	}
}
