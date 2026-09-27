---- MODULE IssueProtocol ----
EXTENDS FiniteSets

CONSTANTS Actors, Writers, Bots, Questions, Proposals, Heads, NoP, NoHead, NoActor

VARIABLES state, open, proposal, amends, base, ratified, ratifiedBase,
          head, gate, prLock, scope, mergedBy, mergedHead, directedBy

vars == <<state, open, proposal, amends, base, ratified, ratifiedBase,
          head, gate, prLock, scope, mergedBy, mergedHead, directedBy>>

Kinds == {"none", "asked", "proposed", "stuck", "unsupported",
          "ratified", "pr_open", "failed", "merged", "closed"}

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

OnlyDirectorsDirect == directedBy \in Directors \cup {NoActor}

RatifiesCurrentProposal ==
    state \in Building => (ratified # NoP /\ ratified = proposal)

NoOpenQuestionsWhenRatified == state \in Building => open = {}

AmendsHeldLock == state \in Building => ratifiedBase = amends

MergedGatePassed == FactoryMerged => gate[mergedHead] = "pass"

MergedCurrentHead == FactoryMerged => mergedHead = head

MergedOneProject == FactoryMerged => scope = "one"

MergedLockRatified == FactoryMerged => prLock = ratified

\* Witnesses.
QuestionsAsked == state = "asked"

GotStuck == state = "stuck"

FoundUnsupported == state = "unsupported"

PullRequestFailed == state = "failed"

OthersClosed == state = "closed"

OthersMerged == state = "merged" /\ mergedBy = "other"

FactoryMerges == FactoryMerged

AmendmentMerges == FactoryMerged /\ ratifiedBase # NoP

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

ResetPR ==
    /\ head' = NoHead
    /\ gate' = [h \in Heads |-> "none"]
    /\ prLock' = NoP
    /\ scope' = "one"
    /\ mergedBy' = "nobody"
    /\ mergedHead' = NoHead

\* The factory drafts: it asks questions, proposes statements against the
\* base branch's current lock, gets stuck, or finds the issue unsupported.
Draft ==
    /\ ResetPR
    /\ ratified' = NoP
    /\ ratifiedBase' = NoP
    /\ UNCHANGED base
    /\ \/ \E Q \in (SUBSET Questions) \ {{}} :
            state' = "asked" /\ open' = Q /\ proposal' = NoP /\ amends' = NoP
       \/ \E p \in Proposals :
            state' = "proposed" /\ open' = {} /\ proposal' = p /\ amends' = base
       \/ \E k \in {"stuck", "unsupported"} :
            state' = k /\ open' = {} /\ proposal' = NoP /\ amends' = NoP

Solve(a) ==
    /\ a \in Directors
    /\ state \in {"none", "stuck", "unsupported", "closed"}
    /\ directedBy' = a
    /\ Draft

Revise(a) ==
    /\ a \in Directors
    /\ state \in {"asked", "proposed", "stuck", "unsupported", "closed"}
    /\ directedBy' = a
    /\ Draft

Choose(a, q) ==
    /\ a \in Directors
    /\ state = "asked"
    /\ q \in open
    /\ directedBy' = a
    /\ IF open = {q}
          THEN Draft
          ELSE /\ open' = open \ {q}
               /\ UNCHANGED <<state, proposal, amends, base, ratified, ratifiedBase,
                              head, gate, prLock, scope, mergedBy, mergedHead>>

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
                   head, gate, prLock, scope, mergedBy, mergedHead>>

Build ==
    /\ state = "ratified"
    /\ \E h \in Heads :
        /\ state' = "pr_open"
        /\ head' = h
        /\ gate' = [gate EXCEPT ![h] = "pending"]
        /\ prLock' = ratified
        /\ scope' = "one"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   mergedBy, mergedHead, directedBy>>

\* Someone pushes a new head commit to the pull request, with any content.
Push(h, l, s) ==
    /\ state \in {"pr_open", "failed"}
    /\ h # head
    /\ head' = h
    /\ gate' = [gate EXCEPT ![h] = "pending"]
    /\ prLock' = l
    /\ scope' = s
    /\ UNCHANGED <<state, open, proposal, amends, base, ratified, ratifiedBase,
                   mergedBy, mergedHead, directedBy>>

CIGate ==
    /\ state \in {"pr_open", "failed"}
    /\ gate[head] = "pending"
    /\ \E r \in {"pass", "fail"} : gate' = [gate EXCEPT ![head] = r]
    /\ UNCHANGED <<state, open, proposal, amends, base, ratified, ratifiedBase,
                   head, prLock, scope, mergedBy, mergedHead, directedBy>>

NoticeFail ==
    /\ state = "pr_open"
    /\ gate[head] = "fail"
    /\ state' = "failed"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy>>

Retry(a) ==
    /\ a \in Directors
    /\ state = "failed"
    /\ state' = "pr_open"
    /\ directedBy' = a
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
                   head, gate, prLock, scope, directedBy>>

OthersMerge ==
    /\ state \in {"pr_open", "failed"}
    /\ state' = "merged"
    /\ mergedBy' = "other"
    /\ mergedHead' = head
    /\ base' = prLock
    /\ UNCHANGED <<open, proposal, amends, ratified, ratifiedBase,
                   head, gate, prLock, scope, directedBy>>

OthersClose ==
    /\ state \in {"pr_open", "failed"}
    /\ state' = "closed"
    /\ UNCHANGED <<open, proposal, amends, base, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy>>

\* The base branch's lock changes elsewhere.
BaseMoves(l) ==
    /\ l # base
    /\ base' = l
    /\ UNCHANGED <<state, open, proposal, amends, ratified, ratifiedBase,
                   head, gate, prLock, scope, mergedBy, mergedHead, directedBy>>

Finished == state = "merged" /\ UNCHANGED vars

Next ==
    \/ \E a \in Actors :
        \/ Solve(a)
        \/ Revise(a)
        \/ Retry(a)
        \/ \E q \in Questions : Choose(a, q)
        \/ \E p \in Proposals : Ratify(a, p)
    \/ Build
    \/ \E h \in Heads, l \in Locks, s \in {"one", "many"} : Push(h, l, s)
    \/ CIGate
    \/ NoticeFail
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
                       head, gate, prLock, scope, mergedBy, mergedHead>>

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
                       head, gate, prLock, scope, mergedBy, mergedHead>>

\* An amendment is ratified after the base branch's lock has moved.
RatifyOverMovedLock ==
    \E a \in Directors :
        /\ state = "proposed"
        /\ state' = "ratified"
        /\ ratified' = proposal
        /\ ratifiedBase' = base
        /\ directedBy' = a
        /\ UNCHANGED <<open, proposal, amends, base,
                       head, gate, prLock, scope, mergedBy, mergedHead>>

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
                       head, gate, prLock, scope, directedBy>>

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
                   head, gate, prLock, scope, directedBy>>

Spec == Init /\ [][Next]_vars
====
