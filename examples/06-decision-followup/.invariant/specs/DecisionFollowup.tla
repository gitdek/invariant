---- MODULE DecisionFollowup ----
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Followups, SpecLines, ReadmePassages, RepairsPerRequest

\* One instance tracks one plan's obligation; conflicts abstract other merges.
\* SpecLines and ReadmePassages are the text written by that merged plan.
VARIABLES planMerged, decisionOwed, door, executed, phase, draft,
    followup, created, bundle, ci, ciFresh, conflict, repairsUsed, hold, event

vars == <<planMerged, decisionOwed, door, executed, phase, draft,
    followup, created, bundle, ci, ciFresh, conflict, repairsUsed, hold, event>>

NoFollowup == "none"

DecisionId == "newDecision"

Sources == {"plan", "mergedChange", "review"}

AllowedFiles == {"decisions/", "SPEC.md", "README"}

Phases == {"waiting", "drafting", "draftFailed", "readyToCreate",
    "creating", "createFailed", "open", "needsRepair", "blocked", "closed", "merged"}

EventKinds == {"initial", "planMerge", "draftStart", "draftRetry", "draftFailure",
    "draftSuccess", "createStart", "createRetry", "createFailure", "create",
    "check", "conflict", "repair", "block", "close", "retry", "resume", "merge"}

Event(kind) == [kind |-> kind, previousRepairs |-> repairsUsed]

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

NeedsRepair == ci = "failed" \/ conflict

ReadyToMerge == phase = "open" /\ ci = "passed" /\ ciFresh /\ ~conflict

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
    /\ ci \in {"none", "pending", "passed", "failed"}
    /\ ciFresh \in BOOLEAN
    /\ conflict \in BOOLEAN
    /\ repairsUsed \in Nat
    /\ hold \in {"none", "exhausted", "closed"}
    /\ event \in [kind : EventKinds, previousRepairs : Nat]

MergeRequiresCurrentCI == phase = "merged" => ci = "passed" /\ ciFresh

OneFollowup == Cardinality(created) <= 1

RepairAllowance == repairsUsed <= RepairsPerRequest

HumanHoldRespected ==
    /\ (hold = "closed" <=> phase = "closed")
    /\ (hold = "exhausted" <=> phase = "blocked")

OnlyOwedMergedPlans == phase # "waiting" => planMerged /\ decisionOwed

