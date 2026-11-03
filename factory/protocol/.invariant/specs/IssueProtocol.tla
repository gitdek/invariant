---- MODULE IssueProtocol ----
EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Actors, Writers, Bots, Questions, Proposals, Heads, NoP, NoHead, NoActor,
          PullRequests, SourcePR, FollowPR, NoPR, Decisions,
          WorkflowInitial, WorkflowViewRows, WorkflowStepRows, BaseRefs, TrustedBases,
          NoSource, NoPermit, NoAudit

VARIABLES state, open, proposal, amends, base, ratified, ratifiedBase,
          head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
          stops, failure, rebuiltAfter,
          sourceActor, source, follow, permit, audit

ordinaryVars == <<state, open, proposal, amends, base, ratified, ratifiedBase,
          head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
          stops, failure, rebuiltAfter>>

followVars == <<sourceActor, source, follow, permit, audit>>

vars == <<state, open, proposal, amends, base, ratified, ratifiedBase,
          head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
          stops, failure, rebuiltAfter,
          sourceActor, source, follow, permit, audit>>

Kinds == {"none", "asked", "proposed", "stuck", "unsupported",
          "ratified", "pr_open", "failed", "merged", "closed"}

\* Why the issue's latest failure happened.
Failures == {"none", "stopped", "limit", "gate", "ci", "unmergeable"}

\* rebuiltAfter is the failure a writer's retry last built the ratified
\* proposal again after, while the factory's run of the new build is all
\* there is to see: drafting again, a push to the pull request or a move of
\* the base branch forgets it. Only RebuildsOwnFailures and the witnesses of
\* a rebuild read it, and forgetting it keeps the model from holding every
\* later state twice.

\* Builds of an issue that may stop partway before the factory refuses another.
MaxStops == 2

\* Only people with write access who are not bots direct the factory.
Directors == Writers \ Bots

Locks == Proposals \cup {NoP}

\* States in which a ratified proposal is being built or has been merged.
Building == {"ratified", "pr_open", "failed", "merged"}

FactoryMerged == state = "merged" /\ mergedBy = "factory"

\* The original issue variables continue to describe the ordinary source build.
\* Follow-up work has separate authority and never changes that history.
\* WorkflowSteps and WorkflowViews are an interface to the independently proved,
\* amended decision-followup.Workflow, not a replacement implementation of it.
\* A step is <<before token, operation, effect outcome, after token>>.
\* A view is <<token, phase, human hold, allowance token, repairs used>>.
\* The checking bounds supply small illustrative accepted paths. Production
\* acceptance must come from that Workflow for the entire matching context.

\* TLC config values support nested sets; these tagged rows encode tuples
\* without baking the external Workflow relation into pinned helper definitions.
RowField(row, tag) ==
    CHOOSE value \in ((CHOOSE field \in row : tag \in field) \ {tag}) : TRUE

RowRepairCount(value) ==
    CASE value = "0" -> 0
      [] value = "1" -> 1
      [] value = "2" -> 2
      [] OTHER -> 3

WorkflowViews ==
    {<<RowField(row, "tokenTag"), RowField(row, "phaseTag"),
       RowField(row, "holdTag") = "true", RowField(row, "allowanceTag"),
       RowRepairCount(RowField(row, "repairsTag"))>> : row \in WorkflowViewRows}

WorkflowSteps ==
    {<<RowField(row, "beforeTag"), RowField(row, "operationTag"),
       RowField(row, "outcomeTag"), RowField(row, "afterTag")>> : row \in WorkflowStepRows}

CoreStates == {v[1] : v \in WorkflowViews}

CoreView(c) == CHOOSE v \in WorkflowViews : v[1] = c

CorePhase(c) == CoreView(c)[2]

CoreHold(c) == CoreView(c)[3]

CoreAllowance(c) == CoreView(c)[4]

CoreRepairs(c) == CoreView(c)[5]

CoreOperations == {"open", "classify", "repair", "effect", "close", "retry", "resume", "merge"}

CoreOutcomes == {t[3] : t \in WorkflowSteps}

CoreTransitionType == CoreStates \X CoreOperations \X CoreOutcomes \X CoreStates

AllowedFollowupArtifacts == {"decisions/", "SPEC.md", "README.md"}

\* Artifact values denote path classes; decisions/ includes its descendants.
ArtifactClasses == AllowedFollowupArtifacts \cup {"project-code"}

FollowGateResults == {"none", "pass", "fail", "skip"}

SourceHistory ==
    [state |-> state, proposal |-> proposal, ratified |-> ratified,
     ratifiedBase |-> ratifiedBase, amends |-> amends, open |-> open,
     head |-> head, prLock |-> prLock, mergedBy |-> mergedBy,
     mergedHead |-> mergedHead, merger |-> sourceActor]

SourceHistoryType ==
    [state : Kinds, proposal : Locks, ratified : Locks, ratifiedBase : Locks,
     amends : Locks, open : SUBSET Questions, head : Heads \cup {NoHead},
     prLock : Locks, mergedBy : {"nobody", "factory", "other"},
     mergedHead : Heads \cup {NoHead}, merger : Actors \cup {NoActor}]

AuthorityType == Locks \X PullRequests \X Decisions

SourceType ==
    [history : SourceHistoryType, isPlan : BOOLEAN, owes : BOOLEAN,
     pr : PullRequests, decision : Decisions, claim : AuthorityType,
     candidatePR : PullRequests]

