---- MODULE WatcherRecovery ----
EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Watchers, MaxCrashes, MaxFollowCrashes, MaxFollowOps, MaxDraftFailures, MaxCreateFailures,
    MaxPublicationRefusals, MaxLostCreations, MaxLostPublications, MaxLostMerges,
    MaxCloses, MaxHeadMoves, MaxCIFailures, MaxUnknownMerges, MaxRefusedMerges, MaxFollowEvents

VARIABLES lane, fence, follow,
    given,       \* commands a writer has given on the issue
    posts,       \* number of factory posts answering each command or event
    retried,     \* a writer has given a retry, so the latest build is a retry's
    retryPosts,  \* factory posts answering the latest retry
    buildPosts,  \* factory posts answering the latest build: a pull request, or a stopped run
    runs,        \* each agent run as recorded: "none", "recorded" or "done"
    runStarts,   \* how many times each agent run was started (history)
    ratPushed,   \* the issue's branch holds the ratification
    codePushed,  \* the latest build's code is pushed
    prs,         \* pull requests opened for the latest build (history)
    pr,          \* the latest build's pull request: "none", "draft", "ready", "closed" or "merged"
    gate,        \* CI's gate on that pull request's head: "none" or "pass"
    mergeable,   \* whether GitHub can merge it
    merges,      \* number of merges made
    pred,        \* the draft the latest retry replaces: "none", "draft", "retired", "closed" or "merged"
    intent,      \* the latest retry's record that it replaces that draft is stored
    watched,     \* a writer's retry watches the latest build's failed draft again
    watchPosts,  \* factory posts answering that retry
    marked,      \* the factory marked the latest build's draft ready (history)
    lease,       \* the watcher holding the repository's lease, or "nobody"
    up,          \* whether each watcher is running
    inRun,       \* the agent run each watcher is in the middle of, or "none"
    crashes,     \* crashes and stalls so far
    unleased     \* watchers that ever took an effect without holding the lease (history)

vars == <<given, posts, retried, retryPosts, buildPosts, runs, runStarts, ratPushed,
          codePushed, prs, pr, gate, mergeable, merges, pred, intent, watched, watchPosts,
          marked, lease, up, inRun, crashes, unleased, lane, fence, follow>>

\* Commands a writer gives once: solve (a draft), ratify and a note. A retry
\* is apart: a writer can give one after each build that stopped or failed,
\* to build again, or to watch the build's failed draft again.
Cmds == {"solve", "ratify", "note"}

\* Everything else the factory answers with one post: the commands, and the merge.
Keys == Cmds \cup {"merge"}

\* Steps that start an agent run: the draft, and the latest build. A retry's
\* build is a new run, which leaves a watcher still in the one before stale.
RunKeys == {"solve", "build"}

NoOne == "nobody"

\* Ordinary commands and a follow-up use separate lanes, avoiding their
\* irrelevant Cartesian product. One initial state starts in the ordinary lane
\* with an inactive follow-up record; ChooseFollowup can select the follow-up
\* lane only from that untouched state. The follow-up environment chooses each
\* run result, response and human action independently when it happens.
\* Counters bound repetition, not scenario selection or action ordering.
\* MaxFollowEvents additionally bounds the total number of exceptional external
\* inputs (including crashes). Every combination and ordering within BOTH the
\* individual limits and this total budget remains enabled at its effect.
\* Follow-up crashes also have their own checking bound; the ordinary bound
\* retains the two lease losses needed to test a returning stale build runner.
\* Successful responses, reconciliation, writer requests and successful CI do
\* not spend this budget. A crash-induced stopped result is not counted twice.
\* MaxFollowOps reserves identities for the successful draft and creation,
\* bounded failures/refusals and crash-stopped runs, four repairs, reopening,
\* and each invalidated merge plus its successor; it is not a policy hold.
\* The checked bound is 3 + MaxFollowEvents + MaxCloses: draft, creation,
\* final merge, one extra operation per exceptional event and one reopen
\* per closure. This reserves successors instead of weakening retry progress.
FOps == 1..MaxFollowOps
FEffectKinds == {"run", "publication", "merge", "reopen", "answer"}
FEffects == (0..MaxFollowOps) \X FEffectKinds
FRecordedOps == 1..follow.op
FRecordedEffects == (0..follow.op) \X FEffectKinds

\* Unrecorded effects have implicit zero intent and dispatch counts.
FIntent(effect) ==
    IF effect \in DOMAIN follow.intents THEN follow.intents[effect] ELSE 0

FDispatch(effect) ==
    IF effect \in DOMAIN follow.dispatches THEN follow.dispatches[effect] ELSE 0

\* Unallocated identities have the dense model's implicit none/zero defaults.
\* Allocation appends contiguous operation records and inserts only actual
\* effect keys. All recorded identities and complete journals remain.
\* The operation tuples are functions on 1..follow.op, exactly as before.
FExtend(existing, added) ==
    [key \in (DOMAIN existing) \cup (DOMAIN added) |->
        IF key \in DOMAIN added THEN added[key] ELSE existing[key]]
FKinds == {"none", "draft", "create", "repair", "merge", "reopen"}
FOutcomes == {"none", "success", "failed", "nochange", "stopped", "invalidated", "unknown", "refused"}
FIdentity == [source |-> "ratified-plan-obligation", branch |-> "followup-branch", pr |-> "followup-pr"]

FSnapshot(op, kind, head, left, round, cause, stage) ==
    [identity |-> FIdentity, op |-> op, kind |-> kind, head |-> head,
     left |-> left, round |-> round, cause |-> cause, stage |-> stage]

FollowInitial ==
    [events |-> 0, draftFailures |-> 0, createFailures |-> 0, phase |-> "idle", next |-> "draft", op |-> 0,
     kinds |-> <<>>, bases |-> <<>>,
     rounds |-> <<>>, starts |-> <<>>,
     raw |-> <<>>, results |-> <<>>,
     resultSnapshots |-> <<>>,
     snapshots |-> <<>>, accepted |-> <<>>, intents |-> [e \in {} |-> 0],
     pending |-> {}, completed |-> {}, cancelled |-> {}, dispatches |-> [e \in {} |-> 0],
     charged |-> {}, left |-> 2, round |-> 0, holds |-> {},
     requested |-> FALSE, consumed |-> 0, barrier |-> 0, answers |-> 0,
     owner |-> NoOne, runFence |-> 0,
     applied |-> TRUE, checks |-> TRUE, actors |-> {},
     publicationRefused |-> {}, externalDispatches |-> <<>>, remotePR |-> "none", knownPR |-> "none", creations |-> 0, reopens |-> 0,
     remoteHead |-> 0, head |-> 0, publications |-> <<>>,
     remoteCI |-> "none", remoteCIHead |-> 0, remoteCanMerge |-> FALSE, remoteMergeHead |-> 0, ci |-> "none", ciHead |-> 0,
     canMerge |-> FALSE, mergeHead |-> 0, merges |-> 0,
     remoteMerge |-> "none", mergeOutcome |-> "none", proof |-> "none",
     status |-> "active", cause |-> "none", lost |-> {},
     closeCount |-> 0, headMoves |-> 0, ciFailures |-> 0, unknownMerges |-> 0, refusedMerges |-> 0,
     externalMerged |-> FALSE]

\* The inactive lane has the same record fields and value types as an active
\* follow-up, so action and fairness comparisons never compare a string with
\* a record. Only the phase differs from the original follow-up initial state.
FollowInactive == [FollowInitial EXCEPT !.phase = "inactive"]

FPhases == {"idle", "record-run", "start-run", "running", "run-result",
            "record-publication", "start-publication", "publication", "watch", "hold",
            "record-merge", "start-merge", "merge-response", "uncertain",
            "record-reopen", "start-reopen", "reopen-response", "done"}

FollowTypeOK ==
    IF lane = "ordinary" THEN follow = FollowInactive ELSE
    /\ follow.events \in 0..MaxFollowEvents
    /\ follow.draftFailures \in 0..MaxDraftFailures
    /\ follow.createFailures \in 0..MaxCreateFailures
    /\ follow.phase \in FPhases
    /\ follow.next \in FKinds
    /\ follow.op \in 0..MaxFollowOps
    /\ follow.kinds \in Seq(FKinds) /\ Len(follow.kinds) = follow.op
    /\ follow.bases \in Seq(Nat) /\ Len(follow.bases) = follow.op
    /\ follow.rounds \in Seq(0..1) /\ Len(follow.rounds) = follow.op
    /\ follow.starts \in Seq(Nat) /\ Len(follow.starts) = follow.op
    /\ follow.raw \in Seq(FOutcomes) /\ Len(follow.raw) = follow.op
    /\ follow.results \in Seq(FOutcomes) /\ Len(follow.results) = follow.op
    /\ follow.resultSnapshots \in Seq(Nat) /\ Len(follow.resultSnapshots) = follow.op
    /\ follow.snapshots \in Seq([identity : {FIdentity}, op : 0..MaxFollowOps,
         kind : FKinds, head : Nat, left : 0..2, round : 0..1,
         cause : FOutcomes \cup {"ci-failed", "closed"}, stage : STRING])
    /\ DOMAIN follow.intents \subseteq FRecordedEffects
    /\ follow.intents \in [DOMAIN follow.intents -> Nat]
    /\ follow.accepted \in Seq([identity : {FIdentity}, op : 0..MaxFollowOps,
         kind : FKinds, head : Nat, left : 0..2, round : 0..1,
         cause : FOutcomes \cup {"ci-failed", "closed"}, stage : STRING])
    /\ follow.cancelled \subseteq FRecordedEffects
    /\ follow.pending \subseteq FRecordedEffects
    /\ follow.completed \subseteq FRecordedEffects
    /\ follow.dispatches \in [DOMAIN follow.intents -> Nat]
    /\ follow.charged \subseteq FRecordedOps
    /\ follow.left \in 0..2
    /\ follow.round \in 0..1
    /\ follow.holds \subseteq Nat
    /\ follow.requested \in BOOLEAN
    /\ follow.consumed \in Nat
    /\ follow.barrier \in 0..MaxFollowOps
    /\ follow.answers \in Nat
    /\ follow.owner \in Watchers \cup {NoOne}
    /\ follow.runFence \in Nat
    /\ follow.applied \in BOOLEAN
    /\ follow.checks \in BOOLEAN
    /\ follow.actors \subseteq Watchers
    /\ follow.publicationRefused \subseteq FRecordedOps
    /\ follow.externalDispatches \in Seq(Nat) /\ Len(follow.externalDispatches) = follow.op
    /\ follow.remotePR \in {"none", "open", "closed", "merged"}
    /\ follow.knownPR \in {"none", "followup-pr"}
    /\ follow.creations \in Nat
    /\ follow.reopens \in Nat
    /\ follow.remoteHead \in Nat
    /\ follow.head \in Nat
    /\ follow.publications \in Seq(Nat) /\ Len(follow.publications) = follow.op
    /\ follow.remoteCI \in {"none", "pass", "fail"}
    /\ follow.remoteCIHead \in Nat
    /\ follow.ci \in {"none", "pass", "fail"}
    /\ follow.ciHead \in Nat
    /\ follow.canMerge \in BOOLEAN
    /\ follow.mergeHead \in Nat
    /\ follow.merges \in Nat
    /\ follow.remoteMerge \in FOutcomes
    /\ follow.mergeOutcome \in FOutcomes
    /\ follow.proof \in {"none", "response", "observation"}
    /\ follow.status \in {"active", "closed", "exhausted", "merged", "refused", "unmergeable"}
    /\ follow.cause \in FOutcomes \cup {"ci-failed", "closed"}
    /\ follow.lost \subseteq FRecordedEffects
    /\ follow.remoteCanMerge \in BOOLEAN
    /\ follow.remoteMergeHead \in Nat
    /\ follow.externalMerged \in BOOLEAN
    /\ follow.closeCount \in 0..MaxCloses
    /\ follow.headMoves \in 0..MaxHeadMoves
    /\ follow.ciFailures \in 0..MaxCIFailures
    /\ follow.unknownMerges \in 0..MaxUnknownMerges
    /\ follow.refusedMerges \in 0..MaxRefusedMerges

