---- MODULE DecisionFollowup ----
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Followups, SpecLines, ReadmePassages, RepairsPerRequest,
    MaxRenewals, MaxHeadMoves, Exercises

\* One plan and one stable PR. Revision numbers stand for distinct immutable heads.
\* The finite exploration permits MaxRenewals human requests and MaxHeadMoves
\* independent head changes; automatic work is never stopped by an operation cap.
\* Exercises partition representative environments to avoid multiplying independent
\* histories: repairs, a single conflict, a revision with no human renewal, and
\* merge refusal/closure. Every policy outcome has a witness; this finite draft
\* does not enumerate every cross-exercise interleaving. Other classifications
\* use a simple successful path, since their evidence and artifacts are immutable.
\* Observations denote current ordered remote facts; notification delivery and
\* durable recovery protocol composition are outside this amendment.
\* Effect results are environmental facts awaiting delivery or reconciliation.
\* Executing an effect and accepting its outcome are separate steps, including
\* when the reply is lost. No fairness assumes that GitHub or a person responds.
VARIABLES planMerged, decisionOwed, door, executed, phase, draft,
    followup, created, bundle, ci, ciFresh, conflict, repairsUsed, hold, event,
    head, ciHead, repairError, failureCause, effect, resumePending, renewals, headMoves, exercise

vars == <<planMerged, decisionOwed, door, executed, phase, draft,
    followup, created, bundle, ci, ciFresh, conflict, repairsUsed, hold, event,
    head, ciHead, repairError, failureCause, effect, resumePending, renewals, headMoves, exercise>>

NoFollowup == "none"

DecisionId == "newDecision"

Sources == {"plan", "mergedChange", "review"}

AllowedFiles == {"decisions/", "SPEC.md", "README"}

Phases == {"waiting", "drafting", "draftFailed", "readyToCreate",
    "creating", "createFailed", "open", "needsRepair", "repairing", "merging",
    "blocked", "closed", "mergeRefused", "merged", "externallyMerged"}

Holds == {"none", "exhausted", "closed", "mergeRefused"}

CIValues == {"none", "pending", "passed", "failed"}

RepairErrors == {"none", "failed", "noChange"}

Causes == {"ci", "conflict", "execution"}

EffectKinds == {"none", "repair", "merge"}

EffectStages == {"idle", "authorized", "pending", "lost", "settled"}

EffectResults == {"none", "published", "failed", "noChange", "merged", "refused"}

EffectType == [id : Nat, kind : EffectKinds, head : Nat,
    stage : EffectStages, runs : Nat, result : EffectResults, cause : SUBSET Causes]

EmptyEffect == [id |-> 0, kind |-> "none", head |-> 0,
    stage |-> "idle", runs |-> 0, result |-> "none", cause |-> {}]

Token(id, kind, revision) == [id |-> id, kind |-> kind, head |-> revision]

NoToken == Token(0, "none", 0)

RepairOutcomes == {"repairPublished", "repairSuperseded", "repairFailed", "repairNoChange"}

MergeOutcomes == {"merge", "mergeRefusal"}

OutcomeEvents == RepairOutcomes \cup MergeOutcomes

EffectEvents == OutcomeEvents \cup {"repair", "mergeAuthorize", "executeRepair",
    "executeMerge", "lostReply", "externalMerge", "rejectOutcome", "rejectCI"}

ObservationEvents == RepairOutcomes \cup {"mergeRefusal", "externalMerge",
    "revision", "rejectCI", "rejectOutcome"}

EventKinds == {"initial", "planMerge", "draftStart", "draftRetry", "draftFailure",
    "draftSuccess", "createStart", "createRetry", "createFailure", "create",
    "requestCI", "check", "conflict", "repair", "block", "close", "retry", "resume",
    "deferResume", "mergeAuthorize", "executeRepair", "executeMerge", "lostReply",
    "repairPublished", "repairSuperseded", "repairFailed", "repairNoChange",
    "merge", "mergeRefusal", "externalMerge", "revision", "rejectCI", "rejectOutcome"}

\* Transition evidence is normalized when a field is irrelevant, to keep the
\* bounded graph small; it is not an observation supplied to the workflow.
Active(e) == e.stage \in {"authorized", "pending", "lost"}

PriorEffect(kind) ==
    IF kind \in {"repair", "mergeAuthorize"} THEN
        [EmptyEffect EXCEPT !.id = effect.id,
            !.stage = IF Active(effect) THEN "authorized" ELSE "idle"]
    ELSE IF kind = "resume" THEN
        [EmptyEffect EXCEPT !.stage = IF Active(effect) THEN "authorized" ELSE "idle"]
    ELSE IF kind \in EffectEvents THEN effect ELSE EmptyEffect

EventWithToken(kind, token) ==
    [kind |-> kind, previousRepairs |-> repairsUsed, previousHold |-> hold,
     previousHead |-> IF kind \in ObservationEvents THEN head ELSE 0,
     previousCI |-> IF kind \in ObservationEvents THEN ci ELSE "none",
     previousCIHead |-> IF kind \in ObservationEvents THEN ciHead ELSE 0,
     previousFresh |-> IF kind \in ObservationEvents THEN ciFresh ELSE FALSE,
     previousConflict |-> IF kind \in ObservationEvents THEN conflict ELSE FALSE,
     previousEffect |-> PriorEffect(kind),
     previousResume |-> IF kind \in {"resume", "deferResume", "externalMerge", "merge"}
        THEN resumePending ELSE FALSE,
     token |-> token]

Event(kind) == EventWithToken(kind, NoToken)

EmptyDraft == [status |-> "none", decidedBy |-> "none",
    needsOwner |-> TRUE, sources |-> {}]

DraftValue(needsOwner) ==
    [status |-> IF door = "two-way" /\ executed /\ ~needsOwner
        THEN "decided" ELSE "proposed",
     decidedBy |-> IF door = "two-way" /\ executed /\ ~needsOwner
        THEN "agent" ELSE "none",
     needsOwner |-> needsOwner, sources |-> Sources]

EmptyBundle ==
    [command |-> "none", journal |-> "none", logRow |-> "none",
     specCitations |-> [s \in SpecLines |-> "none"],
     readmeCitations |-> [r \in ReadmePassages |-> "none"],
     rolledUp |-> FALSE, files |-> {}]

CompleteBundleValue ==
    [command |-> "invariant decisions decide", journal |-> DecisionId, logRow |-> DecisionId,
     specCitations |-> [s \in SpecLines |-> DecisionId],
     readmeCitations |-> [r \in ReadmePassages |-> DecisionId],
     rolledUp |-> TRUE, files |-> AllowedFiles]