FollowType ==
    [pr : PullRequests \cup {NoPR}, head : Heads \cup {NoHead},
     ciHead : Heads \cup {NoHead}, gate : FollowGateResults,
     decisions : BOOLEAN, graph : BOOLEAN, scopeHead : Heads \cup {NoHead},
     paths : SUBSET ArtifactClasses, tied : BOOLEAN, target : BaseRefs,
     core : CoreStates, mergedHead : Heads \cup {NoHead},
     outcome : CoreOutcomes \cup {"none"}, resumed : BOOLEAN,
     planRatification : Locks, projectLock : Locks]

ContextType ==
    [authority : AuthorityType, sourcePR : PullRequests, pr : PullRequests,
     head : Heads \cup {NoHead}, core : CoreStates,
     actor : Actors \cup {NoActor}, authenticated : BOOLEAN,
     ci : (Heads \cup {NoHead}) \X FollowGateResults \X BOOLEAN \X BOOLEAN,
     scope : (Heads \cup {NoHead}) \X (SUBSET ArtifactClasses) \X BOOLEAN,
     target : BaseRefs]

PermitType == [transition : CoreTransitionType, context : ContextType]

AuditType ==
    [receipt : PermitType, request : ContextType,
     beforePR : PullRequests \cup {NoPR}, afterPR : PullRequests \cup {NoPR},
     beforeCore : CoreStates, afterCore : CoreStates,
     operation : CoreOperations, outcome : CoreOutcomes]

FollowupTypes ==
    /\ sourceActor \in Actors \cup {NoActor}
    /\ source \in {NoSource} \cup SourceType
    /\ follow \in FollowType
    /\ permit \in {NoPermit} \cup PermitType
    /\ audit \in {NoAudit} \cup AuditType

SourceAuthority == <<source.history.ratified, source.pr, source.decision>>

EligibleSource ==
    /\ source # NoSource
    /\ source.isPlan
    /\ source.owes
    /\ source.history.state = "merged"
    /\ source.history.ratified # NoP
    /\ source.history.proposal = source.history.ratified
    /\ source.history.prLock = source.history.ratified
    /\ source.history.mergedHead = source.history.head
    /\ source.history.mergedHead # NoHead
    /\ source.pr = SourcePR

\* FollowPR denotes the PR identity in the independently verified obligation
\* binding. Other PRs may present the identical branch name and claimed source.
VerifiedAssociation ==
    /\ source.claim = SourceAuthority
    /\ source.candidatePR = FollowPR
    /\ source.candidatePR # source.pr

FollowupActive == follow.pr # NoPR

FollowupMerged == FollowupActive /\ CorePhase(follow.core) = "merged"

FollowupScopeOK ==
    /\ follow.scopeHead = follow.head
    /\ follow.paths \subseteq AllowedFollowupArtifacts
    /\ follow.tied

FollowupChecksOK ==
    /\ follow.ciHead = follow.head
    /\ follow.decisions
    /\ follow.graph
    /\ follow.gate \in {"pass", "skip"}
    /\ (follow.gate = "skip" => FollowupScopeOK)

\* The receipt binds even observations that can change while a core call is
\* outstanding. A fresh head or new scope/check evidence needs fresh acceptance.
FollowContext(a, authenticated) ==
    [authority |-> source.claim, sourcePR |-> source.pr,
     pr |-> source.candidatePR, head |-> follow.head, core |-> follow.core,
     actor |-> a, authenticated |-> authenticated,
     ci |-> <<follow.ciHead, follow.gate, follow.decisions, follow.graph>>,
     scope |-> <<follow.scopeHead, follow.paths, follow.tied>>,
     target |-> follow.target]

FollowupSourceAuthorized == FollowupActive => EligibleSource

FollowupAssociationAuthenticated == FollowupActive => VerifiedAssociation

FollowupSourcePreserved == source # NoSource => SourceHistory = source.history