\* New safety statements; the ordinary statements below retain their text.
FollowSnapshotsKept ==
    lane = "followup" => (
     follow.snapshots = follow.accepted
        )

FollowOnePR ==
    lane = "followup" => (
     follow.creations <= 1
        )

FollowOperationOnce ==
    lane = "followup" => (
        /\ \A o \in FRecordedOps : follow.starts[o] <= 1
        /\ \A e \in DOMAIN follow.dispatches : FDispatch(e) <= 1
        )

FollowOneMerge ==
    lane = "followup" => (
     follow.merges <= 1
        )

FollowWriterRequired ==
    lane = "followup" => (
        /\ follow.consumed > 0 => follow.requested
        /\ follow.reopens > 0 => follow.consumed > 0
        /\ \A h \in follow.holds, e \in DOMAIN follow.dispatches :
            (e[2] # "answer" /\ FDispatch(e) > 0 /\ FIntent(e) > h)
            => h < follow.consumed /\ follow.consumed <= FIntent(e)
        )

FollowRenewalWaits ==
    lane = "followup" => (
        follow.consumed > 0 /\ follow.barrier > 0 =>
            follow.resultSnapshots[follow.barrier] > 0 /\
            follow.resultSnapshots[follow.barrier] < follow.consumed
        )

FollowSnapshotsBeforeEffects ==
    lane = "followup" => (
        \A e \in DOMAIN follow.dispatches : FDispatch(e) > 0 =>
            /\ FIntent(e) \in 1..Len(follow.snapshots)
            /\ follow.snapshots[FIntent(e)].identity = FIdentity
            /\ follow.snapshots[FIntent(e)].op = e[1]
        )

FollowEffectLedger ==
    lane = "followup" => (
        /\ follow.pending \cap follow.completed = {}
        /\ follow.cancelled \cap (follow.pending \cup follow.completed) = {}
        /\ follow.pending \cup follow.completed \cup follow.cancelled = {e \in DOMAIN follow.intents : FIntent(e) > 0}
        /\ \A e \in follow.completed : FDispatch(e) = 1
        /\ \A o \in FRecordedOps : follow.starts[o] = FDispatch(<<o, "run">>)
        /\ \A o \in FRecordedOps : follow.publications[o] > 0 => FDispatch(<<o, "publication">>) = 1
        /\ follow.creations = Cardinality({o \in FRecordedOps : follow.kinds[o] = "create" /\ follow.publications[o] > 0})
        /\ follow.reopens = Cardinality({o \in FRecordedOps : FDispatch(<<o, "reopen">>) = 1})
        /\ follow.knownPR # "none" => follow.creations = 1
        /\ follow.merges = (IF follow.remotePR = "merged" THEN 1 ELSE 0)
        /\ \A o \in FRecordedOps : follow.resultSnapshots[o] > 0 =>
            /\ follow.resultSnapshots[o] <= Len(follow.snapshots)
            /\ follow.snapshots[follow.resultSnapshots[o]].op = o
            /\ ~\E e \in follow.pending : e[1] = o
        )

FollowResultsCurrent ==
    lane = "followup" => (
     follow.applied
        )

FollowCurrentRevision ==
    lane = "followup" => (
        /\ follow.ci # "none" => follow.ciHead = follow.head
        /\ follow.canMerge => follow.mergeHead = follow.head
        /\ follow.checks
        )

FollowAllowance ==
    lane = "followup" => (
        /\ follow.charged = {o \in FRecordedOps : follow.kinds[o] = "repair"}
        /\ \A r \in 0..1 : Cardinality({o \in follow.charged : follow.rounds[o] = r}) <= 2
        /\ follow.left + Cardinality({o \in follow.charged : follow.rounds[o] = follow.round}) = 2
        /\ (follow.round = 1) = (follow.consumed > 0)
        /\ Cardinality({i \in 1..Len(follow.snapshots) : follow.snapshots[i].stage = "writer-renewal"}) =
            (IF follow.consumed > 0 THEN 1 ELSE 0)
        /\ follow.consumed > 0 =>
            follow.snapshots[follow.consumed].left = 2 /\
            follow.snapshots[follow.consumed].stage = "writer-renewal"
        )

FollowFailurePreserved ==
    lane = "followup" => (
        \A o \in FRecordedOps :
            follow.raw[o] \in {"failed", "nochange", "stopped"} /\ follow.results[o] # "none"
            => follow.results[o] = follow.raw[o]
        )

FollowMergeConfirmed ==
    lane = "followup" => (
        /\ follow.status = "merged" =>
            follow.remotePR = "merged" /\ follow.merges = 1 /\ follow.proof # "none"
        /\ follow.mergeOutcome = "success" => follow.status = "merged"
        /\ follow.externalMerged /\ follow.remotePR = "merged" =>
            \A o \in FRecordedOps : FDispatch(<<o, "merge">>) = follow.externalDispatches[o]
        )

FollowAnswersOnce ==
    lane = "followup" => (
     follow.answers <= 1
        )

\* Statements about progress do not require a person, CI or GitHub to act.
FollowRunsSettled ==
    \A o \in FOps :
        (IF lane = "followup" THEN
            IF o <= follow.op THEN follow.starts[o] = 1 ELSE FALSE
         ELSE FALSE)
        ~> (IF lane = "followup" THEN
               IF o <= follow.op THEN follow.resultSnapshots[o] > 0 ELSE FALSE
            ELSE TRUE)

FollowPublicationsSettled ==
    \A o \in FOps :
        (IF lane = "followup" THEN
            IF o <= follow.op THEN FDispatch(<<o, "publication">>) = 1 ELSE FALSE
         ELSE FALSE)
        ~> (IF lane = "followup" THEN
               IF o <= follow.op THEN follow.resultSnapshots[o] > 0 ELSE FALSE
            ELSE TRUE)

FollowKnownMergesSettled ==
    /\ (IF lane = "followup" THEN follow.remotePR = "merged" ELSE FALSE)
        ~> (IF lane = "followup" THEN follow.status = "merged" ELSE TRUE)
    /\ (IF lane = "followup" THEN follow.remoteMerge = "refused" ELSE FALSE)
        ~> (IF lane = "followup" THEN follow.status \in {"merged", "refused"} ELSE TRUE)

FollowRequestsAnswered ==
    (IF lane = "followup" THEN follow.requested ELSE FALSE)
    ~> (IF lane = "followup" THEN follow.answers = 1 ELSE TRUE)

FollowAuthorizedWorkStarts ==
    \A o \in FOps :
        (IF lane = "followup" THEN
            IF o <= follow.op THEN follow.kinds[o] # "none" ELSE FALSE
         ELSE FALSE)
        ~> (IF lane = "followup" THEN
               IF o <= follow.op THEN
                   follow.resultSnapshots[o] > 0 \/
                   (\E k \in FEffectKinds : FDispatch(<<o, k>>) = 1)
               ELSE FALSE
            ELSE TRUE)

FollowAutomaticRetries ==
    \A o \in FOps :
        (IF lane = "followup" THEN
            IF o <= follow.op THEN
                follow.kinds[o] \in {"draft", "create"} /\ follow.resultSnapshots[o] > 0 /\
                follow.results[o] \in {"failed", "stopped", "nochange", "refused"}
            ELSE FALSE
         ELSE FALSE)
        ~> (IF lane = "followup" THEN
               IF o <= follow.op THEN
                   \E n \in FRecordedOps : n > o /\ follow.kinds[n] = follow.kinds[o] /\ follow.starts[n] = 1
               ELSE FALSE
            ELSE TRUE)

\* Witnesses name durable facts, not a chosen scenario or a program counter.
AutomaticDraftAndCreateRecovery ==
    /\ lane = "followup"
    /\ (
        /\ follow.publicationRefused # {}
        /\ follow.creations = 1
        /\ \E d, c \in FRecordedOps : follow.kinds[d] = "draft" /\ follow.results[d] # "success" /\
             follow.resultSnapshots[d] > 0 /\ follow.kinds[c] = "create" /\
             follow.results[c] # "success" /\ follow.resultSnapshots[c] > 0
        )

RepairAfterCIFailure ==
    /\ lane = "followup"
    /\ (
        follow.head >= 2 /\ follow.creations = 1 /\ Cardinality(follow.charged) >= 1
        )

ExhaustedThenRenewed ==
    /\ lane = "followup"
    /\ (
        follow.consumed > 0 /\ Cardinality({o \in follow.charged : follow.rounds[o] = 0}) = 2 /\
            \E o \in follow.charged : follow.rounds[o] = 1 /\ follow.publications[o] > 0
        )

ClosedFollowupResumed ==
    /\ lane = "followup"
    /\ (
     follow.reopens = 1 /\ follow.creations = 1 /\ follow.remotePR = "open"
        )

LostCreationAdoptsClosed ==
    /\ lane = "followup"
    /\ (
        follow.knownPR = "followup-pr" /\ follow.status = "closed" /\
        \E e \in follow.lost : e[2] = "publication" /\ follow.kinds[e[1]] = "create"
        )

LostCreationAdoptsMerged ==
    /\ lane = "followup"
    /\ (
        follow.knownPR = "followup-pr" /\ follow.status = "merged" /\
        \E e \in follow.lost : e[2] = "publication" /\ follow.kinds[e[1]] = "create"
        )

LostRepairPublicationRecovered ==
    /\ lane = "followup"
    /\ (
        \E e \in follow.lost : e[2] = "publication" /\ follow.kinds[e[1]] = "repair" /\
            follow.resultSnapshots[e[1]] > 0 /\ FDispatch(e) = 1
        )

LostMergeResponseRecovered ==
    /\ lane = "followup"
    /\ (
        follow.status = "merged" /\ crashes > 0 /\
        \E e \in follow.lost : e[2] = "merge" /\ e \in follow.completed
        )

UnknownMergeRecorded ==
    /\ lane = "followup"
    /\ (
     follow.mergeOutcome = "unknown" /\ follow.status # "merged"
        )

MergeRefusalReconciled ==
    /\ lane = "followup"
    /\ (
     follow.mergeOutcome = "refused" /\ follow.status = "refused"
        )

ExternalMergeObserved ==
    /\ lane = "followup"
    /\ (
        follow.status = "merged" /\ follow.proof = "observation" /\
        follow.externalMerged /\ follow.ci = "none"
        )

PendingRenewalReconciled ==
    /\ lane = "followup"
    /\ (
        follow.barrier > 0 /\ follow.consumed > follow.resultSnapshots[follow.barrier] /\
        follow.resultSnapshots[follow.barrier] > 0 /\ follow.left = 2 /\ follow.answers = 1
        )

FailedAndNoChangeRepairsRetained ==
    /\ lane = "followup"
    /\ (
        \E a, b \in follow.charged : follow.results[a] = "failed" /\ follow.results[b] = "nochange"
        )

InvalidatedRepairRetainsUsage ==
    /\ lane = "followup"
    /\ (
        \E o \in follow.charged : follow.results[o] = "invalidated" /\ follow.resultSnapshots[o] > 0
        )

TwoWatchersRecoverFollowup ==
    /\ lane = "followup"
    /\ (
        follow.status = "merged" /\ follow.actors = Watchers /\ crashes = MaxFollowCrashes
        )

\* Model helpers. Each snapshot is an accepted Workflow state, stored in an
\* append-only journal. Effects carry the journal position that authorized them.
\* Workflow authorizations are kept before effects; the environment supplies
\* independent bounded results rather than choosing an entire execution script.

LostRepairThenExternalMerge ==
    /\ LostRepairPublicationRecovered
    /\ follow.externalMerged /\ follow.status = "merged"

RepairClosedReconciled ==
    /\ lane = "followup"
    /\ follow.status = "closed"
    /\ \E o \in follow.charged : follow.results[o] = "invalidated" /\ follow.resultSnapshots[o] > 0

MergeClosedBeforeDispatch ==
    /\ lane = "followup"
    /\ follow.status = "closed"
    /\ \E o \in FRecordedOps : follow.kinds[o] = "merge" /\ follow.results[o] = "invalidated" /\
         FDispatch(<<o, "merge">>) = 0

MergeObservedBeforeDispatch ==
    /\ lane = "followup"
    /\ follow.status = "merged" /\ follow.externalMerged
    /\ \E o \in FRecordedOps : follow.kinds[o] = "merge" /\ follow.results[o] = "invalidated" /\
         FDispatch(<<o, "merge">>) = 0

MergeHeadInvalidated ==
    /\ lane = "followup"
    /\ \E o \in FRecordedOps : follow.kinds[o] = "merge" /\ follow.results[o] = "invalidated" /\
         follow.bases[o] # follow.remoteHead

RefusedThenExternallyMerged ==
    /\ lane = "followup"
    /\ follow.status = "merged" /\ follow.externalMerged
    /\ \E o \in FRecordedOps : follow.kinds[o] = "merge" /\ follow.results[o] = "refused"

FollowUnmergeableReported ==
    /\ lane = "followup"
    /\ follow.status = "unmergeable" /\ follow.ci = "pass" /\ ~follow.canMerge

FBase == <<given, posts, retried, retryPosts, buildPosts, runs, runStarts, ratPushed,
           codePushed, prs, pr, gate, mergeable, merges, pred, intent, watched, watchPosts,
           marked, lease, up, inRun, crashes>>

FFrame == UNCHANGED <<FBase, unleased, lane, fence>>

FHolding(w) == lane = "followup" /\ up[w] /\ lease = w

FKind == IF follow.op = 0 THEN "none" ELSE follow.kinds[follow.op]

FRepairUnresolved ==
    FKind = "repair" /\ follow.resultSnapshots[follow.op] = 0

FAppend(op, kind, head, left, round, cause, stage) ==
    Append(follow.snapshots, FSnapshot(op, kind, head, left, round, cause, stage))

FCheck(w, e) ==
    /\ up[w] /\ lease = w
    /\ e[2] \in {"publication", "merge"} => follow.head = follow.remoteHead
    /\ e[2] = "merge" => follow.ci = "pass" /\ follow.ciHead = follow.head /\
         follow.canMerge /\ follow.mergeHead = follow.head

FAgentResults == {"success", "failed", "stopped", "nochange"}

FLosses(k) == Cardinality({e \in follow.lost : e[2] = k})

FCreateLosses == Cardinality({e \in follow.lost : e[2] = "publication" /\ follow.kinds[e[1]] = "create"})

FRepairLosses == Cardinality({e \in follow.lost : e[2] = "publication" /\ follow.kinds[e[1]] = "repair"})

\* Accepting Workflow and recording its first intent is one durable transaction;
\* dispatch remains a separate crash boundary. Internal answer priority removes
\* irrelevant reorderings without restricting any external input.
FAuthorize(w) ==
    /\ FHolding(w)
    /\ follow.phase = "idle"
    /\ ~(follow.requested /\ follow.consumed = 0)
    /\ follow.op < MaxFollowOps
    /\ follow.next = "repair" => follow.left > 0 /\ follow.remotePR = "open"
    /\ follow.next = "merge" => follow.remotePR = "open" /\ follow.head = follow.remoteHead
    /\ follow.status = "active"
    /\ follow.next = "reopen" => follow.consumed > 0 /\ follow.remotePR = "closed"
    /\ LET o == follow.op + 1
           k == follow.next
           effect == CASE k = "merge" -> "merge" [] k = "reopen" -> "reopen" [] OTHER -> "run"
           remaining == IF k = "repair" THEN follow.left - 1 ELSE follow.left
       IN follow' = [follow EXCEPT
           !.op = o,
           !.kinds = Append(follow.kinds, k),
           !.bases = Append(follow.bases, follow.head),
           !.rounds = Append(follow.rounds, follow.round),
           !.starts = Append(follow.starts, 0),
           !.raw = Append(follow.raw, "none"),
           !.results = Append(follow.results, "none"),
           !.resultSnapshots = Append(follow.resultSnapshots, 0),
           !.externalDispatches = Append(follow.externalDispatches, 0),
           !.publications = Append(follow.publications, 0),
           !.intents = FExtend(follow.intents,
               [e \in {<<o, effect>>} |-> Len(follow.snapshots) + 1]),
           !.dispatches = FExtend(follow.dispatches, [e \in {<<o, effect>>} |-> 0]),
           !.pending = @ \cup {<<o, effect>>},
           !.left = remaining, !.charged = IF k = "repair" THEN @ \cup {o} ELSE @,
           !.snapshots = FAppend(o, k, follow.head, remaining, follow.round, follow.cause, "authorized"), !.accepted = FAppend(o, k, follow.head, remaining, follow.round, follow.cause, "authorized"),
           !.phase = CASE k = "merge" -> "start-merge" [] k = "reopen" -> "start-reopen"
                          [] OTHER -> "start-run"]
    /\ FFrame