StableFollowupIdentity ==
    /\ (followup = NoFollowup <=> created = {})
    /\ (followup # NoFollowup => created = {followup})
    /\ (phase \in {"open", "needsRepair", "blocked", "closed", "merged"}
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

CurrentCIResults == ci \in {"passed", "failed"} => ciFresh

EachRepairStartsFreshCI == event.kind = "repair" =>
    ci = "pending" /\ ~ciFresh /\ repairsUsed = event.previousRepairs + 1

RequestsRenewAllowance == event.kind \in {"retry", "resume"} => repairsUsed = 0

AllowanceAccounting ==
    IF event.kind = "repair" THEN repairsUsed = event.previousRepairs + 1
    ELSE IF event.kind \in {"retry", "resume"} THEN repairsUsed = 0
    ELSE repairsUsed = event.previousRepairs

ExhaustionIsBlocked == phase = "blocked" =>
    NeedsRepair /\ repairsUsed = RepairsPerRequest

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

ClosedFollowupWaits == phase = "closed" /\ hold = "closed"

ResumeRenewsUnusedRepairs == event.kind = "resume"
    /\ event.previousRepairs = 1 /\ repairsUsed = 0

ResumeRenewsExhaustedRepairs == event.kind = "resume"
    /\ event.previousRepairs = RepairsPerRequest /\ repairsUsed = 0

DraftProgress ==
    (planMerged /\ decisionOwed /\ phase \in {"waiting", "draftFailed"})
        ~> (phase = "drafting")

CreationProgress == phase \in {"readyToCreate", "createFailed"} ~> (phase = "creating")

RepairProgress == (phase = "needsRepair" /\ repairsUsed < RepairsPerRequest)
    ~> (phase \in {"open", "closed"})

BlockingProgress == (phase = "needsRepair" /\ repairsUsed = RepairsPerRequest)
    ~> (phase \in {"blocked", "closed"})

MergeProgress == ReadyToMerge ~> (phase \in {"merged", "closed", "needsRepair"})

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
    /\ event = [kind |-> "initial", previousRepairs |-> 0]

PlanMerges(owed, planDoor, wasExecuted) ==
    /\ ~planMerged
    /\ planMerged' = TRUE
    /\ decisionOwed' = owed
    /\ door' = planDoor
    /\ executed' = wasExecuted
    /\ event' = Event("planMerge")
    /\ UNCHANGED <<phase, draft, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed, hold>>

StartDraft ==
    /\ planMerged /\ decisionOwed
    /\ phase \in {"waiting", "draftFailed"}
    /\ phase' = "drafting"
    /\ event' = Event(IF phase = "draftFailed" THEN "draftRetry" ELSE "draftStart")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed, hold>>

DraftFails ==
    /\ phase = "drafting"
    /\ phase' = "draftFailed"
    /\ event' = Event("draftFailure")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed, hold>>

DraftSucceeds(needsOwner) ==
    /\ phase = "drafting"
    /\ draft' = DraftValue(needsOwner)
    /\ phase' = "readyToCreate"
    /\ event' = Event("draftSuccess")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed, hold>>

StartCreation ==
    /\ phase \in {"readyToCreate", "createFailed"}
    /\ phase' = "creating"
    /\ event' = Event(IF phase = "createFailed" THEN "createRetry" ELSE "createStart")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed, hold>>

CreationFails ==
    /\ phase = "creating"
    /\ phase' = "createFailed"
    /\ event' = Event("createFailure")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed, hold>>

CreationSucceeds(f) ==
    /\ phase = "creating"
    /\ created = {}
    /\ followup' = f
    /\ created' = {f}
    /\ bundle' = CompleteBundleValue
    /\ phase' = "open"
    /\ ci' = "pending"
    /\ ciFresh' = FALSE
    /\ conflict' = FALSE
    /\ event' = Event("create")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, repairsUsed, hold>>

CheckCompletes(result, hasConflict) ==
    \* An external result for the current contents, never an older CI run.
    /\ phase \in {"open", "closed"}
    /\ ci = "pending"
    /\ ci' = result
    /\ conflict' = hasConflict
    /\ ciFresh' = TRUE
    /\ phase' = IF phase = "closed" THEN "closed"
        ELSE IF ci' = "passed" /\ ~conflict' THEN "open" ELSE "needsRepair"
    /\ event' = Event("check")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, repairsUsed, hold>>

ConflictAppears ==
    /\ phase \in {"open", "closed"}
    /\ ci \in {"pending", "passed"} /\ ~conflict
    /\ conflict' = TRUE
    /\ phase' = IF phase = "closed" THEN "closed" ELSE "needsRepair"
    /\ event' = Event("conflict")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, repairsUsed, hold>>

Repair ==
    /\ phase = "needsRepair" /\ hold = "none"
    /\ repairsUsed < RepairsPerRequest
    /\ repairsUsed' = repairsUsed + 1
    /\ phase' = "open"
    /\ ci' = "pending"
    /\ ciFresh' = FALSE
    /\ conflict' = FALSE
    /\ event' = Event("repair")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, hold>>

Block ==
    /\ phase = "needsRepair"
    /\ repairsUsed = RepairsPerRequest
    /\ phase' = "blocked"
    /\ hold' = "exhausted"
    /\ event' = Event("block")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed>>

PersonCloses ==
    /\ phase \in {"open", "needsRepair", "blocked"}
    /\ phase' = "closed"
    /\ hold' = "closed"
    /\ event' = Event("close")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed>>

PersonRetries ==
    /\ phase = "blocked"
    /\ phase' = "needsRepair"
    /\ hold' = "none"
    /\ repairsUsed' = 0
    /\ event' = Event("retry")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, conflict>>

PersonResumes ==
    /\ phase = "closed"
    /\ phase' = IF NeedsRepair THEN "needsRepair" ELSE "open"
    /\ hold' = "none"
    /\ repairsUsed' = 0
    /\ event' = Event("resume")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, conflict>>

AutoMerge ==
    /\ ReadyToMerge
    /\ hold = "none"
    /\ phase' = "merged"
    /\ event' = Event("merge")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed, hold>>

Done ==
    /\ phase = "merged" \/ (planMerged /\ ~decisionOwed)
    /\ UNCHANGED vars

Next == (\E owed \in BOOLEAN, planDoor \in {"one-way", "two-way"}, wasExecuted \in BOOLEAN :
        PlanMerges(owed, planDoor, wasExecuted))
    \/ StartDraft \/ DraftFails
    \/ (\E needsOwner \in BOOLEAN : DraftSucceeds(needsOwner))
    \/ StartCreation \/ CreationFails
    \/ (\E f \in Followups : CreationSucceeds(f))
    \/ (\E result \in {"passed", "failed"}, hasConflict \in BOOLEAN :
        CheckCompletes(result, hasConflict))
    \/ ConflictAppears \/ Repair \/ Block \/ PersonCloses \/ PersonRetries
    \/ PersonResumes \/ AutoMerge \/ Done

DraftFairness == WF_vars(StartDraft)

CreationFairness == WF_vars(StartCreation)

RepairFairness == WF_vars(Repair)

BlockingFairness == WF_vars(Block)

MergeFairness == WF_vars(AutoMerge)

MergeBeforeCI ==
    /\ phase = "open" /\ ci = "pending"
    /\ phase' = "merged"
    /\ event' = Event("merge")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed, hold>>

CreateDuplicate ==
    /\ followup # NoFollowup
    /\ \E f \in Followups \ created : created' = created \cup {f}
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, draft, followup,
        bundle, ci, ciFresh, conflict, repairsUsed, hold, event>>

RepairPastAllowance ==
    /\ phase = "needsRepair" /\ repairsUsed = RepairsPerRequest
    /\ repairsUsed' = repairsUsed + 1
    /\ phase' = "open"
    /\ ci' = "pending"
    /\ ciFresh' = FALSE
    /\ conflict' = FALSE
    /\ event' = Event("repair")
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, hold>>

ResumeWithoutPerson ==
    /\ phase = "closed"
    /\ phase' = IF NeedsRepair THEN "needsRepair" ELSE "open"
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, draft, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed, hold, event>>

DecideOneWay ==
    /\ phase = "readyToCreate" /\ door = "one-way"
    /\ draft' = [draft EXCEPT !.status = "decided", !.decidedBy = "agent"]
    /\ UNCHANGED <<planMerged, decisionOwed, door, executed, phase, followup, created,
        bundle, ci, ciFresh, conflict, repairsUsed, hold, event>>

Spec == Init /\ [][Next]_vars
====