FollowupStableIdentity ==
    /\ (FollowupActive => follow.pr = source.candidatePR /\ follow.pr # source.pr)
    /\ (audit # NoAudit /\ audit.beforePR # NoPR => audit.afterPR = audit.beforePR)

FollowupNoNewRatification ==
    /\ follow.planRatification = NoP
    /\ follow.projectLock = NoP

FollowupMergedCurrentHead == FollowupMerged => follow.mergedHead = follow.head

FollowupNarrowMerge == FollowupMerged => FollowupScopeOK

FollowupCurrentDecisionChecks == FollowupMerged => FollowupChecksOK

FollowupTrustedBase == FollowupMerged => follow.target \in TrustedBases

FollowupCommandsAuthorized ==
    audit # NoAudit /\ audit.operation \in {"retry", "resume"} =>
        /\ audit.request.actor \in Directors
        /\ audit.request.authenticated

FollowupCoreAccepted ==
    audit # NoAudit =>
        /\ audit.receipt = permit
        /\ audit.receipt.transition \in WorkflowSteps
        /\ audit.receipt.context = audit.request
        /\ audit.beforeCore = audit.receipt.transition[1]
        /\ audit.beforeCore = audit.request.core
        /\ audit.afterCore = audit.receipt.transition[4]
        /\ follow.core = audit.afterCore
        /\ audit.operation = audit.receipt.transition[2]
        /\ audit.outcome = audit.receipt.transition[3]
        /\ follow.outcome = audit.outcome

FollowupHumanHold ==
    /\ (FollowupActive /\ CorePhase(follow.core) = "closed" => CoreHold(follow.core))
    /\ (FollowupMerged => ~CoreHold(follow.core))
    /\ (audit # NoAudit /\ CoreHold(audit.beforeCore) /\ ~CoreHold(audit.afterCore)
           => audit.operation = "resume")

FollowupRetryRenewsAllowance ==
    audit # NoAudit /\ audit.operation = "retry" =>
        /\ CorePhase(audit.beforeCore) = "blocked"
        /\ CoreAllowance(audit.afterCore) # CoreAllowance(audit.beforeCore)
        /\ CoreRepairs(audit.afterCore) = 0

FollowupClosedRetryResumes ==
    audit # NoAudit /\ audit.operation = "resume" =>
        /\ CorePhase(audit.beforeCore) = "closed"
        /\ audit.afterPR = audit.beforePR
        /\ audit.afterPR # NoPR

FollowupTwoRepairsPerAllowance == FollowupActive => CoreRepairs(follow.core) \in 0..2

HumanMergedPlanFollowupMerges ==
    /\ FollowupMerged
    /\ source.history.mergedBy = "other"
    /\ source.history.merger \in Directors

ClosedFollowupResumesSamePR ==
    /\ FollowupActive
    /\ follow.resumed
    /\ CorePhase(follow.core) = "open"
    /\ follow.pr = FollowPR
    /\ ~CoreHold(follow.core)

SkippedProjectGateFollowupMerges == FollowupMerged /\ follow.gate = "skip"

BlockedFollowupGetsNewAllowance == audit # NoAudit /\ audit.operation = "retry"

ClosedFollowupKeepsHold == FollowupActive /\ CorePhase(follow.core) = "closed" /\ CoreHold(follow.core)

AcceptedEffectFailurePreserved == audit # NoAudit /\ audit.operation = "effect" /\ audit.outcome = "failed"


TypeOK ==
    /\ state \in Kinds
    /\ open \subseteq Questions
    /\ proposal \in Locks
    /\ amends \in Locks
    /\ base \in Locks
    /\ ratified \in Locks
    /\ ratifiedBase \in Locks
    /\ head \in Heads \cup {NoHead}
    /\ gate \in [Heads -> {"none", "pending", "pass", "fail"}]
    /\ prLock \in Locks
    /\ scope \in {"one", "many"}
    /\ mergedBy \in {"nobody", "factory", "other"}
    /\ mergedHead \in Heads \cup {NoHead}
    /\ directedBy \in Actors \cup {NoActor}
    /\ stops \in Nat
    /\ failure \in Failures
    /\ rebuiltAfter \in Failures
    /\ FollowupTypes

OnlyDirectorsDirect == directedBy \in Directors \cup {NoActor}

RatifiesCurrentProposal ==
    state \in Building => (ratified # NoP /\ ratified = proposal)

NoOpenQuestionsWhenRatified == state \in Building => open = {}

AmendsHeldLock == state \in Building => ratifiedBase = amends

MergedGatePassed == FactoryMerged => gate[mergedHead] = "pass"

MergedCurrentHead == FactoryMerged => mergedHead = head

MergedOneProject == FactoryMerged => scope = "one"

MergedLockRatified == FactoryMerged => prLock = ratified

StopLimit == stops <= MaxStops

StoppedHasNoPullRequest == failure \in {"stopped", "limit"} => head = NoHead

\* When a ratified proposal waits for its build, nothing is left of an earlier
\* pull request: no head, CI result, lock, scope or merge.
BuildStartsClean ==
    state = "ratified" =>
        /\ head = NoHead
        /\ gate = [h \in Heads |-> "none"]
        /\ prLock = NoP
        /\ scope = "one"
        /\ mergedBy = "nobody"
        /\ mergedHead = NoHead

\* A retry retires a pull request to build its ratified proposal again only
\* after the build failed its own gate in the factory's run, never after CI's
\* gate failed or the pull request couldn't merge.
RebuildsOwnFailures == rebuiltAfter \in {"none", "gate"}

\* Witnesses.
QuestionsAsked == state = "asked"

GotStuck == state = "stuck"

FoundUnsupported == state = "unsupported"

PullRequestFailed == state = "failed"

OthersClosed == state = "closed"

OthersMerged == state = "merged" /\ mergedBy = "other"

FactoryMerges == FactoryMerged

AmendmentMerges == FactoryMerged /\ ratifiedBase # NoP

BuildStopped == state = "failed" /\ failure = "stopped"

BuildRefusedAtLimit == state = "failed" /\ failure = "limit"

OwnGateFailed == state = "failed" /\ failure = "gate" /\ head # NoHead

CannotMerge == state = "failed" /\ failure = "unmergeable"

RebuiltAndMerged == rebuiltAfter = "gate" /\ FactoryMerged

RebuiltAndStopped == rebuiltAfter = "gate" /\ state = "failed" /\ failure = "stopped"

RebuiltFailedAgain == rebuiltAfter = "gate" /\ state = "failed" /\ failure = "gate" /\ head # NoHead


\* The draft model.
OrdinaryInit ==
    /\ state = "none"
    /\ open = {}
    /\ proposal = NoP
    /\ amends = NoP
    /\ base = NoP
    /\ ratified = NoP
    /\ ratifiedBase = NoP
    /\ head = NoHead
    /\ gate = [h \in Heads |-> "none"]
    /\ prLock = NoP
    /\ scope = "one"
    /\ mergedBy = "nobody"
    /\ mergedHead = NoHead
    /\ directedBy = NoActor
    /\ stops = 0
    /\ failure = "none"
    /\ rebuiltAfter = "none"

ResetPR ==
    /\ head' = NoHead
    /\ gate' = [h \in Heads |-> "none"]
    /\ prLock' = NoP
    /\ scope' = "one"
    /\ mergedBy' = "nobody"
    /\ mergedHead' = NoHead

\* A build stopped before it made a pull request.
StoppedBuild == state = "failed" /\ head = NoHead

\* Every draft the factory can post: questions, statements, stuck or unsupported.
Drafts ==
    {[kind |-> "asked", open |-> Q, p |-> NoP] : Q \in (SUBSET Questions) \ {{}}}
    \cup {[kind |-> "proposed", open |-> {}, p |-> p] : p \in Proposals}
    \cup {[kind |-> k, open |-> {}, p |-> NoP] : k \in {"stuck", "unsupported"}}

\* The factory posts draft d: it asks questions, proposes statements against
\* the base branch's current lock, gets stuck, or finds the issue unsupported.
Draft(d) ==
    /\ ResetPR
    /\ ratified' = NoP
    /\ ratifiedBase' = NoP
    /\ failure' = "none"
    /\ rebuiltAfter' = "none"
    /\ UNCHANGED <<base, stops>>
    /\ state' = d.kind
    /\ open' = d.open
    /\ proposal' = d.p
    /\ amends' = IF d.kind = "proposed" THEN base ELSE NoP

Solve(a, d) ==
    /\ a \in Directors
    /\ state \in {"none", "stuck", "unsupported", "closed"} \/ StoppedBuild
    /\ directedBy' = a
    /\ Draft(d)

Revise(a, d) ==
    /\ a \in Directors
    /\ state \in {"asked", "proposed", "stuck", "unsupported", "closed"} \/ StoppedBuild
    /\ directedBy' = a
    /\ Draft(d)

\* Answering the last open question drafts again, as d.
Choose(a, q, d) ==
    /\ a \in Directors
    /\ state = "asked"
    /\ q \in open
    /\ directedBy' = a
    /\ IF open = {q}
          THEN Draft(d)
          ELSE /\ open' = open \ {q}
               /\ UNCHANGED <<state, proposal, amends, base, ratified, ratifiedBase,
                              head, gate, prLock, scope, mergedBy, mergedHead,
                              stops, failure, rebuiltAfter>>

Ratify(a, p) ==
    /\ a \in Directors
    /\ state = "proposed"
    /\ open = {}
    /\ p = proposal
    /\ base = amends
    /\ state' = "ratified"
    /\ ratified' = p
    /\ ratifiedBase' = base
    /\ directedBy' = a
    /\ UNCHANGED <<open, proposal, amends, base,
                   head, gate, prLock, scope, mergedBy, mergedHead,
                   stops, failure, rebuiltAfter>>

Build(h) ==
    /\ state = "ratified"
    /\ stops < MaxStops
    /\ h \in Heads
    /\ state' = "pr_open"
    /\ head' = h
    /\ gate' = [gate EXCEPT ![h] = "pending"]
    /\ prLock' = ratified
    /\ scope' = "one"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   mergedBy, mergedHead, directedBy, stops, failure, rebuiltAfter>>

\* The build starts, then stops before it makes a pull request.
StopBuild ==
    /\ state = "ratified"
    /\ stops < MaxStops
    /\ state' = "failed"
    /\ stops' = stops + 1
    /\ failure' = "stopped"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy, rebuiltAfter>>

\* Two builds have already stopped partway, so the factory won't start another.
RefuseBuild ==
    /\ state = "ratified"
    /\ stops = MaxStops
    /\ state' = "failed"
    /\ failure' = "limit"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
                   stops, rebuiltAfter>>

\* The code fails the gate in the factory's own run: it opens a draft pull
\* request and posts that the build failed.
BuildFailsGate(h) ==
    /\ state = "ratified"
    /\ stops < MaxStops
    /\ h \in Heads
    /\ state' = "failed"
    /\ head' = h
    /\ gate' = [gate EXCEPT ![h] = "pending"]
    /\ prLock' = ratified
    /\ scope' = "one"
    /\ failure' = "gate"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   mergedBy, mergedHead, directedBy, stops, rebuiltAfter>>

\* Someone pushes a new head commit to the pull request, with any content.
Push(h, l, s) ==
    /\ state \in {"pr_open", "failed"}
    /\ head # NoHead
    /\ h # head
    /\ head' = h
    /\ gate' = [gate EXCEPT ![h] = "pending"]
    /\ prLock' = l
    /\ scope' = s
    /\ rebuiltAfter' = "none"
    /\ UNCHANGED <<state, open, proposal, amends, base, ratified, ratifiedBase,
                   mergedBy, mergedHead, directedBy, stops, failure>>

\* CI's gate reports result r on the pull request's current head.
CIGate(r) ==
    /\ state \in {"pr_open", "failed"}
    /\ head # NoHead
    /\ gate[head] = "pending"
    /\ r \in {"pass", "fail"}
    /\ gate' = [gate EXCEPT ![head] = r]
    /\ UNCHANGED <<state, open, proposal, amends, base, ratified, ratifiedBase,
                   head, prLock, scope, mergedBy, mergedHead, directedBy,
                   stops, failure, rebuiltAfter>>

NoticeFail ==
    /\ state = "pr_open"
    /\ gate[head] = "fail"
    /\ state' = "failed"
    /\ failure' = "ci"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
                   stops, rebuiltAfter>>

\* CI's gate passed, but the pull request changes more than its one project
\* or its lock isn't the ratified proposal.
NoticeUnmergeable ==
    /\ state = "pr_open"
    /\ gate[head] = "pass"
    /\ (scope # "one" \/ prLock # ratified)
    /\ state' = "failed"
    /\ failure' = "unmergeable"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
                   stops, rebuiltAfter>>

\* Retry reopens a failed pull request, or builds a stopped build's ratified
\* proposal again with the stop limit reset.
Retry(a) ==
    /\ a \in Directors
    /\ state = "failed"
    /\ directedBy' = a
    /\ failure' = "none"
    /\ IF head = NoHead
          THEN state' = "ratified" /\ stops' = 0
          ELSE state' = "pr_open" /\ UNCHANGED stops
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, rebuiltAfter>>

\* Rebuild builds the ratified proposal again after the build failed its own
\* gate in the factory's run and left a draft pull request. The ratification
\* stands; the draft, its head and CI's result there are retired, so none of
\* them stands for the next build, and the stops are counted afresh.
Rebuild(a) ==
    /\ a \in Directors
    /\ state = "failed"
    /\ failure = "gate"
    /\ head # NoHead
    /\ state' = "ratified"
    /\ directedBy' = a
    /\ failure' = "none"
    /\ stops' = 0
    /\ rebuiltAfter' = failure
    /\ ResetPR
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase>>

Merge ==
    /\ state = "pr_open"
    /\ gate[head] = "pass"
    /\ scope = "one"
    /\ prLock = ratified
    /\ state' = "merged"
    /\ mergedBy' = "factory"
    /\ mergedHead' = head
    /\ base' = prLock
    /\ UNCHANGED <<open, proposal, amends, ratified, ratifiedBase,
                   head, gate, prLock, scope, directedBy, stops, failure, rebuiltAfter>>

OthersMerge ==
    /\ state \in {"pr_open", "failed"}
    /\ head # NoHead
    /\ state' = "merged"
    /\ mergedBy' = "other"
    /\ mergedHead' = head
    /\ base' = prLock
    /\ failure' = "none"
    /\ UNCHANGED <<open, proposal, amends, ratified, ratifiedBase,
                   head, gate, prLock, scope, directedBy, stops, rebuiltAfter>>

OthersClose ==
    /\ state \in {"pr_open", "failed"}
    /\ head # NoHead
    /\ state' = "closed"
    /\ failure' = "none"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
                   stops, rebuiltAfter>>

\* The base branch's lock changes elsewhere.
BaseMoves(l) ==
    /\ l # base
    /\ base' = l
    /\ rebuiltAfter' = "none"
    /\ UNCHANGED <<state, open, proposal, amends, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
                   stops, failure>>

Finished == state = "merged" /\ UNCHANGED vars

OrdinaryNext ==
    \/ \E a \in Actors :
        \/ \E d \in Drafts : Solve(a, d) \/ Revise(a, d)
        \/ Retry(a)
        \/ Rebuild(a)
        \/ \E q \in Questions, d \in Drafts : Choose(a, q, d)
        \/ \E p \in Proposals : Ratify(a, p)
    \/ \E h \in Heads : Build(h) \/ BuildFailsGate(h)
    \/ StopBuild
    \/ RefuseBuild
    \/ \E h \in Heads, l \in Locks, s \in {"one", "many"} : Push(h, l, s)
    \/ \E r \in {"pass", "fail"} : CIGate(r)
    \/ NoticeFail
    \/ NoticeUnmergeable
    \/ Merge
    \/ OthersClose
    \/ \E l \in Locks : BaseMoves(l)
    \/ Finished

\* Known bugs.

\* A bot or someone without write access gets the factory to take the issue.
ObeyAnyone ==
    /\ UNCHANGED followVars
    /\ source = NoSource
    /\ (
        \E a \in Actors \ Directors :
            /\ state \in {"none", "stuck", "unsupported", "closed"}
            /\ state' = "stuck"
            /\ directedBy' = a
            /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                           head, gate, prLock, scope, mergedBy, mergedHead,
                           stops, failure, rebuiltAfter>>
        )

\* A ratify naming an earlier proposal's hash is accepted.
RatifyEarlier ==
    /\ UNCHANGED followVars
    /\ source = NoSource
    /\ (
        \E a \in Directors, p \in Proposals \ {proposal} :
            /\ state = "proposed"
            /\ base = amends
            /\ state' = "ratified"
            /\ ratified' = p
            /\ ratifiedBase' = base
            /\ directedBy' = a
            /\ UNCHANGED <<open, proposal, amends, base,
                           head, gate, prLock, scope, mergedBy, mergedHead,
                           stops, failure, rebuiltAfter>>
        )

\* An amendment is ratified after the base branch's lock has moved.
RatifyOverMovedLock ==
    /\ UNCHANGED followVars
    /\ source = NoSource
    /\ (
        \E a \in Directors :
            /\ state = "proposed"
            /\ state' = "ratified"
            /\ ratified' = proposal
            /\ ratifiedBase' = base
            /\ directedBy' = a
            /\ UNCHANGED <<open, proposal, amends, base,
                           head, gate, prLock, scope, mergedBy, mergedHead,
                           stops, failure, rebuiltAfter>>
        )

\* The factory merges whichever head passed CI, not the current one.
MergeOnAnyPass ==
    /\ UNCHANGED followVars
    /\ source = NoSource
    /\ (
        \E h \in Heads :
            /\ state = "pr_open"
            /\ gate[h] = "pass"
            /\ scope = "one"
            /\ prLock = ratified
            /\ state' = "merged"
            /\ mergedBy' = "factory"
            /\ mergedHead' = h
            /\ base' = prLock
            /\ UNCHANGED <<open, proposal, amends, ratified, ratifiedBase,
                           head, gate, prLock, scope, directedBy, stops, failure, rebuiltAfter>>
        )

\* The factory merges without checking the project's lock.
MergeIgnoringLock ==
    /\ UNCHANGED followVars
    /\ source = NoSource
    /\ (
        /\ state = "pr_open"
        /\ gate[head] = "pass"
        /\ scope = "one"
        /\ state' = "merged"
        /\ mergedBy' = "factory"
        /\ mergedHead' = head
        /\ base' = prLock
        /\ UNCHANGED <<open, proposal, amends, ratified, ratifiedBase,
                       head, gate, prLock, scope, directedBy, stops, failure, rebuiltAfter>>
        )

\* The factory starts a third build after two have stopped partway.
StartPastLimit ==
    /\ UNCHANGED followVars
    /\ source = NoSource
    /\ (
        /\ state = "ratified"
        /\ stops = MaxStops
        /\ state' = "failed"
        /\ stops' = stops + 1
        /\ failure' = "stopped"
        /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                       head, gate, prLock, scope, mergedBy, mergedHead, directedBy, rebuiltAfter>>
        )

\* The factory merges a draft pull request whose own gate failed, before
\* anyone says retry.
MergeFailedBuild ==
    /\ UNCHANGED followVars
    /\ source = NoSource
    /\ (
        /\ state = "failed"
        /\ head # NoHead
        /\ state' = "merged"
        /\ mergedBy' = "factory"
        /\ mergedHead' = head
        /\ base' = prLock
        /\ UNCHANGED <<open, proposal, amends, ratified, ratifiedBase,
                       head, gate, prLock, scope, directedBy, stops, failure, rebuiltAfter>>
        )

\* A retry builds a failed build again but keeps its draft's head, CI's
\* result and lock for the next build.
RebuildKeepsDraft ==
    /\ UNCHANGED followVars
    /\ source = NoSource
    /\ (
        \E a \in Directors :
            /\ state = "failed"
            /\ failure = "gate"
            /\ head # NoHead
            /\ state' = "ratified"
            /\ directedBy' = a
            /\ failure' = "none"
            /\ stops' = 0
            /\ rebuiltAfter' = failure
            /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                           head, gate, prLock, scope, mergedBy, mergedHead>>
        )

\* A retry builds a pull request that failed CI's gate again, instead of
\* watching it again.
RebuildAfterCI ==
    /\ UNCHANGED followVars
    /\ source = NoSource
    /\ (
        \E a \in Directors :
            /\ state = "failed"
            /\ failure \in {"gate", "ci"}
            /\ head # NoHead
            /\ state' = "ratified"
            /\ directedBy' = a
            /\ failure' = "none"
            /\ stops' = 0
            /\ rebuiltAfter' = failure
            /\ ResetPR
            /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase>>
        )


\* Bounded environment and integration actions. The ordinary actions above
\* retain their existing arguments and meaning for current watcher callers.
FirstHead == CHOOSE h \in Heads : TRUE

OtherHead == CHOOSE h \in Heads : h # FirstHead

FirstPlan == CHOOSE p \in Proposals : TRUE

FirstDecision == CHOOSE d \in Decisions : TRUE

EmptyFollow ==
    [pr |-> NoPR, head |-> NoHead, ciHead |-> NoHead, gate |-> "none",
     decisions |-> FALSE, graph |-> FALSE, scopeHead |-> NoHead,
     paths |-> {}, tied |-> FALSE, target |-> CHOOSE b \in TrustedBases : TRUE,
     core |-> WorkflowInitial, mergedHead |-> NoHead,
     outcome |-> "none", resumed |-> FALSE,
     planRatification |-> NoP, projectLock |-> NoP]

Init ==
    /\ OrdinaryInit
    /\ sourceActor = NoActor
    /\ source = NoSource
    /\ follow = EmptyFollow
    /\ permit = NoPermit
    /\ audit = NoAudit

\* Canonical source observations bound this interface check, while the ordinary
\* issue continues to have its original behaviors while no follow-up is active;
\* an ordinary step discards any pending observation or acceptance receipt.
ObserveSource(mode) ==
    /\ source = NoSource
    /\ state \in {"pr_open", "merged"}
    /\ proposal = FirstPlan
    /\ ratified = FirstPlan
    /\ amends = NoP
    /\ ratifiedBase = NoP
    /\ head = FirstHead
    /\ prLock = ratified
    /\ scope = "one"
    /\ stops = 0
    /\ failure = "none"
    /\ rebuiltAfter = "none"
    /\ gate = [h \in Heads |-> IF h = FirstHead THEN "pending" ELSE "none"]
    /\ (state = "merged" => mergedBy = "other" /\ sourceActor \in Directors)
    /\ (state = "pr_open" => sourceActor = NoActor)
    /\ base = IF state = "merged" THEN ratified ELSE NoP
    /\ source' =
        [history |-> SourceHistory, isPlan |-> mode # "not-plan",
         owes |-> mode # "non-owing", pr |-> SourcePR,
         decision |-> FirstDecision,
         claim |-> <<IF mode = "wrong-plan"
                       THEN CHOOSE p \in Proposals : p # ratified
                       ELSE ratified, SourcePR, FirstDecision>>,
         candidatePR |-> IF mode = "forged-pr"
                          THEN CHOOSE p \in PullRequests : p \notin {SourcePR, FollowPR}
                          ELSE FollowPR]
    /\ follow' = [EmptyFollow EXCEPT !.head = FirstHead,
                  !.paths = AllowedFollowupArtifacts, !.tied = TRUE]
    /\ UNCHANGED <<ordinaryVars, sourceActor, permit, audit>>

\* Name the observed merger explicitly so the explorer can enumerate this
\* environment input without changing the ordinary OthersMerge operation.
OthersMergeAs(a) == OthersMerge /\ sourceActor' = a

OrdinaryStep ==
    /\ ~FollowupActive
    /\ source' = NoSource
    /\ follow' = EmptyFollow
    /\ permit' = NoPermit
    /\ audit' = NoAudit
    /\ ((OrdinaryNext /\ UNCHANGED sourceActor)
        \/ (\E a \in Actors : OthersMergeAs(a)))

\* CI and scope are independent observations. A push retains their old revision
\* stamps, making stale evidence visible rather than turning it into a pass.
ObserveFollowupCI(result) ==
    /\ follow.ciHead # follow.head
    /\ CorePhase(follow.core) # "merged"
    /\ follow' = [follow EXCEPT !.ciHead = follow.head, !.gate = result[1],
                  !.decisions = result[2], !.graph = result[3]]
    /\ audit' = NoAudit
    /\ UNCHANGED permit

ObserveFollowupScope ==
    /\ follow.scopeHead # follow.head
    /\ CorePhase(follow.core) # "merged"
    /\ follow' = [follow EXCEPT !.scopeHead = follow.head]
    /\ audit' = NoAudit
    /\ UNCHANGED permit

PushFollowup(kind) ==
    /\ FollowupActive
    /\ CorePhase(follow.core) # "merged"
    /\ follow.head = FirstHead
    /\ follow' = [follow EXCEPT !.head = OtherHead,
                  !.paths = IF kind = "broad"
                              THEN AllowedFollowupArtifacts \cup {"project-code"}
                              ELSE AllowedFollowupArtifacts,
                  !.tied = kind # "unrelated",
                  !.target = IF kind = "untrusted"
                               THEN CHOOSE b \in BaseRefs \ TrustedBases : TRUE
                               ELSE CHOOSE b \in TrustedBases : TRUE]
    /\ audit' = NoAudit
    /\ UNCHANGED permit

\* This is the trusted environment boundary: it delivers the amended Workflow's
\* acceptance for this entire context, not just for the context-free tuple.
\* The tuple table is a bounded overapproximation of possible core outputs.
\* External Workflow acceptance is delivered for an exact request context.
\* The finite relation is an abstract test fixture, not a local classifier.
\* In particular, the protocol never manufactures acceptance from a branch name.
ReceiveWorkflowStep(t, a, authenticated) ==
    /\ permit = NoPermit
    /\ t \in WorkflowSteps
    /\ t[1] = follow.core
    /\ IF t[2] \in {"retry", "resume"}
          THEN a \in Actors
          ELSE a = NoActor /\ ~authenticated
    /\ permit' = [transition |-> t, context |-> FollowContext(a, authenticated)]
    /\ audit' = NoAudit
    /\ UNCHANGED follow

DiscardWorkflowReceipt ==
    /\ permit # NoPermit
    /\ permit' = NoPermit
    /\ audit' = NoAudit
    /\ UNCHANGED follow

MatchingWorkflowReceipt ==
    /\ permit # NoPermit
    /\ audit = NoAudit
    /\ permit.transition \in WorkflowSteps
    /\ permit.transition[1] = follow.core
    /\ permit.context = FollowContext(permit.context.actor, permit.context.authenticated)

FollowupStepAuthority ==
    /\ EligibleSource
    /\ VerifiedAssociation
    /\ IF permit.transition[2] = "open"
          THEN ~FollowupActive
          ELSE FollowupActive

FollowupStepPolicy ==
    LET t == permit.transition IN
    /\ CoreRepairs(t[4]) \in 0..2
    /\ (CorePhase(t[4]) = "closed" => CoreHold(t[4]))
    /\ (CoreHold(t[1]) /\ ~CoreHold(t[4]) => t[2] = "resume")
    /\ (t[2] \in {"retry", "resume"} =>
           permit.context.actor \in Directors /\ permit.context.authenticated)
    /\ (t[2] = "retry" =>
           /\ CorePhase(t[1]) = "blocked"
           /\ CoreAllowance(t[4]) # CoreAllowance(t[1])
           /\ CoreRepairs(t[4]) = 0)
    /\ (t[2] = "resume" => CorePhase(t[1]) = "closed" /\ follow.pr # NoPR)
    /\ (CorePhase(t[4]) = "merged" =>
           /\ t[2] = "merge"
           /\ ~CoreHold(t[1])
           /\ ~CoreHold(t[4])
           /\ FollowupScopeOK
           /\ FollowupChecksOK
           /\ follow.target \in TrustedBases)

\* Commit precisely the accepted before/after core state and effect outcome.
\* Outcome names are opaque to protocol policy; a failed effect is not promoted
\* to a successful merge, repair or resume by this adapter.
RecordFollowupStep(t, nextPR, nextMergedHead) ==
    /\ follow' = [follow EXCEPT !.core = t[4], !.pr = nextPR,
                  !.mergedHead = nextMergedHead, !.outcome = t[3],
                  !.resumed = @ \/ (t[2] = "resume" /\ CorePhase(t[4]) = "open")]
    /\ audit' = [receipt |-> permit,
                  request |-> FollowContext(permit.context.actor, permit.context.authenticated),
                  beforePR |-> follow.pr, afterPR |-> nextPR,
                  beforeCore |-> follow.core, afterCore |-> t[4],
                  operation |-> t[2], outcome |-> t[3]]
    /\ UNCHANGED permit