FStart(w) ==
    /\ FHolding(w)
    /\ follow.phase = "start-run"
    /\ FKind = "repair" => follow.remotePR = "open" /\ follow.bases[follow.op] = follow.remoteHead
    /\ LET o == follow.op
           e == <<o, "run">>
       IN follow' = [follow EXCEPT !.starts[o] = @ + 1, !.dispatches[e] = @ + 1,
           !.owner = w, !.runFence = fence,
           !.actors = @ \cup {w}, !.phase = "running"]
    /\ FFrame

FFinish(w, result) ==
    /\ FHolding(w)
    /\ follow.phase = "running"
    /\ follow.owner = w /\ follow.runFence = fence
    /\ result \in FAgentResults
    /\ result # "success" => follow.events < MaxFollowEvents
    /\ (result # "success" /\ FKind = "draft") => follow.draftFailures < MaxDraftFailures
    /\ (result # "success" /\ FKind = "create") => follow.createFailures < MaxCreateFailures
    /\ follow' = [follow EXCEPT !.raw[follow.op] = result, !.phase = "run-result",
           !.draftFailures = IF result # "success" /\ FKind = "draft" THEN @ + 1 ELSE @,
           !.createFailures = IF result # "success" /\ FKind = "create" THEN @ + 1 ELSE @,
           !.events = IF result # "success" THEN @ + 1 ELSE @]
    /\ FFrame

FRecoverStopped(w) ==
    /\ FHolding(w)
    /\ follow.phase = "running"
    /\ follow.owner # w \/ follow.runFence # fence
    /\ follow' = [follow EXCEPT !.raw[follow.op] = "stopped", !.phase = "run-result"]
    /\ FFrame

FReconcileRun(w) ==
    /\ FHolding(w)
    /\ follow.phase = "run-result"
    /\ LET o == follow.op
           result == IF follow.raw[o] = "success" /\ FKind = "repair" /\ (follow.bases[o] # follow.remoteHead \/ follow.remotePR # "open")
                     THEN "invalidated" ELSE follow.raw[o]
           publish == result = "success" /\ FKind \in {"create", "repair"}
           hold == FKind = "repair" /\ ~publish /\ (follow.left = 0 \/ follow.remotePR = "closed")
           idx == Len(follow.snapshots) + 1
           newhead == IF result = "invalidated" THEN follow.remoteHead ELSE follow.head
       IN follow' = [follow EXCEPT
           !.results[o] = result, !.owner = NoOne, !.runFence = 0,
           !.applied = @ /\ (result # "success" \/ follow.bases[o] = newhead),
           !.completed = @ \cup {<<o, "run">>},
           !.pending = (@ \ {<<o, "run">>}) \cup (IF publish THEN {<<o, "publication">>} ELSE {}),
           !.intents = IF publish THEN
               FExtend(follow.intents, [e \in {<<o, "publication">>} |-> idx]) ELSE @,
           !.dispatches = IF publish THEN
               FExtend(follow.dispatches, [e \in {<<o, "publication">>} |-> 0]) ELSE @,
           !.resultSnapshots[o] = IF publish THEN 0 ELSE idx,
           !.snapshots = FAppend(o, FKind, newhead, follow.left, follow.round, result, "run-outcome"), !.accepted = FAppend(o, FKind, newhead, follow.left, follow.round, result, "run-outcome"),
           !.head = newhead, !.ci = IF result = "invalidated" THEN "none" ELSE @,
           !.ciHead = IF result = "invalidated" THEN 0 ELSE @,
           !.canMerge = IF result = "invalidated" THEN FALSE ELSE @,
           !.mergeHead = IF result = "invalidated" THEN 0 ELSE @,
           !.cause = IF result = "success" THEN @ ELSE result,
           !.phase = IF publish THEN "start-publication" ELSE IF hold THEN "hold" ELSE "idle",
           !.status = IF hold THEN IF follow.remotePR = "closed" THEN "closed" ELSE "exhausted" ELSE @,
           !.holds = IF hold THEN @ \cup {idx} ELSE @,
           !.next = IF FKind = "draft" /\ result = "success" THEN "create" ELSE FKind]
    /\ FFrame

FPublish(w, lose) ==
    /\ FHolding(w)
    /\ follow.phase = "start-publication"
    /\ lose => follow.events < MaxFollowEvents
    /\ lose => IF FKind = "create" THEN FCreateLosses < MaxLostCreations ELSE FRepairLosses < MaxLostPublications
    /\ FKind = "create" => follow.remotePR = "none"
    /\ FKind = "repair" => follow.remotePR = "open" /\ follow.bases[follow.op] = follow.remoteHead
    /\ LET o == follow.op
           e == <<o, "publication">>
           newhead == follow.remoteHead + 1
       IN follow' = [follow EXCEPT !.dispatches[e] = @ + 1,
           !.checks = @ /\ FCheck(w, e), !.actors = @ \cup {w},
           !.remotePR = "open", !.creations = IF FKind = "create" THEN @ + 1 ELSE @,
           !.remoteHead = newhead, !.publications[o] = newhead,
           !.remoteCI = "none", !.remoteCIHead = 0, !.remoteCanMerge = FALSE, !.remoteMergeHead = 0,
           !.lost = IF lose THEN @ \cup {e} ELSE @, !.phase = "publication",
           !.events = IF lose THEN @ + 1 ELSE @]
    /\ FFrame

FReconcileSuperseded(w) ==
    /\ FHolding(w)
    /\ follow.phase \in {"record-run", "start-run", "record-publication", "start-publication", "record-merge", "start-merge"}
    /\ FKind \in {"repair", "merge"}
    /\ follow.bases[follow.op] # follow.remoteHead \/ follow.remotePR # "open"
    /\ LET o == follow.op
           e == <<o, IF FKind = "merge" THEN "merge" ELSE
                 IF follow.phase \in {"record-run", "start-run"} THEN "run" ELSE "publication">>
           idx == Len(follow.snapshots) + 1
           closed == follow.remotePR = "closed"
           merged == follow.remotePR = "merged"
           hold == closed \/ (FKind = "repair" /\ follow.left = 0 /\ ~merged)
           snap == FAppend(o, FKind, follow.remoteHead, follow.left, follow.round, "invalidated", "superseded-outcome")
       IN follow' = [follow EXCEPT !.results[o] = "invalidated", !.cause = "invalidated",
           !.resultSnapshots[o] = idx, !.head = follow.remoteHead,
           !.ci = "none", !.ciHead = 0, !.canMerge = FALSE, !.mergeHead = 0,
           !.snapshots = snap, !.accepted = snap,
           !.pending = @ \ {e}, !.cancelled = IF FIntent(e) > 0 THEN @ \cup {e} ELSE @,
           !.phase = IF merged THEN "done" ELSE IF hold THEN "hold" ELSE IF FKind = "merge" THEN "watch" ELSE "idle",
           !.next = "repair", !.proof = IF merged THEN "observation" ELSE @,
           !.status = IF merged THEN "merged" ELSE IF closed THEN "closed" ELSE IF hold THEN "exhausted" ELSE @,
           !.holds = IF hold THEN @ \cup {idx} ELSE @]
    /\ FFrame

FPublicationRefusal(w, lose) ==
    /\ FHolding(w)
    /\ follow.phase = "start-publication"
    /\ FKind = "create" /\ Cardinality(follow.publicationRefused) < MaxPublicationRefusals
    /\ follow.events + 1 + (IF lose THEN 1 ELSE 0) <= MaxFollowEvents
    /\ lose => FCreateLosses < MaxLostCreations
    /\ follow' = [follow EXCEPT !.dispatches[<<follow.op, "publication">>] = @ + 1,
           !.publicationRefused = @ \cup {follow.op}, !.actors = @ \cup {w}, !.phase = "publication",
           !.events = @ + 1 + (IF lose THEN 1 ELSE 0),
           !.lost = IF lose THEN @ \cup {<<follow.op, "publication">>} ELSE @]
    /\ FFrame