Terminal == phase \in {"merged", "externallyMerged"}

Live == followup # NoFollowup /\ ~Terminal

RemoteUnmerged == effect.result # "merged"

NeedsRepair == ci = "failed" \/ conflict \/ repairError # "none"

CurrentCauses == (IF ci = "failed" THEN {"ci"} ELSE {})
    \cup (IF conflict THEN {"conflict"} ELSE {})
    \cup (IF repairError # "none" THEN {"execution"} ELSE {})

PhaseFor(h, e, check, collision, err) ==
    IF h = "closed" THEN "closed"
    ELSE IF h = "exhausted" THEN "blocked"
    ELSE IF h = "mergeRefused" THEN "mergeRefused"
    ELSE IF Active(e) THEN IF e.kind = "repair" THEN "repairing" ELSE "merging"
    ELSE IF check = "failed" \/ collision \/ err # "none" THEN "needsRepair"
    ELSE "open"

ReadyToMerge == phase = "open" /\ ci = "passed" /\ ciFresh /\ ~conflict
    /\ ciHead = head /\ hold = "none" /\ ~Active(effect) /\ repairError = "none"

NewEffect(kind) == [id |-> effect.id + 1, kind |-> kind, head |-> head,
    stage |-> "authorized", runs |-> 0, result |-> "none",
    cause |-> IF kind = "repair" THEN CurrentCauses ELSE {}]

MatchingOutcome(id, kind, revision) ==
    /\ Active(effect) /\ effect.stage \in {"pending", "lost"}
    /\ id = effect.id /\ kind = effect.kind /\ revision = effect.head

SameObservation ==
    /\ head = event.previousHead
    /\ ci = event.previousCI /\ ciHead = event.previousCIHead
    /\ ciFresh = event.previousFresh /\ conflict = event.previousConflict

TypeOK ==
    /\ planMerged \in BOOLEAN
    /\ decisionOwed \in BOOLEAN
    /\ door \in {"one-way", "two-way"}
    /\ executed \in BOOLEAN
    /\ phase \in Phases
    /\ draft \in [status : {"none", "proposed", "decided"},
        decidedBy : {"none", "agent"}, needsOwner : BOOLEAN, sources : SUBSET Sources]
    /\ followup \in Followups \cup {NoFollowup}
    /\ created \subseteq Followups
    /\ bundle \in [command : {"none", "invariant decisions decide"},
        journal : {"none", DecisionId}, logRow : {"none", DecisionId},
        specCitations : [SpecLines -> {"none", DecisionId}],
        readmeCitations : [ReadmePassages -> {"none", DecisionId}],
        rolledUp : BOOLEAN, files : SUBSET (AllowedFiles \cup {"code"})]
    /\ ci \in CIValues /\ ciFresh \in BOOLEAN /\ conflict \in BOOLEAN
    /\ repairsUsed \in Nat /\ hold \in Holds
    /\ head \in Nat /\ ciHead \in Nat
    /\ repairError \in RepairErrors /\ failureCause \subseteq Causes
    /\ effect \in EffectType /\ resumePending \in BOOLEAN
    /\ renewals \in 0..MaxRenewals /\ headMoves \in 0..MaxHeadMoves
    /\ exercise \in Exercises \cup {"simple"}
    /\ event \in [kind : EventKinds, previousRepairs : Nat, previousHold : Holds,
        previousHead : Nat, previousCI : CIValues, previousCIHead : Nat,
        previousFresh : BOOLEAN, previousConflict : BOOLEAN,
        previousEffect : EffectType, previousResume : BOOLEAN,
        token : [id : Nat, kind : EffectKinds, head : Nat]]

MergeRequiresCurrentCI == phase = "merged" => ci = "passed" /\ ciFresh

OneFollowup == Cardinality(created) <= 1

RepairAllowance == repairsUsed <= RepairsPerRequest

HumanHoldRespected ==
    /\ (hold = "closed" <=> phase = "closed")
    /\ (hold = "exhausted" <=> phase = "blocked")
    /\ (hold = "mergeRefused" <=> phase = "mergeRefused")
    /\ (event.previousHold # "none"
        /\ event.kind \notin {"retry", "resume", "merge", "externalMerge"}
        => hold = IF event.kind = "close" THEN "closed" ELSE event.previousHold)

OnlyOwedMergedPlans == phase # "waiting" => planMerged /\ decisionOwed

StableFollowupIdentity ==
    /\ (followup = NoFollowup <=> created = {})
    /\ (followup # NoFollowup => created = {followup})
    /\ (phase \in {"open", "needsRepair", "repairing", "merging", "blocked",
        "closed", "mergeRefused", "merged", "externallyMerged"}
        <=> followup # NoFollowup)

DraftUsesEvidence == draft.status # "none" => draft.sources = Sources

DecisionClassification ==
    draft.status # "none" =>
        /\ (draft.status = "decided" <=>
            door = "two-way" /\ executed /\ ~draft.needsOwner)
        /\ (draft.decidedBy = "agent" <=> draft.status = "decided")

FollowupHasDecision == followup # NoFollowup => draft.status \in {"decided", "proposed"}

DecisionCommandUsed == followup # NoFollowup => bundle.command = "invariant decisions decide"

JournalAndLogAgree == followup # NoFollowup =>
    bundle.journal = DecisionId /\ bundle.logRow = DecisionId

CitationsCoverChangedText == followup # NoFollowup =>
    /\ \A s \in SpecLines : bundle.specCitations[s] = DecisionId
    /\ \A r \in ReadmePassages : bundle.readmeCitations[r] = DecisionId

SpecIsRolledUp == followup # NoFollowup => bundle.rolledUp

FollowupScope == bundle.files \subseteq AllowedFiles

MergeWithoutConflict == phase = "merged" => ~conflict

CurrentCIResults == ci \in {"passed", "failed"} => ciFresh /\ ciHead = head

EachRepairStartsFreshCI == event.kind = "repairPublished" =>
    /\ head = event.previousHead + 1 /\ ci = "none" /\ ~ciFresh /\ ciHead = 0
    /\ repairsUsed = event.previousRepairs

RequestsRenewAllowance == event.kind \in {"retry", "resume"} => repairsUsed = 0

AllowanceAccounting ==
    IF event.kind = "repair" THEN repairsUsed = event.previousRepairs + 1
    ELSE IF event.kind \in {"retry", "resume"} THEN repairsUsed = 0
    ELSE repairsUsed = event.previousRepairs

ExhaustionIsBlocked == phase = "blocked" => repairsUsed = RepairsPerRequest

RepairAuthorizationCharged == event.kind = "repair" =>
    /\ repairsUsed = event.previousRepairs + 1
    /\ effect.kind = "repair" /\ effect.stage = "authorized"
    /\ effect.runs = 0 /\ effect.result = "none" /\ effect.cause # {}

RepairFailureIsSeparate == event.kind \in {"repairFailed", "repairNoChange"} =>
    /\ SameObservation
    /\ repairError = IF event.kind = "repairFailed" THEN "failed" ELSE "noChange"
    /\ failureCause = event.previousEffect.cause /\ failureCause # {}
    /\ effect.stage = "settled"

RepairFailureRemainsActionable ==
    repairError # "none" /\ Live /\ hold = "none" /\ ~Active(effect)
        => phase = "needsRepair"

RevisionInvalidatesCI == event.kind = "revision" =>
    /\ head = event.previousHead + 1 /\ ci = "none" /\ ciHead = 0 /\ ~ciFresh
    /\ repairsUsed = event.previousRepairs /\ hold = event.previousHold

TruthfulFollowupState == Live =>
    phase = PhaseFor(hold, effect, ci, conflict, repairError)

StableOperationIdentity ==
    /\ (Active(effect) => followup # NoFollowup /\ effect.id > 0 /\ effect.head > 0)
    /\ (event.kind \in {"repair", "mergeAuthorize"} =>
        /\ ~Active(event.previousEffect)
        /\ effect.id = event.previousEffect.id + 1 /\ effect.head = head)
    /\ (event.kind \in EffectEvents \ {"repair", "mergeAuthorize"} =>
        /\ effect.id = event.previousEffect.id
        /\ effect.kind = event.previousEffect.kind /\ effect.head = event.previousEffect.head)

EffectRunsOnce ==
    /\ effect.runs <= 1
    /\ (effect.stage = "authorized" => effect.runs = 0 /\ effect.result = "none")
    /\ (effect.stage \in {"pending", "lost"} => effect.runs = 1 /\ effect.result # "none")
    /\ (event.kind \in {"executeRepair", "executeMerge"} =>
        /\ event.previousEffect.stage = "authorized" /\ event.previousEffect.runs = 0
        /\ effect.runs = 1)
    /\ (event.kind = "lostReply" =>
        /\ effect.stage = "lost" /\ effect.runs = event.previousEffect.runs
        /\ effect.result = event.previousEffect.result)

OutcomeGuards == event.kind \in OutcomeEvents =>
    /\ event.previousEffect.stage \in {"pending", "lost"}
    /\ event.token = Token(event.previousEffect.id,
        event.previousEffect.kind, event.previousEffect.head)
    /\ effect.stage = "settled" /\ effect.runs = event.previousEffect.runs
    /\ effect.result = event.previousEffect.result
    /\ (event.kind \in RepairOutcomes => event.previousEffect.kind = "repair")
    /\ (event.kind \in MergeOutcomes => event.previousEffect.kind = "merge")
    /\ (event.kind = "repairPublished" => effect.result = "published"
        /\ event.previousHead = effect.head)
    /\ (event.kind = "repairSuperseded" => effect.result = "published"
        /\ event.previousHead # effect.head /\ SameObservation)
    /\ (event.kind = "repairFailed" => effect.result = "failed")
    /\ (event.kind = "repairNoChange" => effect.result = "noChange")
    /\ (event.kind = "merge" => effect.result = "merged")
    /\ (event.kind = "mergeRefusal" => effect.result = "refused")

RejectedOutcomesPreserveState == event.kind \in {"rejectCI", "rejectOutcome"} =>
    /\ SameObservation /\ repairsUsed = event.previousRepairs
    /\ hold = event.previousHold
    /\ effect = event.previousEffect

MergeAuthorizationIsPending == event.kind = "mergeAuthorize" =>
    /\ ~Terminal /\ effect.kind = "merge" /\ effect.stage = "authorized"
    /\ effect.head = head /\ ci = "passed" /\ ciFresh /\ ciHead = head
    /\ ~conflict /\ hold = "none"

FactoryMergeConfirmed == phase = "merged" =>
    /\ effect.kind = "merge" /\ effect.stage = "settled"
    /\ effect.result = "merged" /\ effect.runs = 1 /\ effect.head = head

MergeRefusalIsTruthful == event.kind = "mergeRefusal" =>
    /\ ~Terminal /\ SameObservation
    /\ (event.previousHold = "closed" => hold = "closed")
    /\ (event.previousHold = "none" =>
        hold = IF head = effect.head /\ ci = "passed" /\ ciFresh /\ ~conflict
            THEN "mergeRefused" ELSE "none")

DeferredResumeSafety ==
    /\ (resumePending => hold = "closed" /\ Live)
    /\ (event.kind = "deferResume" =>
        /\ resumePending /\ hold = "closed" /\ Active(effect)
        /\ repairsUsed = event.previousRepairs)
    /\ (event.kind = "resume" =>
        /\ event.previousHold = "closed" /\ ~Active(event.previousEffect)
        /\ ~Terminal /\ ~resumePending /\ hold = "none")
    /\ (event.kind = "retry" => event.previousHold \in {"exhausted", "mergeRefused"})

ExternalSettlementHonest == event.kind = "externalMerge" =>
    /\ phase = "externallyMerged" /\ SameObservation
    /\ repairsUsed = event.previousRepairs /\ ~resumePending /\ hold = "none"

CIPendingWasRequested == ci = "pending" => ciHead = head /\ ~ciFresh

DecidedDecisionMerged == phase = "merged" /\ draft.status = "decided"

OneWayProposedMerged == phase = "merged" /\ door = "one-way" /\ draft.status = "proposed"

OwnerInputProposedMerged == phase = "merged" /\ door = "two-way"
    /\ executed /\ draft.needsOwner /\ draft.status = "proposed"

UnexecutedProposedMerged == phase = "merged" /\ door = "two-way"
    /\ ~executed /\ draft.status = "proposed"

NoDecisionWork == planMerged /\ ~decisionOwed /\ created = {} /\ phase = "waiting"

DraftRetried == event.kind = "draftRetry" /\ phase = "drafting"

CreationRetried == event.kind = "createRetry" /\ phase = "creating"

RepairCanMerge == phase = "merged" /\ repairsUsed > 0

RepairsExhausted == phase = "blocked" /\ repairsUsed = RepairsPerRequest

PersonRenewsExhaustedWork == event.kind = "retry" /\ repairsUsed = 0
    /\ event.previousHold = "exhausted"

ClosedFollowupWaits == phase = "closed" /\ hold = "closed"

ResumeRenewsUnusedRepairs == event.kind = "resume"
    /\ event.previousRepairs = 1 /\ repairsUsed = 0

ResumeRenewsExhaustedRepairs == event.kind = "resume"
    /\ event.previousRepairs = RepairsPerRequest /\ repairsUsed = 0

FailedRepairRetainsConflict == event.kind = "repairFailed"
    /\ conflict /\ ci = "passed" /\ repairError = "failed" /\ repairsUsed = 1

NoChangeRetainsCIFailure == event.kind = "repairNoChange"
    /\ ci = "failed" /\ repairError = "noChange" /\ repairsUsed = 1

FailedRepairsExhaust == phase = "blocked" /\ repairError = "failed"
    /\ repairsUsed = RepairsPerRequest

NoChangeRepairsExhaust == phase = "blocked" /\ repairError = "noChange"
    /\ repairsUsed = RepairsPerRequest

FailureAllowanceRenewed == event.kind = "retry" /\ repairError # "none"
    /\ repairsUsed = 0 /\ event.previousRepairs = RepairsPerRequest

PublishedRepairInvalidatesCI == event.kind = "repairPublished"
    /\ ci = "none" /\ ~ciFresh /\ repairsUsed > 0

HeadMovesAfterPassingCI == event.kind = "revision" /\ event.previousCI = "passed"
    /\ ci = "none" /\ ~ciFresh

RevisionKeepsExhaustionHold == event.kind = "revision"
    /\ hold = "exhausted" /\ repairsUsed = RepairsPerRequest

MergeAuthorizationPending == event.kind = "mergeAuthorize" /\ phase = "merging"

ClosedMergeRefused == event.kind = "mergeRefusal" /\ hold = "closed"

ConflictedMergeRefused == event.kind = "mergeRefusal" /\ conflict
    /\ phase = "needsRepair" /\ ci = "passed"

MovedHeadMergeRefused == event.kind = "mergeRefusal" /\ head # effect.head
    /\ ci = "none" /\ phase = "open"

CleanMergeRefused == event.kind = "mergeRefusal" /\ hold = "mergeRefused"
    /\ head = effect.head /\ ci = "passed" /\ ~conflict

PersonRetriesRefusedMerge == event.kind = "retry"
    /\ event.previousHold = "mergeRefused" /\ repairsUsed = 0 /\ hold = "none"

LostRepairReconciled == event.kind = "repairFailed"
    /\ event.previousEffect.stage = "lost" /\ effect.runs = 1 /\ repairsUsed = 1

LostMergeReconciled == event.kind = "merge"
    /\ event.previousEffect.stage = "lost" /\ phase = "merged"

DeferredResumeWaits == resumePending /\ Active(effect) /\ hold = "closed"
    /\ repairsUsed > 0

DeferredResumeApplied == event.kind = "resume" /\ event.previousResume
    /\ repairsUsed = 0 /\ hold = "none"

DeferredResumeSettledExternally == event.kind = "externalMerge"
    /\ event.previousResume /\ ~resumePending /\ phase = "externallyMerged"
    /\ repairsUsed = event.previousRepairs

ExternalMergeWithoutPassingCI == phase = "externallyMerged" /\ ci # "passed"

OldOperationOutcomeRejected == event.kind = "rejectOutcome"
    /\ effect.id > 1 /\ event.token.id < effect.id /\ Active(effect)

OldOutcomeAfterRenewalRejected == event.kind = "rejectOutcome"
    /\ renewals = 1 /\ effect.id > 1 /\ event.token.id < effect.id /\ Active(effect)

RevisionKeepsClosureHold == event.kind = "revision" /\ hold = "closed"

OldCIOutcomeRejected == event.kind = "rejectCI"
    /\ event.token.head < head /\ ci = "none"

SupersededRepairReconciled == event.kind = "repairSuperseded"
    /\ head # effect.head /\ effect.stage = "settled"

DraftProgress ==
    (planMerged /\ decisionOwed /\ phase \in {"waiting", "draftFailed"})
        ~> (phase = "drafting")

CreationProgress == phase \in {"readyToCreate", "createFailed"} ~> (phase = "creating")

RepairProgress == (phase = "needsRepair" /\ repairsUsed < RepairsPerRequest)
    ~> (Active(effect) \/ hold # "none" \/ Terminal \/ ~NeedsRepair)

BlockingProgress == (phase = "needsRepair" /\ repairsUsed = RepairsPerRequest)
    ~> (phase \in {"blocked", "closed", "externallyMerged"} \/ ~NeedsRepair)

MergeProgress == ReadyToMerge
    ~> (Active(effect) \/ hold # "none" \/ Terminal \/ ~ReadyToMerge)

DeferredResumeProgress == (resumePending /\ ~Active(effect)) ~> ~resumePending

CIRequestProgress == (Live /\ hold = "none" /\ ~Active(effect)
    /\ ~NeedsRepair /\ ci = "none")
    ~> (ci # "none" \/ Active(effect) \/ hold # "none" \/ Terminal \/ NeedsRepair)

Init ==
    /\ planMerged = FALSE
    /\ decisionOwed = FALSE
    /\ door = "one-way"
    /\ executed = FALSE
    /\ phase = "waiting"
    /\ draft = EmptyDraft
    /\ followup = NoFollowup
    /\ created = {}
    /\ bundle = EmptyBundle
    /\ ci = "none"
    /\ ciFresh = FALSE
    /\ conflict = FALSE
    /\ repairsUsed = 0
    /\ hold = "none"
    /\ head = 0
    /\ ciHead = 0
    /\ repairError = "none"
    /\ failureCause = {}
    /\ effect = EmptyEffect
    /\ resumePending = FALSE
    /\ renewals = 0
    /\ headMoves = 0
    /\ exercise = "simple"
    /\ event = [kind |-> "initial", previousRepairs |-> 0, previousHold |-> "none",
        previousHead |-> 0, previousCI |-> "none", previousCIHead |-> 0,
        previousFresh |-> FALSE, previousConflict |-> FALSE,
        previousEffect |-> EmptyEffect, previousResume |-> FALSE, token |-> NoToken]

PlanMerges(owed, doorKind, wasExecuted) ==
    /\ ~planMerged
    /\ owed \in BOOLEAN
    /\ doorKind \in {"one-way", "two-way"}
    /\ wasExecuted \in BOOLEAN
    /\ planMerged' = TRUE
    /\ decisionOwed' = owed
    /\ door' = doorKind
    /\ executed' = wasExecuted
    /\ event' = Event("planMerge")
    /\ UNCHANGED <<phase, draft, followup, created, bundle, ci, ciFresh, conflict, repairsUsed, hold,
        head, ciHead, repairError, failureCause, effect, resumePending, renewals, headMoves, exercise>>

StartDraft ==
    /\ planMerged /\ decisionOwed
    /\ phase \in {"waiting", "draftFailed"}
    /\ phase' = "drafting"
    /\ event' = Event(IF phase = "draftFailed" THEN "draftRetry" ELSE "draftStart")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

DraftFails ==
    /\ phase = "drafting"
    /\ phase' = "draftFailed"
    /\ event' = Event("draftFailure")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

DraftSucceeds(needsOwner, chosenExercise) ==
    /\ phase = "drafting"
    /\ needsOwner \in BOOLEAN
    /\ chosenExercise \in IF door = "two-way" /\ executed /\ ~needsOwner
        THEN Exercises ELSE {"simple"}
    /\ exercise' = chosenExercise
    /\ draft' = DraftValue(needsOwner)
    /\ phase' = "readyToCreate"
    /\ event' = Event("draftSuccess")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, followup, created, bundle, ci, ciFresh,
        conflict, repairsUsed, hold, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves>>

StartCreation ==
    /\ phase \in {"readyToCreate", "createFailed"}
    /\ phase' = "creating"
    /\ event' = Event(IF phase = "createFailed" THEN "createRetry" ELSE "createStart")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

CreationFails ==
    /\ phase = "creating"
    /\ phase' = "createFailed"
    /\ event' = Event("createFailure")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

CreationSucceeds(f) ==
    /\ phase = "creating"
    /\ created = {}
    /\ f \in Followups
    /\ followup' = f
    /\ created' = {f}
    /\ bundle' = CompleteBundleValue
    /\ phase' = "open"
    /\ head' = 1
    /\ ci' = "pending"
    /\ ciHead' = 1
    /\ ciFresh' = FALSE
    /\ conflict' = FALSE
    /\ event' = Event("create")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, repairsUsed, hold, repairError,
        failureCause, effect, resumePending, renewals, headMoves, exercise>>

RequestCI ==
    /\ Live /\ RemoteUnmerged
    /\ hold = "none" /\ ~Active(effect) /\ ~NeedsRepair
    /\ ci = "none"
    /\ ci' = "pending"
    /\ ciHead' = head
    /\ ciFresh' = FALSE
    /\ event' = Event("requestCI")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, draft, followup, created, bundle,
        conflict, repairsUsed, hold, head, repairError, failureCause, effect, resumePending,
        renewals, headMoves, exercise>>

CheckCompletes(revision, result) ==
    /\ result = "passed" \/ exercise \in {"repairs", "revision"}
    /\ Live /\ RemoteUnmerged
    /\ ci = "pending" /\ ciHead = head /\ revision = head
    /\ result \in {"passed", "failed"}
    /\ ci' = result
    /\ ciFresh' = TRUE
    /\ phase' = PhaseFor(hold, effect, result, conflict, repairError)
    /\ event' = Event("check")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, conflict,
        repairsUsed, hold, head, ciHead, repairError, failureCause, effect, resumePending,
        renewals, headMoves, exercise>>

ObserveRevision ==
    /\ exercise = "revision"
    /\ Live /\ RemoteUnmerged
    /\ headMoves < MaxHeadMoves
    /\ head' = head + 1
    /\ headMoves' = headMoves + 1
    /\ ci' = "none"
    /\ ciHead' = 0
    /\ ciFresh' = FALSE
    /\ phase' = PhaseFor(hold, effect, "none", conflict, repairError)
    /\ event' = Event("revision")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, conflict,
        repairsUsed, hold, repairError, failureCause, effect, resumePending, renewals, exercise>>

ConflictAppears ==
    /\ exercise = "conflict" /\ head = 1 /\ ci = "passed"
    /\ Live /\ RemoteUnmerged /\ ~conflict
    /\ conflict' = TRUE
    /\ phase' = PhaseFor(hold, effect, ci, TRUE, repairError)
    /\ event' = Event("conflict")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, repairsUsed, hold, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

Repair ==
    /\ phase = "needsRepair" /\ hold = "none" /\ ~Active(effect)
    /\ repairsUsed < RepairsPerRequest
    /\ repairsUsed' = repairsUsed + 1
    /\ effect' = NewEffect("repair")
    /\ phase' = "repairing"
    /\ event' = Event("repair")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, hold, head, ciHead, repairError, failureCause, resumePending,
        renewals, headMoves, exercise>>

ExecuteRepair(result) ==
    /\ Live /\ effect.kind = "repair" /\ effect.stage = "authorized"
    /\ result \in {"published", "failed", "noChange"}
    /\ effect' = [effect EXCEPT !.stage = "pending", !.runs = @ + 1, !.result = result]
    /\ event' = Event("executeRepair")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause,
        resumePending, renewals, headMoves, exercise>>

AutoMerge ==
    /\ ReadyToMerge
    /\ effect' = NewEffect("merge")
    /\ phase' = "merging"
    /\ event' = Event("mergeAuthorize")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause,
        resumePending, renewals, headMoves, exercise>>

ExecuteMerge(result) ==
    /\ result = "merged" \/ exercise \in {"conflict", "revision", "merge"}
    /\ Live /\ effect.kind = "merge" /\ effect.stage = "authorized"
    /\ result \in {"merged", "refused"}
    /\ result = "merged" => hold = "none" /\ head = effect.head
        /\ ci = "passed" /\ ciFresh /\ ciHead = head /\ ~conflict
    /\ effect' = [effect EXCEPT !.stage = "pending", !.runs = @ + 1, !.result = result]
    /\ event' = Event("executeMerge")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause,
        resumePending, renewals, headMoves, exercise>>

LoseReply ==
    /\ exercise # "simple"
    /\ Live /\ effect.stage = "pending"
    /\ effect' = [effect EXCEPT !.stage = "lost"]
    /\ event' = Event("lostReply")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause,
        resumePending, renewals, headMoves, exercise>>

RepairOutcome(id, revision, result) ==
    /\ Live /\ RemoteUnmerged
    /\ MatchingOutcome(id, "repair", revision)
    /\ result = effect.result /\ result \in {"published", "failed", "noChange"}
    /\ effect' = [effect EXCEPT !.stage = "settled"]
    /\ head' = IF result = "published" /\ head = effect.head THEN head + 1 ELSE head
    /\ ci' = IF result = "published" /\ head = effect.head THEN "none" ELSE ci
    /\ ciHead' = IF result = "published" /\ head = effect.head THEN 0 ELSE ciHead
    /\ ciFresh' = IF result = "published" /\ head = effect.head THEN FALSE ELSE ciFresh
    /\ conflict' = IF result = "published" /\ head = effect.head THEN FALSE ELSE conflict
    /\ repairError' = IF result \in {"failed", "noChange"} THEN result
        ELSE IF head = effect.head THEN "none" ELSE repairError
    /\ failureCause' = IF result \in {"failed", "noChange"} THEN effect.cause
        ELSE IF head = effect.head THEN {} ELSE failureCause
    /\ phase' = PhaseFor(hold, effect', ci', conflict', repairError')
    /\ event' = EventWithToken(IF result = "failed" THEN "repairFailed"
        ELSE IF result = "noChange" THEN "repairNoChange"
        ELSE IF head = effect.head THEN "repairPublished" ELSE "repairSuperseded",
        Token(id, "repair", revision))
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle,
        repairsUsed, hold, resumePending, renewals, headMoves, exercise>>

MergeOutcome(id, revision, result) ==
    /\ Live
    /\ MatchingOutcome(id, "merge", revision)
    /\ result = effect.result /\ result \in {"merged", "refused"}
    /\ effect' = [effect EXCEPT !.stage = "settled"]
    /\ hold' = IF result = "merged" THEN "none"
        ELSE IF hold = "none" /\ head = effect.head /\ ci = "passed" /\ ciFresh /\ ~conflict
            THEN "mergeRefused" ELSE hold
    /\ resumePending' = IF result = "merged" THEN FALSE ELSE resumePending
    /\ phase' = IF result = "merged" THEN "merged"
        ELSE PhaseFor(hold', effect', ci, conflict, repairError)
    /\ event' = EventWithToken(IF result = "merged" THEN "merge" ELSE "mergeRefusal",
        Token(id, "merge", revision))
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, head, ciHead, repairError, failureCause, renewals,
        headMoves, exercise>>

Block ==
    /\ phase = "needsRepair" /\ ~Active(effect)
    /\ repairsUsed = RepairsPerRequest
    /\ phase' = "blocked"
    /\ hold' = "exhausted"
    /\ event' = Event("block")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

PersonCloses ==
    /\ exercise # "simple"
    /\ Live /\ RemoteUnmerged /\ hold # "closed"
    /\ phase' = "closed"
    /\ hold' = "closed"
    /\ event' = Event("close")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

PersonRetries ==
    /\ exercise \in {"repairs", "conflict", "merge"}
    /\ Live /\ hold \in {"exhausted", "mergeRefused"} /\ ~Active(effect)
    /\ renewals < MaxRenewals
    /\ hold' = "none"
    /\ repairsUsed' = 0
    /\ renewals' = renewals + 1
    /\ phase' = PhaseFor("none", effect, ci, conflict, repairError)
    /\ event' = Event("retry")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, head, ciHead, repairError, failureCause, effect, resumePending,
        headMoves, exercise>>

PersonResumes ==
    /\ exercise \in {"repairs", "conflict", "merge"}
    /\ Live /\ hold = "closed" /\ ~Active(effect) /\ ~resumePending
    /\ renewals < MaxRenewals
    /\ hold' = "none"
    /\ repairsUsed' = 0
    /\ renewals' = renewals + 1
    /\ phase' = PhaseFor("none", effect, ci, conflict, repairError)
    /\ event' = Event("resume")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, head, ciHead, repairError, failureCause, effect, resumePending,
        headMoves, exercise>>

DeferResume ==
    /\ exercise \in {"repairs", "conflict", "merge"}
    /\ Live /\ hold = "closed" /\ Active(effect) /\ ~resumePending
    /\ renewals < MaxRenewals
    /\ resumePending' = TRUE
    /\ event' = Event("deferResume")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause, effect,
        renewals, headMoves, exercise>>

ApplyDeferredResume ==
    /\ Live /\ hold = "closed" /\ ~Active(effect) /\ resumePending
    /\ renewals < MaxRenewals
    /\ hold' = "none"
    /\ repairsUsed' = 0
    /\ renewals' = renewals + 1
    /\ resumePending' = FALSE
    /\ phase' = PhaseFor("none", effect, ci, conflict, repairError)
    /\ event' = Event("resume")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, head, ciHead, repairError, failureCause, effect, headMoves, exercise>>

ObserveExternalMerge ==
    /\ exercise # "simple"
    /\ Live /\ RemoteUnmerged
    /\ phase' = "externallyMerged"
    /\ hold' = "none"
    /\ resumePending' = FALSE
    /\ effect' = IF Active(effect) THEN [effect EXCEPT !.stage = "settled"] ELSE effect
    /\ event' = Event("externalMerge")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, head, ciHead, repairError, failureCause, renewals,
        headMoves, exercise>>

RejectCI(revision) ==
    /\ exercise # "simple"
    /\ Live /\ revision \in 0..head
    /\ revision # head \/ ci # "pending"
    /\ event' = EventWithToken("rejectCI", Token(0, "none", revision))
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

RejectOutcome(id, kind, revision) ==
    /\ exercise # "simple"
    /\ Live /\ effect.id > 0
    /\ id \in 0..effect.id /\ kind \in {"repair", "merge"} /\ revision \in 0..head
    /\ ~MatchingOutcome(id, kind, revision)
    /\ event' = EventWithToken("rejectOutcome", Token(id, kind, revision))
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

Done ==
    /\ Terminal \/ (planMerged /\ ~decisionOwed)
    /\ UNCHANGED vars

\* Representatives cover all four classification outcomes; either PR identity
\* is interchangeable, while CreateDuplicate still has a second identity available.
\* Rejection probes use zero when no predecessor exists. Such probes are already
\* disabled before a head or operation exists; their arguments remain in Nat.
Next == PlanMerges(FALSE, "one-way", FALSE)
    \/ PlanMerges(TRUE, "one-way", TRUE)
    \/ PlanMerges(TRUE, "two-way", TRUE)
    \/ PlanMerges(TRUE, "two-way", FALSE)
    \/ StartDraft \/ DraftFails
    \/ DraftSucceeds(TRUE, "simple")
    \/ (\E chosenExercise \in Exercises : DraftSucceeds(FALSE, chosenExercise))
    \/ StartCreation \/ CreationFails
    \/ CreationSucceeds(CHOOSE f \in Followups : TRUE)
    \/ RequestCI
    \/ (\E result \in {"passed", "failed"} : CheckCompletes(head, result))
    \/ ObserveRevision \/ ConflictAppears \/ Repair
    \/ (\E result \in {"published", "failed", "noChange"} : ExecuteRepair(result))
    \/ AutoMerge \/ (\E result \in {"merged", "refused"} : ExecuteMerge(result))
    \/ LoseReply
    \/ RepairOutcome(effect.id, effect.head, effect.result)
    \/ MergeOutcome(effect.id, effect.head, effect.result)
    \/ Block \/ PersonCloses \/ PersonRetries \/ PersonResumes
    \/ DeferResume \/ ApplyDeferredResume \/ ObserveExternalMerge
    \/ RejectCI(IF head = 0 THEN 0 ELSE head - 1) \/ RejectCI(head)
    \/ RejectOutcome(IF effect.id = 0 THEN 0 ELSE effect.id - 1, effect.kind, effect.head)
    \/ RejectOutcome(effect.id, IF effect.kind = "repair" THEN "merge" ELSE "repair", effect.head)
    \/ RejectOutcome(effect.id, effect.kind, IF effect.head = 0 THEN 0 ELSE effect.head - 1)
    \/ RejectOutcome(effect.id, effect.kind, effect.head)
    \/ Done

DraftFairness == WF_vars(StartDraft)

CreationFairness == WF_vars(StartCreation)

RepairFairness == WF_vars(Repair)

BlockingFairness == WF_vars(Block)

MergeFairness == WF_vars(AutoMerge)

DeferredResumeFairness == WF_vars(ApplyDeferredResume)

CIRequestFairness == WF_vars(RequestCI)

MergeBeforeCI ==
    /\ phase = "open" /\ ci = "pending"
    /\ phase' = "merged"
    /\ event' = Event("merge")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

CreateDuplicate ==
    /\ followup # NoFollowup
    /\ \E f \in Followups \ created : created' = created \cup {f}
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, draft, followup, bundle, ci, ciFresh,
        conflict, repairsUsed, hold, event, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

RepairPastAllowance ==
    /\ phase = "needsRepair" /\ repairsUsed = RepairsPerRequest
    /\ repairsUsed' = repairsUsed + 1
    /\ phase' = "repairing"
    /\ effect' = NewEffect("repair")
    /\ event' = Event("repair")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, hold, head, ciHead, repairError, failureCause, resumePending,
        renewals, headMoves, exercise>>

ResumeWithoutPerson ==
    /\ phase = "closed"
    /\ phase' = IF NeedsRepair THEN "needsRepair" ELSE "open"
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, event, head, ciHead, repairError, failureCause,
        effect, resumePending, renewals, headMoves, exercise>>

DecideOneWay ==
    /\ phase = "readyToCreate" /\ door = "one-way"
    /\ draft' = [draft EXCEPT !.status = "decided", !.decidedBy = "agent"]
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, event, head, ciHead, repairError, failureCause,
        effect, resumePending, renewals, headMoves, exercise>>

MergeAuthorizationAsCompletion ==
    /\ phase = "merging" /\ effect.stage = "authorized"
    /\ phase' = "merged"
    /\ event' = Event("mergeAuthorize")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause, effect,
        resumePending, renewals, headMoves, exercise>>

ExecuteRepairTwice ==
    /\ Live /\ effect.kind = "repair" /\ effect.stage \in {"pending", "lost"}
    /\ effect' = [effect EXCEPT !.runs = @ + 1]
    /\ event' = Event("executeRepair")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, head, ciHead, repairError, failureCause,
        resumePending, renewals, headMoves, exercise>>

AcceptStaleRepairOutcomeStep(id, revision, result) ==
    /\ Live /\ RemoteUnmerged /\ effect.id > 1
    /\ MatchingOutcome(id, "repair", revision)
    /\ effect.result = "failed" /\ result = "failed"
    /\ effect' = [effect EXCEPT !.stage = "settled"]
    /\ head' = IF result = "published" /\ head = effect.head THEN head + 1 ELSE head
    /\ ci' = IF result = "published" /\ head = effect.head THEN "none" ELSE ci
    /\ ciHead' = IF result = "published" /\ head = effect.head THEN 0 ELSE ciHead
    /\ ciFresh' = IF result = "published" /\ head = effect.head THEN FALSE ELSE ciFresh
    /\ conflict' = IF result = "published" /\ head = effect.head THEN FALSE ELSE conflict
    /\ repairError' = IF result \in {"failed", "noChange"} THEN result
        ELSE IF head = effect.head THEN "none" ELSE repairError
    /\ failureCause' = IF result \in {"failed", "noChange"} THEN effect.cause
        ELSE IF head = effect.head THEN {} ELSE failureCause
    /\ phase' = PhaseFor(hold, effect', ci', conflict', repairError')
    /\ event' = EventWithToken("repairFailed", Token(effect.id - 1, "repair", effect.head))
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle,
        repairsUsed, hold, resumePending, renewals, headMoves, exercise>>

AcceptStaleRepairOutcome == AcceptStaleRepairOutcomeStep(effect.id, effect.head, "failed")

FabricateCIFailureStep(id, revision, result) ==
    /\ Live /\ RemoteUnmerged
    /\ MatchingOutcome(id, "repair", revision)
    /\ result = "failed" /\ effect.result = result
    /\ ci = "passed" /\ conflict
    /\ effect' = [effect EXCEPT !.stage = "settled"]
    /\ head' = IF result = "published" /\ head = effect.head THEN head + 1 ELSE head
    /\ ci' = "failed"
    /\ ciHead' = IF result = "published" /\ head = effect.head THEN 0 ELSE ciHead
    /\ ciFresh' = IF result = "published" /\ head = effect.head THEN FALSE ELSE ciFresh
    /\ conflict' = IF result = "published" /\ head = effect.head THEN FALSE ELSE conflict
    /\ repairError' = IF result \in {"failed", "noChange"} THEN result
        ELSE IF head = effect.head THEN "none" ELSE repairError
    /\ failureCause' = IF result \in {"failed", "noChange"} THEN effect.cause
        ELSE IF head = effect.head THEN {} ELSE failureCause
    /\ phase' = PhaseFor(hold, effect', ci', conflict', repairError')
    /\ event' = EventWithToken(IF result = "failed" THEN "repairFailed"
        ELSE IF result = "noChange" THEN "repairNoChange"
        ELSE IF head = effect.head THEN "repairPublished" ELSE "repairSuperseded",
        Token(id, "repair", revision))
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle,
        repairsUsed, hold, resumePending, renewals, headMoves, exercise>>

FabricateCIFailure == FabricateCIFailureStep(effect.id, effect.head, "failed")

NoChangeWaitsForCIStep(id, revision, result) ==
    /\ Live /\ RemoteUnmerged
    /\ MatchingOutcome(id, "repair", revision)
    /\ result = "noChange" /\ effect.result = result
    /\ ci = "failed"
    /\ effect' = [effect EXCEPT !.stage = "settled"]
    /\ head' = IF result = "published" /\ head = effect.head THEN head + 1 ELSE head
    /\ ci' = "pending"
    /\ ciHead' = head
    /\ ciFresh' = FALSE
    /\ conflict' = IF result = "published" /\ head = effect.head THEN FALSE ELSE conflict
    /\ repairError' = "none"
    /\ failureCause' = IF result \in {"failed", "noChange"} THEN effect.cause
        ELSE IF head = effect.head THEN {} ELSE failureCause
    /\ phase' = PhaseFor(hold, effect', ci', conflict', repairError')
    /\ event' = EventWithToken(IF result = "failed" THEN "repairFailed"
        ELSE IF result = "noChange" THEN "repairNoChange"
        ELSE IF head = effect.head THEN "repairPublished" ELSE "repairSuperseded",
        Token(id, "repair", revision))
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle,
        repairsUsed, hold, resumePending, renewals, headMoves, exercise>>

NoChangeWaitsForCI == NoChangeWaitsForCIStep(effect.id, effect.head, "noChange")

KeepCIOnNewHead ==
    /\ Live /\ RemoteUnmerged /\ ci = "passed"
    /\ headMoves < MaxHeadMoves
    /\ head' = head + 1
    /\ headMoves' = headMoves + 1
    /\ event' = Event("revision")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, hold, ciHead, repairError, failureCause, effect,
        resumePending, renewals, exercise>>

OutcomeLiftsClosureStep(id, revision, result) ==
    /\ Live /\ RemoteUnmerged /\ hold = "closed"
    /\ MatchingOutcome(id, "repair", revision)
    /\ result = "failed" /\ effect.result = result
    /\ effect' = [effect EXCEPT !.stage = "settled"]
    /\ head' = IF result = "published" /\ head = effect.head THEN head + 1 ELSE head
    /\ ci' = IF result = "published" /\ head = effect.head THEN "none" ELSE ci
    /\ ciHead' = IF result = "published" /\ head = effect.head THEN 0 ELSE ciHead
    /\ ciFresh' = IF result = "published" /\ head = effect.head THEN FALSE ELSE ciFresh
    /\ conflict' = IF result = "published" /\ head = effect.head THEN FALSE ELSE conflict
    /\ repairError' = IF result \in {"failed", "noChange"} THEN result
        ELSE IF head = effect.head THEN "none" ELSE repairError
    /\ failureCause' = IF result \in {"failed", "noChange"} THEN effect.cause
        ELSE IF head = effect.head THEN {} ELSE failureCause
    /\ phase' = PhaseFor("none", effect', ci', conflict', repairError')
    /\ event' = EventWithToken(IF result = "failed" THEN "repairFailed"
        ELSE IF result = "noChange" THEN "repairNoChange"
        ELSE IF head = effect.head THEN "repairPublished" ELSE "repairSuperseded",
        Token(id, "repair", revision))
    /\ hold' = "none"
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle,
        repairsUsed, resumePending, renewals, headMoves, exercise>>

OutcomeLiftsClosure == OutcomeLiftsClosureStep(effect.id, effect.head, "failed")

RefusalRetriesAutomaticallyStep(id, revision, result) ==
    /\ Live /\ hold = "none" /\ head = effect.head
    /\ ci = "passed" /\ ciFresh /\ ~conflict
    /\ MatchingOutcome(id, "merge", revision)
    /\ result = "refused" /\ effect.result = result
    /\ effect' = [effect EXCEPT !.stage = "settled"]
    /\ hold' = "none"
    /\ resumePending' = IF result = "merged" THEN FALSE ELSE resumePending
    /\ phase' = "open"
    /\ event' = EventWithToken(IF result = "merged" THEN "merge" ELSE "mergeRefusal",
        Token(id, "merge", revision))
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, repairsUsed, head, ciHead, repairError, failureCause, renewals,
        headMoves, exercise>>

RefusalRetriesAutomatically == RefusalRetriesAutomaticallyStep(effect.id, effect.head, "refused")

ResumeBeforeReconciliation ==
    /\ Live /\ hold = "closed" /\ Active(effect) /\ resumePending
    /\ hold' = "none"
    /\ repairsUsed' = 0
    /\ resumePending' = FALSE
    /\ phase' = PhaseFor("none", effect, ci, conflict, repairError)
    /\ event' = Event("resume")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, ci,
        ciFresh, conflict, head, ciHead, repairError, failureCause, effect, renewals,
        headMoves, exercise>>

RevisionRenewsAllowance ==
    /\ Live /\ RemoteUnmerged /\ repairsUsed > 0
    /\ headMoves < MaxHeadMoves
    /\ head' = head + 1
    /\ headMoves' = headMoves + 1
    /\ ci' = "none"
    /\ ciHead' = 0
    /\ ciFresh' = FALSE
    /\ repairsUsed' = 0
    /\ phase' = PhaseFor(hold, effect, "none", conflict, repairError)
    /\ event' = Event("revision")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, conflict,
        hold, repairError, failureCause, effect, resumePending, renewals, exercise>>

ExternalMergeInventsCI ==
    /\ Live /\ RemoteUnmerged /\ ci # "passed"
    /\ phase' = "externallyMerged"
    /\ hold' = "none"
    /\ resumePending' = FALSE
    /\ effect' = IF Active(effect) THEN [effect EXCEPT !.stage = "settled"] ELSE effect
    /\ ci' = "passed"
    /\ ciHead' = head
    /\ ciFresh' = TRUE
    /\ event' = Event("externalMerge")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created, bundle, conflict,
        repairsUsed, head, repairError, failureCause, renewals, headMoves, exercise>>

Spec == Init /\ [][Next]_vars
====