ApplyFollowupStep ==
    /\ MatchingWorkflowReceipt
    /\ FollowupStepAuthority
    /\ FollowupStepPolicy
    /\ RecordFollowupStep(permit.transition,
           IF permit.transition[2] = "open" THEN source.candidatePR ELSE follow.pr,
           IF CorePhase(permit.transition[4]) = "merged" THEN follow.head ELSE follow.mergedHead)

FollowupStep ==
    /\ source # NoSource
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>
    /\ (\/ \E r \in {<<"pass", TRUE, TRUE>>, <<"skip", TRUE, TRUE>>,
                      <<"fail", TRUE, TRUE>>, <<"pass", FALSE, TRUE>>,
                      <<"skip", TRUE, FALSE>>} : ObserveFollowupCI(r)
        \/ ObserveFollowupScope
        \/ \E k \in {"narrow", "broad", "unrelated", "untrusted"} : PushFollowup(k)
        \/ \E t \in WorkflowSteps, a \in Actors \cup {NoActor}, ok \in BOOLEAN :
                ReceiveWorkflowStep(t, a, ok)
        \/ DiscardWorkflowReceipt
        \/ ApplyFollowupStep)

Wait == UNCHANGED vars

Next ==
    \/ OrdinaryStep
    \/ \E mode \in {"eligible", "non-owing", "not-plan", "forged-pr", "wrong-plan"} : ObserveSource(mode)
    \/ FollowupStep
    \/ Wait