FReconcileCreationRefusal(w) ==
    /\ FHolding(w)
    /\ follow.phase = "publication" /\ follow.op \in follow.publicationRefused
    /\ LET o == follow.op
           snap == FAppend(o, FKind, follow.head, follow.left, follow.round, "refused", "creation-outcome")
       IN follow' = [follow EXCEPT !.pending = @ \ {<<o, "publication">>},
           !.completed = @ \cup {<<o, "publication">>}, !.resultSnapshots[o] = Len(follow.snapshots) + 1,
           !.results[o] = "refused", !.cause = "refused", !.phase = "idle", !.next = "create",
           !.snapshots = snap, !.accepted = snap]
    /\ FFrame

FReconcilePublication(w) ==
    /\ FHolding(w)
    /\ follow.phase = "publication"
    /\ follow.publications[follow.op] > 0
    /\ LET o == follow.op
           idx == Len(follow.snapshots) + 1
           closed == follow.remotePR = "closed"
           merged == follow.remotePR = "merged"
       IN follow' = [follow EXCEPT !.knownPR = "followup-pr", !.head = follow.remoteHead,
           !.ci = "none", !.ciHead = 0, !.canMerge = FALSE, !.mergeHead = 0,
           !.completed = @ \cup {<<o, "publication">>}, !.pending = @ \ {<<o, "publication">>},
           !.resultSnapshots[o] = idx,
           !.snapshots = FAppend(o, FKind, follow.remoteHead, follow.left, follow.round, follow.cause, "publication-outcome"), !.accepted = FAppend(o, FKind, follow.remoteHead, follow.left, follow.round, follow.cause, "publication-outcome"),
           !.phase = IF merged THEN "done" ELSE IF closed THEN "hold" ELSE "watch",
           !.status = IF merged THEN "merged" ELSE IF closed THEN "closed" ELSE "active",
           !.proof = IF merged THEN "observation" ELSE @,
           !.holds = IF closed THEN @ \cup {idx} ELSE @]
    /\ FFrame

FObservePR(w) ==
    /\ FHolding(w)
    /\ follow.phase \in {"watch", "idle", "hold", "done"}
    /\ follow.pending = {} \/ follow.pending = {<<0, "answer">>}
    /\ follow.status # "merged"
    /\ ~(follow.status = "closed" /\ follow.remotePR = "closed")
    /\ ~(follow.phase = "idle" /\ follow.next = "reopen" /\ follow.consumed > 0)
    /\ follow.remotePR \in {"closed", "merged"}
    /\ LET idx == Len(follow.snapshots) + 1
           closed == follow.remotePR = "closed"
       IN follow' = [follow EXCEPT
           !.head = follow.remoteHead,
           !.ci = IF follow.head # follow.remoteHead THEN "none" ELSE @,
           !.ciHead = IF follow.head # follow.remoteHead THEN 0 ELSE @,
           !.canMerge = IF follow.head # follow.remoteHead THEN FALSE ELSE @,
           !.mergeHead = IF follow.head # follow.remoteHead THEN 0 ELSE @,
           !.phase = IF closed THEN "hold" ELSE "done",
           !.status = IF closed THEN "closed" ELSE "merged",
           !.proof = IF closed THEN @ ELSE "observation",
           !.holds = IF closed THEN @ \cup {idx} ELSE @,
           !.snapshots = FAppend(follow.op, FKind, follow.remoteHead, follow.left, follow.round, follow.cause, "pr-observation"), !.accepted = FAppend(follow.op, FKind, follow.remoteHead, follow.left, follow.round, follow.cause, "pr-observation")]
    /\ FFrame

FObserveCI(w) ==
    /\ FHolding(w)
    /\ follow.phase = "watch"
    /\ follow.remotePR = "open"
    /\ follow.remoteCI # "none" /\ follow.remoteCIHead = follow.head
    /\ follow.remoteMergeHead = follow.head
    /\ LET failed == follow.remoteCI = "fail"
           hold == failed /\ follow.left = 0
           unmergeable == ~failed /\ ~follow.remoteCanMerge
           idx == Len(follow.snapshots) + 1
       IN follow' = [follow EXCEPT !.ci = follow.remoteCI, !.ciHead = follow.head,
           !.canMerge = follow.remoteCanMerge, !.mergeHead = follow.remoteMergeHead,
           !.phase = IF unmergeable THEN "done" ELSE IF hold THEN "hold" ELSE "idle",
           !.next = IF failed THEN "repair" ELSE "merge",
           !.status = IF unmergeable THEN "unmergeable" ELSE IF hold THEN "exhausted" ELSE @,
           !.holds = IF hold THEN @ \cup {idx} ELSE @,
           !.cause = IF failed THEN "ci-failed" ELSE @,
           !.snapshots = FAppend(follow.op, FKind, follow.head, follow.left, follow.round,
                                IF failed THEN "ci-failed" ELSE follow.cause, "ci-observation"), !.accepted = FAppend(follow.op, FKind, follow.head, follow.left, follow.round,
                                IF failed THEN "ci-failed" ELSE follow.cause, "ci-observation")]
    /\ FFrame

FConsumeRequest(w) ==
    /\ FHolding(w)
    /\ follow.requested /\ follow.consumed = 0
    /\ ~FRepairUnresolved
    /\ follow.phase \in {"hold", "idle", "watch", "done"}
    /\ LET idx == Len(follow.snapshots) + 1
           closed == follow.remotePR = "closed"
       IN follow' = [follow EXCEPT !.consumed = idx, !.round = 1, !.left = 2,
           !.snapshots = FAppend(0, "none", follow.head, 2, 1, follow.cause, "writer-renewal"), !.accepted = FAppend(0, "none", follow.head, 2, 1, follow.cause, "writer-renewal"),
           !.phase = IF closed \/ follow.status = "exhausted" THEN "idle" ELSE @,
           !.next = IF closed THEN "reopen" ELSE IF follow.status = "exhausted" THEN "repair" ELSE @,
           !.status = IF follow.phase = "done" /\ ~closed THEN @ ELSE "active"]
    /\ FFrame

FRecordAnswer(w) ==
    /\ FHolding(w)
    /\ follow.consumed > 0 /\ FIntent(<<0, "answer">>) = 0
    /\ follow' = [follow EXCEPT
           !.intents = FExtend(follow.intents, [e \in {<<0, "answer">>} |-> follow.consumed]),
           !.dispatches = FExtend(follow.dispatches, [e \in {<<0, "answer">>} |-> 0]),
           !.pending = @ \cup {<<0, "answer">>}]
    /\ FFrame

FAnswer(w) ==
    /\ FHolding(w)
    /\ <<0, "answer">> \in follow.pending
    /\ follow' = [follow EXCEPT !.answers = @ + 1, !.dispatches[<<0, "answer">>] = @ + 1,
           !.pending = @ \ {<<0, "answer">>}, !.completed = @ \cup {<<0, "answer">>},
           !.actors = @ \cup {w}]
    /\ FFrame

