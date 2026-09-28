---- MODULE IssueProtocol ----
EXTENDS Naturals, FiniteSets

CONSTANTS Actors, Writers, Bots, Questions, Proposals, Heads, NoP, NoHead, NoActor

VARIABLES state, open, proposal, amends, base, ratified, ratifiedBase,
          head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
          stops, failure

vars == <<state, open, proposal, amends, base, ratified, ratifiedBase,
          head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
          stops, failure>>

Kinds == {"none", "asked", "proposed", "stuck", "unsupported",
          "ratified", "pr_open", "failed", "merged", "closed"}

\* Why the issue's latest failure happened.
Failures == {"none", "stopped", "limit", "gate", "ci", "unmergeable"}

\* Builds of an issue that may stop partway before the factory refuses another.
MaxStops == 2

\* Only people with write access who are not bots direct the factory.
Directors == Writers \ Bots

Locks == Proposals \cup {NoP}

\* States in which a ratified proposal is being built or has been merged.
Building == {"ratified", "pr_open", "failed", "merged"}

FactoryMerged == state = "merged" /\ mergedBy = "factory"

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

\* The draft model.
Init ==
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
                              stops, failure>>

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
                   stops, failure>>

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
                   mergedBy, mergedHead, directedBy, stops, failure>>

\* The build starts, then stops before it makes a pull request.
StopBuild ==
    /\ state = "ratified"
    /\ stops < MaxStops
    /\ state' = "failed"
    /\ stops' = stops + 1
    /\ failure' = "stopped"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy>>

\* Two builds have already stopped partway, so the factory won't start another.
RefuseBuild ==
    /\ state = "ratified"
    /\ stops = MaxStops
    /\ state' = "failed"
    /\ failure' = "limit"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
                   stops>>

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
                   mergedBy, mergedHead, directedBy, stops>>

\* Someone pushes a new head commit to the pull request, with any content.
Push(h, l, s) ==
    /\ state \in {"pr_open", "failed"}
    /\ head # NoHead
    /\ h # head
    /\ head' = h
    /\ gate' = [gate EXCEPT ![h] = "pending"]
    /\ prLock' = l
    /\ scope' = s
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
                   stops, failure>>

NoticeFail ==
    /\ state = "pr_open"
    /\ gate[head] = "fail"
    /\ state' = "failed"
    /\ failure' = "ci"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
                   stops>>

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
                   stops>>

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
                   head, gate, prLock, scope, mergedBy, mergedHead>>

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
                   head, gate, prLock, scope, directedBy, stops, failure>>

OthersMerge ==
    /\ state \in {"pr_open", "failed"}
    /\ head # NoHead
    /\ state' = "merged"
    /\ mergedBy' = "other"
    /\ mergedHead' = head
    /\ base' = prLock
    /\ failure' = "none"
    /\ UNCHANGED <<open, proposal, amends, ratified, ratifiedBase,
                   head, gate, prLock, scope, directedBy, stops>>

OthersClose ==
    /\ state \in {"pr_open", "failed"}
    /\ head # NoHead
    /\ state' = "closed"
    /\ failure' = "none"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
                   stops>>

\* The base branch's lock changes elsewhere.
BaseMoves(l) ==
    /\ l # base
    /\ base' = l
    /\ UNCHANGED <<state, open, proposal, amends, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy,
                   stops, failure>>

Finished == state = "merged" /\ UNCHANGED vars

Next ==
    \/ \E a \in Actors :
        \/ \E d \in Drafts : Solve(a, d) \/ Revise(a, d)
        \/ Retry(a)
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
    \/ OthersMerge
    \/ OthersClose
    \/ \E l \in Locks : BaseMoves(l)
    \/ Finished

\* Known bugs.

\* A bot or someone without write access gets the factory to take the issue.
ObeyAnyone ==
    \E a \in Actors \ Directors :
        /\ state \in {"none", "stuck", "unsupported", "closed"}
        /\ state' = "stuck"
        /\ directedBy' = a
        /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                       head, gate, prLock, scope, mergedBy, mergedHead,
                       stops, failure>>

\* A ratify naming an earlier proposal's hash is accepted.
RatifyEarlier ==
    \E a \in Directors, p \in Proposals \ {proposal} :
        /\ state = "proposed"
        /\ base = amends
        /\ state' = "ratified"
        /\ ratified' = p
        /\ ratifiedBase' = base
        /\ directedBy' = a
        /\ UNCHANGED <<open, proposal, amends, base,
                       head, gate, prLock, scope, mergedBy, mergedHead,
                       stops, failure>>

\* An amendment is ratified after the base branch's lock has moved.
RatifyOverMovedLock ==
    \E a \in Directors :
        /\ state = "proposed"
        /\ state' = "ratified"
        /\ ratified' = proposal
        /\ ratifiedBase' = base
        /\ directedBy' = a
        /\ UNCHANGED <<open, proposal, amends, base,
                       head, gate, prLock, scope, mergedBy, mergedHead,
                       stops, failure>>

\* The factory merges whichever head passed CI, not the current one.
MergeOnAnyPass ==
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
                       head, gate, prLock, scope, directedBy, stops, failure>>

\* The factory merges without checking the project's lock.
MergeIgnoringLock ==
    /\ state = "pr_open"
    /\ gate[head] = "pass"
    /\ scope = "one"
    /\ state' = "merged"
    /\ mergedBy' = "factory"
    /\ mergedHead' = head
    /\ base' = prLock
    /\ UNCHANGED <<open, proposal, amends, ratified, ratifiedBase,
                   head, gate, prLock, scope, directedBy, stops, failure>>

\* The factory starts a third build after two have stopped partway.
StartPastLimit ==
    /\ state = "ratified"
    /\ stops = MaxStops
    /\ state' = "failed"
    /\ stops' = stops + 1
    /\ failure' = "stopped"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy>>

\* The factory merges a draft pull request whose own gate failed, before
\* anyone says retry.
MergeFailedBuild ==
    /\ state = "failed"
    /\ head # NoHead
    /\ state' = "merged"
    /\ mergedBy' = "factory"
    /\ mergedHead' = head
    /\ base' = prLock
    /\ UNCHANGED <<open, proposal, amends, ratified, ratifiedBase,
                   head, gate, prLock, scope, directedBy, stops, failure>>

Spec == Init /\ [][Next]_vars
====