\* Counterexamples are local authorization mistakes, not alternate core policy.
FollowupOpenUnmergedSource ==
    /\ source # NoSource
    /\ source.history.state # "merged"
    /\ MatchingWorkflowReceipt
    /\ permit.transition[2] = "open"
    /\ VerifiedAssociation
    /\ RecordFollowupStep(permit.transition, source.candidatePR, NoHead)
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>

FollowupOpenNonOwingSource ==
    /\ source # NoSource
    /\ source.history.state = "merged"
    /\ ~source.owes
    /\ MatchingWorkflowReceipt
    /\ permit.transition[2] = "open"
    /\ VerifiedAssociation
    /\ RecordFollowupStep(permit.transition, source.candidatePR, NoHead)
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>

FollowupOpenForgedAssociation ==
    /\ source # NoSource
    /\ EligibleSource
    /\ ~VerifiedAssociation
    /\ MatchingWorkflowReceipt
    /\ permit.transition[2] = "open"
    /\ RecordFollowupStep(permit.transition, source.candidatePR, NoHead)
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>

FollowupMergeBroaderScope ==
    /\ source # NoSource
    /\ MatchingWorkflowReceipt
    /\ FollowupStepAuthority
    /\ permit.transition[2] = "merge"
    /\ follow.scopeHead = follow.head
    /\ ~(follow.paths \subseteq AllowedFollowupArtifacts)
    /\ follow.ciHead = follow.head
    /\ follow.decisions /\ follow.graph /\ follow.gate = "pass"
    /\ follow.target \in TrustedBases
    /\ RecordFollowupStep(permit.transition, follow.pr, follow.head)
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>