FReopen(w) ==
    /\ FHolding(w)
    /\ follow.phase = "start-reopen"
    /\ follow.remotePR = "closed" /\ follow.consumed > 0
    /\ follow' = [follow EXCEPT !.remotePR = "open", !.reopens = @ + 1,
           !.dispatches[<<follow.op, "reopen">>] = @ + 1,
           !.actors = @ \cup {w}, !.phase = "reopen-response"]
    /\ FFrame

FReconcileReopen(w) ==
    /\ FHolding(w)
    /\ follow.phase = "reopen-response"
    /\ LET o == follow.op
       IN follow' = [follow EXCEPT !.pending = @ \ {<<o, "reopen">>},
           !.completed = @ \cup {<<o, "reopen">>}, !.resultSnapshots[o] = Len(follow.snapshots) + 1,
           !.results[o] = "success", !.phase = "watch",
           !.snapshots = FAppend(o, FKind, follow.head, follow.left, follow.round, follow.cause, "reopen-outcome"), !.accepted = FAppend(o, FKind, follow.head, follow.left, follow.round, follow.cause, "reopen-outcome")]
    /\ FFrame

FMerge(w, result, lose) ==
    /\ FHolding(w)
    /\ follow.phase = "start-merge"
    /\ result \in {"success", "refused", "unknown"}
    /\ follow.events + (IF result # "success" THEN 1 ELSE 0) + (IF lose THEN 1 ELSE 0) <= MaxFollowEvents
    /\ result = "unknown" => follow.unknownMerges < MaxUnknownMerges
    /\ result = "refused" => follow.refusedMerges < MaxRefusedMerges
    /\ lose => FLosses("merge") < MaxLostMerges
    /\ follow.remotePR = "open" /\ follow.head = follow.remoteHead
    /\ follow.ci = "pass" /\ follow.ciHead = follow.head
    /\ follow.canMerge /\ follow.mergeHead = follow.head
    /\ LET e == <<follow.op, "merge">>
       IN follow' = [follow EXCEPT !.dispatches[e] = @ + 1,
           !.checks = @ /\ FCheck(w, e), !.actors = @ \cup {w},
           !.remoteMerge = result, !.phase = "merge-response",
           !.events = @ + (IF result # "success" THEN 1 ELSE 0) + (IF lose THEN 1 ELSE 0),
           !.unknownMerges = IF result = "unknown" THEN @ + 1 ELSE @,
           !.refusedMerges = IF result = "refused" THEN @ + 1 ELSE @,
           !.remotePR = IF result = "success" THEN "merged" ELSE @,
           !.merges = IF result = "success" THEN @ + 1 ELSE @,
           !.lost = IF lose THEN @ \cup {e} ELSE @]
    /\ FFrame

FReconcileMerge(w) ==
    /\ FHolding(w)
    /\ follow.phase \in {"merge-response", "uncertain"}
    /\ follow.phase = "uncertain" => follow.remoteMerge # "unknown" \/ follow.remotePR = "merged"
    /\ LET o == follow.op
           result == IF follow.remotePR = "merged" THEN "success" ELSE follow.remoteMerge
           terminal == result # "unknown"
       IN follow' = [follow EXCEPT !.mergeOutcome = result, !.results[o] = result,
           !.remoteMerge = IF terminal THEN "none" ELSE @,
           !.phase = IF terminal THEN "done" ELSE "uncertain",
           !.status = CASE result = "success" -> "merged" [] result = "refused" -> "refused" [] OTHER -> @,
           !.cause = IF result = "refused" THEN "refused" ELSE @,
           !.proof = IF result = "success" THEN
                        IF <<o, "merge">> \in follow.lost \/ follow.externalMerged \/ follow.remoteMerge # "success"
                        THEN "observation" ELSE "response" ELSE @,
           !.pending = IF terminal THEN @ \ {<<o, "merge">>} ELSE @,
           !.completed = IF terminal THEN @ \cup {<<o, "merge">>} ELSE @,
           !.resultSnapshots[o] = IF terminal THEN Len(follow.snapshots) + 1 ELSE 0,
           !.snapshots = FAppend(o, FKind, follow.head, follow.left, follow.round, result, "merge-outcome"), !.accepted = FAppend(o, FKind, follow.head, follow.left, follow.round, result, "merge-outcome")]
    /\ FFrame

FGiveRequest ==
    /\ lane = "followup" /\ ~follow.requested
    /\ follow.phase = "hold" \/ FRepairUnresolved
    /\ follow' = [follow EXCEPT !.requested = TRUE,
                   !.barrier = IF FRepairUnresolved THEN follow.op ELSE 0]
    /\ FFrame

FCIEvent(result, mergeableNow) ==
    /\ lane = "followup" /\ follow.remotePR = "open"
    /\ follow.remoteCI = "none"
    /\ result \in {"pass", "fail"}
    /\ result = "fail" => follow.ciFailures < MaxCIFailures /\ follow.events < MaxFollowEvents
    /\ follow' = [follow EXCEPT !.remoteCI = result,
           !.remoteCIHead = follow.remoteHead, !.remoteCanMerge = mergeableNow,
           !.remoteMergeHead = follow.remoteHead,
           !.ciFailures = IF result = "fail" THEN @ + 1 ELSE @,
           !.events = IF result = "fail" THEN @ + 1 ELSE @]
    /\ FFrame

FExternalClose ==
    /\ lane = "followup" /\ follow.closeCount < MaxCloses /\ follow.remotePR = "open"
    /\ follow.events < MaxFollowEvents
    /\ follow' = [follow EXCEPT !.remotePR = "closed", !.closeCount = @ + 1, !.events = @ + 1]
    /\ FFrame

FExternalMerge ==
    /\ lane = "followup" /\ follow.remotePR = "open"
    /\ follow.events < MaxFollowEvents
    /\ follow' = [follow EXCEPT !.remotePR = "merged", !.merges = @ + 1, !.externalMerged = TRUE, !.events = @ + 1,
          !.externalDispatches = [o \in FRecordedOps |-> FDispatch(<<o, "merge">>)]]
    /\ FFrame

FInvalidateHead ==
    /\ lane = "followup" /\ follow.headMoves < MaxHeadMoves
    /\ follow.events < MaxFollowEvents
    /\ follow.remotePR \in {"open", "closed"}
    /\ follow' = [follow EXCEPT !.remoteHead = @ + 1,
           !.remoteCI = "none", !.remoteCIHead = 0, !.remoteCanMerge = FALSE, !.remoteMergeHead = 0,
           !.headMoves = @ + 1, !.events = @ + 1]
    /\ FFrame

FObserveHead(w) ==
    /\ FHolding(w)
    /\ follow.phase \in {"watch", "idle", "hold", "done"}
    /\ follow.head # follow.remoteHead
    /\ follow.remotePR \in {"open", "closed"}
    /\ follow' = [follow EXCEPT !.head = follow.remoteHead,
           !.ci = "none", !.ciHead = 0, !.canMerge = FALSE, !.mergeHead = 0,
           !.phase = IF follow.phase = "idle" /\ follow.next = "merge" THEN "watch" ELSE @,
           !.snapshots = FAppend(follow.op, FKind, follow.remoteHead, follow.left, follow.round, follow.cause, "head-observation"),
           !.accepted = FAppend(follow.op, FKind, follow.remoteHead, follow.left, follow.round, follow.cause, "head-observation")]
    /\ FFrame

FResolveUnknown(result) ==
    /\ lane = "followup" /\ follow.phase = "uncertain" /\ follow.remoteMerge = "unknown"
    /\ follow.remotePR # "merged"
    /\ result = "refused" => follow.refusedMerges < MaxRefusedMerges
    /\ result = "success" => follow.remotePR = "open" /\ follow.remoteHead = follow.bases[follow.op]
    /\ follow' = [follow EXCEPT !.remoteMerge = result,
           !.refusedMerges = IF result = "refused" THEN @ + 1 ELSE @,
           !.remotePR = IF result = "success" THEN "merged" ELSE @,
           !.merges = IF result = "success" THEN @ + 1 ELSE @]
    /\ FFrame

FollowAct(w) ==
    \/ FConsumeRequest(w)
    \/ /\ follow.consumed > 0 /\ follow.answers = 0
       /\ (FRecordAnswer(w) \/ FAnswer(w))
    \/ /\ ~(follow.consumed > 0 /\ follow.answers = 0)
       /\ ~(follow.requested /\ follow.consumed = 0 /\ ~FRepairUnresolved /\
               follow.phase \in {"hold", "idle", "watch", "done"})
       /\ (
            \/ FAuthorize(w)
            \/ FStart(w) \/ (\E r \in FAgentResults : FFinish(w, r))
            \/ FRecoverStopped(w) \/ FReconcileRun(w)
            \/ (\E lost \in BOOLEAN : FPublish(w, lost))
            \/ FReconcileSuperseded(w) \/ FReconcilePublication(w)
            \/ (\E lost \in BOOLEAN : FPublicationRefusal(w, lost)) \/ FReconcileCreationRefusal(w)
            \/ FObservePR(w) \/ FObserveCI(w) \/ FObserveHead(w)
            \/ FReopen(w) \/ FReconcileReopen(w)
            \/ (\E r \in {"success", "refused", "unknown"}, lost \in BOOLEAN : FMerge(w, r, lost))
            \/ FReconcileMerge(w)
           )

FollowOutside ==
    \/ FGiveRequest
    \/ \E r \in {"pass", "fail"}, m \in BOOLEAN : FCIEvent(r, m)
    \/ FExternalClose \/ FExternalMerge \/ FInvalidateHead
    \/ \E r \in {"success", "refused"} : FResolveUnknown(r)

DuplicateFollowCreation ==
    /\ lane = "followup" /\ follow.phase = "publication" /\ FKind = "create"
    /\ follow.remotePR \in {"closed", "merged"}
    /\ follow' = [follow EXCEPT !.creations = @ + 1, !.remotePR = "open"]
    /\ FFrame

DuplicateFollowRun ==
    /\ lane = "followup" /\ follow.op > 0 /\ follow.raw[follow.op] = "stopped"
    /\ follow' = [follow EXCEPT !.starts[follow.op] = @ + 1,
                               !.dispatches[<<follow.op, "run">>] = @ + 1]
    /\ FFrame

DuplicateFollowMerge ==
    /\ lane = "followup" /\ follow.status = "merged"
    /\ follow' = [follow EXCEPT !.merges = @ + 1]
    /\ FFrame

ApplyStaleRepairResult ==
    /\ lane = "followup" /\ FKind = "repair" /\ follow.phase = "running"
    /\ \E o \in FRecordedOps : o < follow.op /\ follow.starts[o] = 1 /\
         follow' = [follow EXCEPT !.applied = @ /\ (o = follow.op)]
    /\ FFrame