FollowupMergeStaleCI ==
    /\ source # NoSource
    /\ MatchingWorkflowReceipt
    /\ FollowupStepAuthority
    /\ permit.transition[2] = "merge"
    /\ FollowupScopeOK
    /\ follow.ciHead # follow.head
    /\ follow.decisions /\ follow.graph /\ follow.gate \in {"pass", "skip"}
    /\ follow.target \in TrustedBases
    /\ RecordFollowupStep(permit.transition, follow.pr, follow.head)
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>

FollowupMergeOldHead ==
    /\ source # NoSource
    /\ MatchingWorkflowReceipt
    /\ FollowupStepAuthority
    /\ FollowupStepPolicy
    /\ permit.transition[2] = "merge"
    /\ follow.head = OtherHead
    /\ RecordFollowupStep(permit.transition, follow.pr, FirstHead)
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>

FollowupRetryUnauthorized ==
    /\ source # NoSource
    /\ MatchingWorkflowReceipt
    /\ FollowupStepAuthority
    /\ permit.transition[2] \in {"retry", "resume"}
    /\ ~(permit.context.actor \in Directors /\ permit.context.authenticated)
    /\ RecordFollowupStep(permit.transition, follow.pr, follow.mergedHead)
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>

FollowupApplyStaleAcceptance ==
    /\ source # NoSource
    /\ permit # NoPermit
    /\ permit.transition[2] = "merge"
    /\ permit.transition[1] = follow.core
    /\ permit.context.head # follow.head
    /\ FollowupStepAuthority
    /\ FollowupStepPolicy
    /\ RecordFollowupStep(permit.transition, follow.pr, follow.head)
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>

FollowupMergeWithoutGraphCheck ==
    /\ source # NoSource
    /\ MatchingWorkflowReceipt
    /\ FollowupStepAuthority
    /\ permit.transition[2] = "merge"
    /\ FollowupScopeOK
    /\ follow.ciHead = follow.head
    /\ follow.decisions /\ ~follow.graph /\ follow.gate = "skip"
    /\ follow.target \in TrustedBases
    /\ RecordFollowupStep(permit.transition, follow.pr, follow.head)
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>

FollowupResumeReplacesPR ==
    /\ source # NoSource
    /\ MatchingWorkflowReceipt
    /\ FollowupStepAuthority
    /\ FollowupStepPolicy
    /\ permit.transition[2] = "resume"
    /\ \E pr \in PullRequests \ {SourcePR, follow.pr} :
           RecordFollowupStep(permit.transition, pr, follow.mergedHead)
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>

FollowupChangesEffectOutcome ==
    /\ source # NoSource
    /\ MatchingWorkflowReceipt
    /\ FollowupStepAuthority
    /\ FollowupStepPolicy
    /\ permit.transition[2] = "effect"
    /\ \E outcome \in CoreOutcomes \ {permit.transition[3]} :
           RecordFollowupStep(<<permit.transition[1], permit.transition[2], outcome, permit.transition[4]>>,
                              follow.pr, follow.mergedHead)
    /\ UNCHANGED <<ordinaryVars, sourceActor, source>>

Spec == Init /\ [][Next]_vars
====