DispatchWithoutRecord ==
    /\ lane = "followup" /\ follow.phase = "start-run"
    /\ follow' = [follow EXCEPT !.intents[<<follow.op, "run">>] = 0, !.starts[follow.op] = @ + 1,
                               !.dispatches[<<follow.op, "run">>] = @ + 1]
    /\ FFrame

FollowEffectAfterLeaseLoss ==
    /\ lane = "followup" /\ follow.phase = "running"
    /\ follow.owner # lease
    /\ unleased' = unleased \cup {follow.owner}
    /\ UNCHANGED <<FBase, lane, fence, follow>>

ReuseFollowCI ==
    /\ lane = "followup" /\ follow.head >= 2
    /\ follow' = [follow EXCEPT !.ci = "pass", !.ciHead = follow.head - 1,
                               !.canMerge = TRUE, !.mergeHead = follow.head - 1]
    /\ FFrame

ResumeWithoutWriter ==
    /\ lane = "followup" /\ follow.phase = "hold" /\ ~follow.requested
    /\ follow' = [follow EXCEPT !.consumed = Len(follow.snapshots) + 1,
           !.round = 1, !.left = 2, !.phase = "idle", !.status = "active",
           !.next = IF follow.remotePR = "closed" THEN "reopen" ELSE "repair",
           !.snapshots = FAppend(0, "none", follow.head, 2, 1, follow.cause, "writer-renewal"), !.accepted = FAppend(0, "none", follow.head, 2, 1, follow.cause, "writer-renewal")]
    /\ FFrame

RenewBeforeReconciliation ==
    /\ lane = "followup" /\ follow.requested /\ follow.consumed = 0 /\ FRepairUnresolved
    /\ follow.barrier > 0
    /\ follow' = [follow EXCEPT !.consumed = Len(follow.snapshots) + 1, !.round = 1, !.left = 2,
           !.snapshots = FAppend(0, "none", follow.head, 2, 1, follow.cause, "writer-renewal"), !.accepted = FAppend(0, "none", follow.head, 2, 1, follow.cause, "writer-renewal")]
    /\ FFrame

TreatAuthorizationAsMerge ==
    /\ lane = "followup" /\ follow.phase = "start-merge"
    /\ follow' = [follow EXCEPT !.status = "merged", !.mergeOutcome = "success"]
    /\ FFrame

ChargeLostPushAgain ==
    /\ lane = "followup" /\ follow.phase = "publication" /\ FKind = "repair"
    /\ <<follow.op, "publication">> \in follow.lost /\ follow.left > 0
    /\ follow' = [follow EXCEPT !.left = @ - 1]
    /\ FFrame

HideRepairFailure ==
    /\ lane = "followup" /\ FKind = "repair" /\ follow.results[follow.op] = "nochange"
    /\ follow' = [follow EXCEPT !.results[follow.op] = "failed"]
    /\ FFrame

TypeOK ==
    /\ given \subseteq Cmds
    /\ posts \in [Keys -> Nat]
    /\ retried \in BOOLEAN
    /\ retryPosts \in Nat
    /\ buildPosts \in Nat
    /\ runs \in [RunKeys -> {"none", "recorded", "done"}]
    /\ runStarts \in [RunKeys -> Nat]
    /\ ratPushed \in BOOLEAN
    /\ codePushed \in BOOLEAN
    /\ prs \in Nat
    /\ pr \in {"none", "draft", "ready", "closed", "merged"}
    /\ gate \in {"none", "pass"}
    /\ mergeable \in BOOLEAN
    /\ merges \in Nat
    /\ pred \in {"none", "draft", "retired", "closed", "merged"}
    /\ intent \in BOOLEAN
    /\ watched \in BOOLEAN
    /\ watchPosts \in Nat
    /\ marked \in BOOLEAN
    /\ lease \in Watchers \cup {NoOne}
    /\ up \in [Watchers -> BOOLEAN]
    /\ inRun \in [Watchers -> RunKeys \cup {"none", "stale"}]
    /\ crashes \in 0..MaxCrashes
    /\ unleased \subseteq Watchers
    /\ lane \in {"ordinary", "followup"}
    /\ fence \in Nat
    /\ FollowTypeOK

NoDoubleAnswer ==
    /\ \A k \in Keys : posts[k] <= 1
    /\ retryPosts <= 1
    /\ watchPosts <= 1
    /\ buildPosts <= 1

NoRunTwice == \A k \in RunKeys : runStarts[k] <= 1

OnePullRequest == prs <= 1

OneOpenPullRequest == ~(pr \in {"draft", "ready"} /\ pred = "draft")

RetiredOnlyByRecord == pred = "retired" => intent

NoReplacingMerged == pred = "merged" => runs["build"] = "none" /\ prs = 0

OwnGate == gate = "pass" => prs >= 1

OwnRun == runs["build"] # "none" => runStarts["build"] >= 1

OneMerge == merges <= 1

OnlyHolderActs == unleased = {}

MergeOnlyAfterGate == merges >= 1 => gate = "pass" /\ mergeable

ReadiedOnlyWhenWatched == marked => watched /\ watchPosts >= 1

ReadiedOnlyAfterGate == marked => gate = "pass" /\ mergeable

\* Helpers, computed only from what every watcher can see.

\* The step for k has been asked for: a command was given, or the gate passed
\* on a pull request that isn't a failed draft, or on a failed draft that a
\* writer's retry watches again, once that retry is answered.
Triggered(k) ==
    CASE k \in Cmds -> k \in given
      [] k = "merge" -> gate = "pass" /\ (pr # "draft" \/ (watched /\ watchPosts >= 1))

\* A step is open until a post answers it; the post is always its last effect.
Active(k) == Triggered(k) /\ posts[k] = 0

\* Every effect of step k that comes before its post is already there.
EffectsDone(k) ==
    CASE k = "solve" -> runs["solve"] = "done"
      [] k = "ratify" -> ratPushed
      [] k = "merge" -> merges >= 1 \/ ~mergeable \/ pr \in {"closed", "merged"}
      [] k = "note" -> TRUE

\* The latest retry is still to be answered.
RetryActive == retried /\ retryPosts = 0

\* The retry that watches the latest build's failed draft again is still to
\* be answered.
WatchActive == watched /\ watchPosts = 0

\* The latest retry builds again: nothing was left to replace, or its record
\* says it replaces the draft, which is closed. A retry whose draft someone
\* merged, or closed before the record was stored, is answered without one.
Replacing == pred = "none" \/ (intent /\ pred \in {"retired", "closed"})

\* The latest build has been asked for: by the ratification's answer, or by
\* the latest retry's answer when that retry builds again.
BuildDue == IF retried THEN retryPosts >= 1 /\ Replacing ELSE posts["ratify"] >= 1

\* The latest build is asked for and no post answers it yet.
BuildActive == BuildDue /\ buildPosts = 0

MergeDone == merges >= 1 \/ posts["merge"] >= 1

\* Witnesses.

Merged == merges = 1 /\ posts["merge"] = 1

ReportedUnmergeable == gate = "pass" /\ ~mergeable /\ posts["merge"] = 1

StoppedRunReported == ~retried /\ runs["build"] = "recorded" /\ buildPosts = 1

RetryBuilt == retried /\ pred = "none" /\ prs = 1 /\ buildPosts = 1

RecoveredAfterCrashes == crashes = MaxCrashes /\ Merged

Replaced == pred = "retired" /\ prs = 1 /\ buildPosts = 1

ReplacedAndMerged == pred = "retired" /\ Merged

FailedAgain == retried /\ pr = "draft" /\ buildPosts = 1

StoppedAfterRetiring == pred = "retired" /\ runs["build"] = "recorded" /\ buildPosts = 1

MergedDraftKept == pred = "merged" /\ retryPosts = 1

ClosedDraftNoted == pred = "closed" /\ ~intent /\ retryPosts = 1

WatchedBeforeGate == watched /\ watchPosts = 1 /\ gate = "none"

WatchedAndMerged == watched /\ marked /\ Merged

WatchedButUnmergeable == watched /\ ~marked /\ gate = "pass" /\ ~mergeable /\ posts["merge"] = 1

\* Properties.

CommandsAnswered ==
    /\ \A c \in Cmds : c \in given ~> posts[c] >= 1
    /\ RetryActive ~> retryPosts >= 1
    /\ WatchActive ~> watchPosts >= 1

BuildsAnswered == BuildActive ~> buildPosts >= 1

GatedPullRequestsSettled == (gate = "pass" /\ pr # "draft") ~> MergeDone

WatchedDraftsSettled == (gate = "pass" /\ pr = "draft" /\ watched) ~> MergeDone

\* The model.

Init ==
    /\ lane = "ordinary"
    /\ fence = 0
    /\ follow = FollowInactive
    /\ given = {}
    /\ posts = [k \in Keys |-> 0]
    /\ retried = FALSE
    /\ retryPosts = 0
    /\ buildPosts = 0
    /\ runs = [k \in RunKeys |-> "none"]
    /\ runStarts = [k \in RunKeys |-> 0]
    /\ ratPushed = FALSE
    /\ codePushed = FALSE
    /\ prs = 0
    /\ pr = "none"
    /\ gate = "none"
    /\ mergeable = FALSE
    /\ merges = 0
    /\ pred = "none"
    /\ intent = FALSE
    /\ watched = FALSE
    /\ watchPosts = 0
    /\ marked = FALSE
    /\ lease = NoOne
    /\ up = [w \in Watchers |-> TRUE]
    /\ inRun = [w \in Watchers |-> "none"]
    /\ crashes = 0
    /\ unleased = {}

\* What the issue, its branch and its pull requests show every watcher.
Shared == <<given, posts, retried, retryPosts, buildPosts, runs, runStarts, ratPushed,
            codePushed, prs, pr, gate, mergeable, merges, pred, intent, watched, watchPosts,
            marked>>

\* Select the follow-up lane only before any command, lease or crash. The
\* guard is the complete initial predicate, so switching cannot abandon
\* ordinary obligations. This reaches the former follow-up initial state.
ChooseFollowup ==
    /\ Init
    /\ lane' = "followup"
    /\ follow' = FollowInitial
    /\ UNCHANGED <<Shared, fence, lease, up, inRun, crashes, unleased>>

\* A writer gives a command the issue protocol allows.
Give(c) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ c \notin given
        /\ c = "ratify" => posts["solve"] >= 1
        /\ given' = given \cup {c}
        /\ UNCHANGED <<posts, retried, retryPosts, buildPosts, runs, runStarts, ratPushed,
                       codePushed, prs, pr, gate, mergeable, merges, pred, intent, watched,
                       watchPosts, marked, lease, up, inRun, crashes, unleased>>
       )

\* A writer's retry, once the latest build was answered and stopped or left a
\* failed draft: a new build, with its own agent run, that replaces that
\* draft. After a build that stopped, the draft an earlier retry replaced is
\* still the one this retry replaces, as that retry's record says. The
\* factory takes up an issue's commands in turn, so a retry comes once every
\* command before it is answered. A draft a writer's retry watches again is
\* never replaced.
GiveRetry ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ buildPosts >= 1
        /\ pr \in {"none", "draft"}
        /\ ~watched
        /\ merges = 0
        /\ \A k \in Keys : ~Active(k)
        /\ retried' = TRUE
        /\ retryPosts' = 0
        /\ buildPosts' = 0
        /\ pred' = IF pr = "draft" THEN "draft" ELSE pred
        /\ intent' = IF pr = "draft" THEN FALSE ELSE intent
        /\ runs' = [runs EXCEPT !["build"] = "none"]
        /\ runStarts' = [runStarts EXCEPT !["build"] = 0]
        /\ inRun' = [w \in Watchers |-> IF inRun[w] = "build" THEN "stale" ELSE inRun[w]]
        /\ codePushed' = FALSE
        /\ prs' = 0
        /\ pr' = "none"
        /\ gate' = "none"
        /\ mergeable' = FALSE
        /\ UNCHANGED <<given, posts, ratPushed, merges, watched, watchPosts, marked, lease, up,
                       crashes, unleased>>
       )

\* A writer's retry that watches the latest build's failed draft again, as a
\* modeled project's is: it starts no build. Once the retry is answered and
\* CI's gate passes on the draft, the factory marks the draft ready and
\* merges it, or reports one it can't merge: GitHub refuses it, or it changes
\* the trusted base, which only a person merges. The factory takes up an
\* issue's commands in turn, so the retry comes once every command before it
\* is answered.
GiveWatch ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ buildPosts >= 1
        /\ pr = "draft"
        /\ ~watched
        /\ merges = 0
        /\ \A k \in Keys : ~Active(k)
        /\ watched' = TRUE
        /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runs, runStarts, ratPushed,
                       codePushed, prs, pr, gate, mergeable, merges, pred, intent, watchPosts,
                       marked, lease, up, inRun, crashes, unleased>>
       )

\* CI's gate passes on the latest build's pull request; GitHub may or may not
\* be able to merge it.
CIPass ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ pr \in {"draft", "ready"}
        /\ gate = "none"
        /\ gate' = "pass"
        /\ mergeable' \in BOOLEAN
        /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runs, runStarts,
                       ratPushed, codePushed, prs, pr, merges, pred, intent, watched, watchPosts,
                       marked, lease, up, inRun, crashes, unleased>>
       )

\* Someone else closes an open pull request: the latest build's, or the draft
\* a retry replaces.
OthersClose ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ \/ /\ pr \in {"draft", "ready"}
              /\ pr' = "closed"
              /\ UNCHANGED pred
           \/ /\ pred = "draft"
              /\ pred' = "closed"
              /\ UNCHANGED pr
        /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runs, runStarts,
                       ratPushed, codePushed, prs, gate, mergeable, merges, intent, watched,
                       watchPosts, marked, lease, up, inRun, crashes, unleased>>
       )

\* Someone else merges an open pull request.
OthersMerge ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ \/ /\ pr \in {"draft", "ready"}
              /\ pr' = "merged"
              /\ UNCHANGED pred
           \/ /\ pred = "draft"
              /\ pred' = "merged"
              /\ UNCHANGED pr
        /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runs, runStarts,
                       ratPushed, codePushed, prs, gate, mergeable, merges, intent, watched,
                       watchPosts, marked, lease, up, inRun, crashes, unleased>>
       )

\* Initially the watchers are interchangeable. Choose one representative only
\* for the first follow-up lease; after a crash all holders remain possible.
Acquire(w) ==
    /\ IF lane = "ordinary" THEN TRUE
       ELSE crashes > 0 \/ w = (CHOOSE v \in Watchers : TRUE)
    /\ up[w]
    /\ lease = NoOne
    /\ lease' = w
    /\ UNCHANGED <<Shared, up, inRun, crashes, unleased>>
    /\ fence' = IF lane = "ordinary" THEN fence ELSE fence + 1
    /\ UNCHANGED <<lane, follow>>

\* A lease whose holder is down is no longer renewed, and runs out.
Expire ==
    /\ lease # NoOne
    /\ ~up[lease]
    /\ lease' = NoOne
    /\ UNCHANGED <<Shared, up, inRun, crashes, unleased>>
    /\ UNCHANGED <<lane, fence, follow>>

\* A watcher crashes, between effects or during an agent run; some other watcher keeps running.
Crash(w) ==
    /\ IF lane = "ordinary" THEN TRUE
       ELSE follow.events < MaxFollowEvents /\ crashes < MaxFollowCrashes
    /\ up[w]
    /\ crashes < MaxCrashes
    /\ \E v \in Watchers \ {w} : up[v]
    /\ up' = [up EXCEPT ![w] = FALSE]
    /\ inRun' = [inRun EXCEPT ![w] = "none"]
    /\ crashes' = crashes + 1
    /\ UNCHANGED <<Shared, lease, unleased>>
    /\ UNCHANGED <<lane, fence>>
    /\ follow' = IF lane = "ordinary" THEN follow ELSE [follow EXCEPT !.events = @ + 1]

\* The holder stalls, say its laptop sleeps, past its lease; it wakes still running.
Stall(w) ==
    /\ IF lane = "ordinary" THEN TRUE
       ELSE follow.events < MaxFollowEvents /\ crashes < MaxFollowCrashes
    /\ up[w]
    /\ lease = w
    /\ crashes < MaxCrashes
    /\ lease' = NoOne
    /\ crashes' = crashes + 1
    /\ UNCHANGED <<Shared, up, inRun, unleased>>
    /\ UNCHANGED <<lane, fence>>
    /\ follow' = IF lane = "ordinary" THEN follow ELSE [follow EXCEPT !.events = @ + 1]

\* A crashed watcher restarts, remembering nothing.
Restart(w) ==
    /\ ~up[w]
    /\ lease # w
    /\ up' = [up EXCEPT ![w] = TRUE]
    /\ UNCHANGED <<Shared, lease, inRun, crashes, unleased>>
    /\ UNCHANGED <<lane, fence, follow>>

\* A watcher may take an effect: it is running, holds the lease, and isn't waiting on a run.
Holding(w) == up[w] /\ lease = w /\ inRun[w] = "none"

EffectVars == <<given, retried, gate, mergeable, watched, lease, up, crashes, unleased>>

\* Record the agent run, then start it: the draft's, or the latest build's.
StartRun(w, k) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ \/ k = "solve" /\ Active("solve")
           \/ k = "build" /\ BuildActive
        /\ runs[k] = "none"
        /\ runs' = [runs EXCEPT ![k] = "recorded"]
        /\ runStarts' = [runStarts EXCEPT ![k] = @ + 1]
        /\ inRun' = [inRun EXCEPT ![w] = k]
        /\ UNCHANGED <<posts, retryPosts, buildPosts, ratPushed, codePushed, prs, pr, merges,
                       pred, intent>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars
       )

\* A run recorded and never finished stopped partway: say so, and start no other.
ReportStopped(w, k) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ runs[k] = "recorded"
        /\ \/ /\ k = "solve"
              /\ Active("solve")
              /\ posts' = [posts EXCEPT !["solve"] = @ + 1]
              /\ UNCHANGED buildPosts
           \/ /\ k = "build"
              /\ BuildActive
              /\ buildPosts' = buildPosts + 1
              /\ UNCHANGED posts
        /\ UNCHANGED <<retryPosts, runs, runStarts, ratPushed, codePushed, prs, pr, merges,
                       pred, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars
       )

PushRatification(w) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ Active("ratify")
        /\ ~ratPushed
        /\ ratPushed' = TRUE
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, codePushed, prs, pr,
                       merges, pred, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars
       )

\* Before closing the draft a retry replaces, store the retry's record that
\* it replaces it, where every watcher can read it.
RecordIntent(w) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ RetryActive
        /\ pred = "draft"
        /\ ~intent
        /\ intent' = TRUE
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       prs, pr, merges, pred, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars
       )

\* Close the draft the retry replaces, as its record says.
Retire(w) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ RetryActive
        /\ pred = "draft"
        /\ intent
        /\ pred' = "retired"
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       prs, pr, merges, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars
       )

PushCode(w) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ BuildActive
        /\ runs["build"] = "done"
        /\ ~codePushed
        /\ codePushed' = TRUE
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, prs, pr,
                       merges, pred, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars
       )

\* Open the latest build's pull request: ready when its checks and review
\* passed, and a draft when they didn't.
OpenPullRequest(w, ready) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ BuildActive
        /\ runs["build"] = "done"
        /\ codePushed
        /\ prs = 0
        /\ prs' = prs + 1
        /\ pr' = IF ready THEN "ready" ELSE "draft"
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       merges, pred, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars
       )

MergePullRequest(w) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ Active("merge")
        /\ mergeable
        /\ pr = "ready"
        /\ merges = 0
        /\ merges' = merges + 1
        /\ pr' = "merged"
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       prs, pred, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars
       )

\* The post that answers step k, once no answer is there yet.
Post(w, k) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ Active(k)
        /\ EffectsDone(k)
        /\ posts' = [posts EXCEPT ![k] = @ + 1]
        /\ UNCHANGED <<retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed, prs, pr,
                       merges, pred, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars
       )

\* Answer the latest retry, once the draft it replaces is closed or merged.
AnswerRetry(w) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ RetryActive
        /\ pred # "draft"
        /\ retryPosts' = retryPosts + 1
        /\ UNCHANGED <<posts, buildPosts, runs, runStarts, ratPushed, codePushed, prs, pr,
                       merges, pred, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars
       )

\* Answer the latest build with its pull request.
PostBuild(w) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ BuildActive
        /\ runs["build"] = "done"
        /\ codePushed
        /\ prs >= 1
        /\ buildPosts' = buildPosts + 1
        /\ UNCHANGED <<posts, retryPosts, runs, runStarts, ratPushed, codePushed, prs, pr,
                       merges, pred, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars
       )

\* Answer a writer's retry that watches the latest build's failed draft
\* again: the factory watches it, and says what follows once CI's gate passes.
AnswerWatch(w) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ WatchActive
        /\ watchPosts' = watchPosts + 1
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed, prs,
                       pr, merges, pred, intent, inRun, marked>>
        /\ UNCHANGED EffectVars
       )

\* Mark the watched draft ready, once CI's gate passed on it and the factory
\* can merge it, so it merges as a ready pull request does.
MarkReady(w) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ Holding(w)
        /\ Active("merge")
        /\ pr = "draft"
        /\ mergeable
        /\ pr' = "ready"
        /\ marked' = TRUE
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed, prs,
                       merges, pred, intent, inRun, watchPosts>>
        /\ UNCHANGED EffectVars
       )

OrdinaryAct(w) ==
    \/ \E k \in RunKeys : StartRun(w, k) \/ ReportStopped(w, k)
    \/ PushRatification(w)
    \/ RecordIntent(w)
    \/ Retire(w)
    \/ PushCode(w)
    \/ \E ready \in BOOLEAN : OpenPullRequest(w, ready)
    \/ MergePullRequest(w)
    \/ \E k \in Keys : Post(w, k)
    \/ AnswerRetry(w)
    \/ PostBuild(w)
    \/ AnswerWatch(w)
    \/ MarkReady(w)

\* The agent run finishes and the holder records it. A run a retry made stale
\* is recorded as its own, which no longer counts for the latest build.
Act(w) ==
    \/ /\ lane = "ordinary"
       /\ OrdinaryAct(w)
    \/ /\ lane = "followup"
       /\ FollowAct(w)

FinishRun(w) ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ up[w]
        /\ lease = w
        /\ inRun[w] # "none"
        /\ runs' = IF inRun[w] = "stale" THEN runs ELSE [runs EXCEPT ![inRun[w]] = "done"]
        /\ inRun' = [inRun EXCEPT ![w] = "none"]
        /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runStarts, ratPushed,
                       codePushed, prs, pr, gate, mergeable, merges, pred, intent, watched,
                       watchPosts, marked, lease, up, crashes, unleased>>
       )

\* A watcher that finds it lost the lease drops its run without recording anything.
DropRun(w) ==
    /\ up[w]
    /\ lease # w
    /\ inRun[w] # "none"
    /\ inRun' = [inRun EXCEPT ![w] = "none"]
    /\ UNCHANGED <<Shared, lease, up, crashes, unleased>>
    /\ UNCHANGED <<lane, fence, follow>>

\* Nothing is left to answer.
Rest ==
    /\ IF lane = "ordinary" THEN TRUE ELSE follow.phase \in {"watch", "hold", "done", "uncertain"}
    /\ \A k \in Keys : ~Active(k)
    /\ ~RetryActive
    /\ ~WatchActive
    /\ ~BuildActive
    /\ UNCHANGED vars
    /\ UNCHANGED <<lane, fence, follow>>

Next ==
    \/ ChooseFollowup
    \/ \E c \in Cmds : Give(c)
    \/ GiveRetry
    \/ GiveWatch
    \/ CIPass
    \/ OthersClose
    \/ OthersMerge
    \/ Expire
    \/ \E w \in Watchers :
        \/ Acquire(w) \/ Crash(w) \/ Stall(w) \/ Restart(w)
        \/ Act(w) \/ FinishRun(w) \/ DropRun(w)
    \/ FollowOutside
    \/ Rest

\* Fairness.

FairAcquire == \A w \in Watchers : WF_vars(Acquire(w))

FairExpire == WF_vars(Expire)

FairAct == \A w \in Watchers : WF_vars(Act(w))

FairFinishRun == \A w \in Watchers : WF_vars(FinishRun(w))

FairDropRun == \A w \in Watchers : WF_vars(DropRun(w))

\* Known bugs, left out of Next.

\* A watcher posts its answer without looking for one already there.
PostWithoutLooking ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers, k \in Keys :
            /\ Holding(w)
            /\ Triggered(k)
            /\ EffectsDone(k)
            /\ posts[k] < 2
            /\ posts' = [posts EXCEPT ![k] = @ + 1]
            /\ UNCHANGED <<retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed, prs,
                           pr, merges, pred, intent, inRun>>
            /\ UNCHANGED <<watchPosts, marked>>
            /\ UNCHANGED EffectVars
       )

\* A watcher that finds a run that stopped partway starts it again.
RerunStoppedRun ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers, k \in RunKeys :
            /\ Holding(w)
            /\ \/ k = "solve" /\ Active("solve")
               \/ k = "build" /\ BuildActive
            /\ runs[k] = "recorded"
            /\ runStarts[k] < 2
            /\ runStarts' = [runStarts EXCEPT ![k] = @ + 1]
            /\ inRun' = [inRun EXCEPT ![w] = k]
            /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, ratPushed, codePushed, prs, pr,
                           merges, pred, intent>>
            /\ UNCHANGED <<watchPosts, marked>>
            /\ UNCHANGED EffectVars
       )

\* A watcher that woke after its lease ran out posts without checking the lease.
PostWithoutLease ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers, k \in Keys :
            /\ up[w]
            /\ lease # w
            /\ inRun[w] = "none"
            /\ Active(k)
            /\ EffectsDone(k)
            /\ posts' = [posts EXCEPT ![k] = @ + 1]
            /\ unleased' = unleased \cup {w}
            /\ UNCHANGED <<given, retried, gate, mergeable, watched, lease, up, crashes>>
            /\ UNCHANGED <<retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed, prs,
                           pr, merges, pred, intent, inRun, watchPosts, marked>>
       )

\* A watcher finds no open pull request for the latest build, misses the one
\* it opened that was since closed or merged, and opens another.
OpenWithoutLookingClosed ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers :
            /\ Holding(w)
            /\ BuildActive
            /\ runs["build"] = "done"
            /\ codePushed
            /\ pr \in {"none", "closed", "merged"}
            /\ prs < 2
            /\ prs' = prs + 1
            /\ pr' = "draft"
            /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                           merges, pred, intent, inRun>>
            /\ UNCHANGED <<watchPosts, marked>>
            /\ UNCHANGED EffectVars
       )

\* A retry's build opens its pull request while the draft it replaces is
\* still open.
OpenBeforeRetiring ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers :
            /\ Holding(w)
            /\ RetryActive
            /\ pred = "draft"
            /\ prs = 0
            /\ prs' = 1
            /\ pr' = "ready"
            /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                           merges, pred, intent, inRun>>
            /\ UNCHANGED <<watchPosts, marked>>
            /\ UNCHANGED EffectVars
       )

\* The factory closes the draft before storing the retry's record, so after a
\* crash its own close looks like someone else's.
RetireWithoutRecord ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers :
            /\ Holding(w)
            /\ RetryActive
            /\ pred = "draft"
            /\ pred' = "retired"
            /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                           prs, pr, merges, intent, inRun>>
            /\ UNCHANGED <<watchPosts, marked>>
            /\ UNCHANGED EffectVars
       )

\* A retry whose draft someone merged builds again anyway.
ReplaceMergedDraft ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers :
            /\ Holding(w)
            /\ retried
            /\ retryPosts >= 1
            /\ pred = "merged"
            /\ buildPosts = 0
            /\ runs["build"] = "none"
            /\ runs' = [runs EXCEPT !["build"] = "recorded"]
            /\ runStarts' = [runStarts EXCEPT !["build"] = @ + 1]
            /\ inRun' = [inRun EXCEPT ![w] = "build"]
            /\ UNCHANGED <<posts, retryPosts, buildPosts, ratPushed, codePushed, prs, pr, merges,
                           pred, intent>>
            /\ UNCHANGED <<watchPosts, marked>>
            /\ UNCHANGED EffectVars
       )

\* A writer's retry keeps CI's result from the draft it replaces.
KeepGateAcrossRetry ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        /\ buildPosts >= 1
        /\ pr \in {"none", "draft"}
        /\ ~watched
        /\ merges = 0
        /\ \A k \in Keys : ~Active(k)
        /\ retried' = TRUE
        /\ retryPosts' = 0
        /\ buildPosts' = 0
        /\ pred' = IF pr = "draft" THEN "draft" ELSE pred
        /\ intent' = IF pr = "draft" THEN FALSE ELSE intent
        /\ runs' = [runs EXCEPT !["build"] = "none"]
        /\ runStarts' = [runStarts EXCEPT !["build"] = 0]
        /\ inRun' = [w \in Watchers |-> IF inRun[w] = "build" THEN "stale" ELSE inRun[w]]
        /\ codePushed' = FALSE
        /\ prs' = 0
        /\ pr' = "none"
        /\ UNCHANGED <<given, posts, ratPushed, merges, gate, mergeable, watched, watchPosts,
                       marked, lease, up, crashes, unleased>>
       )

\* A watcher whose agent run a retry made stale records it as the latest
\* build's.
FinishStaleRun ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers :
            /\ up[w]
            /\ lease = w
            /\ inRun[w] = "stale"
            /\ runs' = [runs EXCEPT !["build"] = "done"]
            /\ inRun' = [inRun EXCEPT ![w] = "none"]
            /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runStarts, ratPushed,
                           codePushed, prs, pr, gate, mergeable, merges, pred, intent, watched,
                           watchPosts, marked, lease, up, crashes, unleased>>
       )

\* A watcher that woke after its lease ran out closes the draft without
\* checking the lease.
CloseWithoutLease ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers :
            /\ up[w]
            /\ lease # w
            /\ inRun[w] = "none"
            /\ RetryActive
            /\ pred = "draft"
            /\ intent
            /\ pred' = "retired"
            /\ unleased' = unleased \cup {w}
            /\ UNCHANGED <<given, retried, gate, mergeable, watched, lease, up, crashes>>
            /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                           prs, pr, merges, intent, inRun, watchPosts, marked>>
       )

\* The factory marks a failed draft ready when no writer's retry watches it
\* again.
MarkReadyWithoutWatch ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers :
            /\ Holding(w)
            /\ pr = "draft"
            /\ ~watched
            /\ gate = "pass"
            /\ mergeable
            /\ pr' = "ready"
            /\ marked' = TRUE
            /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                           prs, merges, pred, intent, inRun, watchPosts>>
            /\ UNCHANGED EffectVars
       )

\* The factory marks a watched draft ready before CI's gate passes on it.
MarkReadyBeforeGate ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers :
            /\ Holding(w)
            /\ pr = "draft"
            /\ watched
            /\ watchPosts >= 1
            /\ gate = "none"
            /\ pr' = "ready"
            /\ marked' = TRUE
            /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                           prs, merges, pred, intent, inRun, watchPosts>>
            /\ UNCHANGED EffectVars
       )

\* The factory marks ready a watched draft it can't merge, such as one that
\* changes the trusted base.
MarkUnmergeableReady ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers :
            /\ Holding(w)
            /\ pr = "draft"
            /\ watched
            /\ watchPosts >= 1
            /\ gate = "pass"
            /\ ~mergeable
            /\ pr' = "ready"
            /\ marked' = TRUE
            /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                           prs, merges, pred, intent, inRun, watchPosts>>
            /\ UNCHANGED EffectVars
       )

\* A watcher answers a retry that watches a failed draft again without
\* looking for an answer already there.
AnswerWatchWithoutLooking ==
    /\ lane = "ordinary"
    /\ UNCHANGED <<lane, fence, follow>>
    /\ (
        \E w \in Watchers :
            /\ Holding(w)
            /\ watched
            /\ watchPosts < 2
            /\ watchPosts' = watchPosts + 1
            /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                           prs, pr, merges, pred, intent, inRun, marked>>
            /\ UNCHANGED EffectVars
       )

Spec == Init /\ [][Next]_vars
====

