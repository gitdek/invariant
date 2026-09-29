---- MODULE DecisionJournal ----
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Checkouts, People, Agents, MaxId, MaxLen, MaxWrites

VARIABLES files, main, base, store, pend, decides, mainLog, storeLog

vars == <<files, main, base, store, pend, decides, mainLog, storeLog>>

Writers == People \cup Agents

Ids == 1..MaxId

Ops == {"decide", "propose", "ratify", "supersede", "link"}

Doors == {"one", "two", "none"}

Lines == [op : Ops, by : Writers, door : Doors]

Records == [id : Ids, line : Lines]

Pending == [c : Checkouts, id : Ids, line : Lines, todo : {"file", "store"}]

Journals == [Ids -> Seq(Lines)]

EmptyJournals == [i \in Ids |-> <<>>]

IsPrefix(s, t) == Len(s) <= Len(t) /\ SubSeq(t, 1, Len(s)) = s

Step(st, l) ==
    CASE l.op = "decide" -> "decided"
      [] l.op = "propose" -> "proposed"
      [] l.op = "ratify" -> "ratified"
      [] l.op = "supersede" -> "superseded"
      [] OTHER -> st

Max(S) == IF S = {} THEN 0 ELSE CHOOSE m \in S : \A x \in S : x <= m

\* A decision's state is its lines, folded in order with Step: only a link
\* leaves the state as it was, so the fold ends at the last line that isn't a link.
StateOf(s) ==
    LET ks == {k \in 1..Len(s) : s[k].op # "link"}
    IN IF ks = {} THEN "none" ELSE Step("none", s[Max(ks)])

WellFormed(s) ==
    IF Len(s) = 0 THEN TRUE
    ELSE /\ s[1].op \in {"decide", "propose"}
         /\ \A k \in 2..Len(s) :
              /\ s[k].op \notin {"decide", "propose"}
              /\ s[k].op = "ratify" =>
                   \A e \in 1..(k - 1) : s[e].op \notin {"ratify", "supersede"}

Triples(j) == UNION {{<<i, k, j[i][k]>> : k \in 1..Len(j[i])} : i \in Ids}

StoreIds == {store[k].id : k \in 1..Len(store)}

\* ---------------------------------------------------------------- statements

TypeOK ==
    /\ files \in [Checkouts -> Journals]
    /\ main \in Journals
    /\ base \in [Checkouts -> Journals]
    /\ store \in Seq(Records)
    /\ pend \subseteq Pending /\ Cardinality(pend) <= 1
    /\ decides \in [Ids -> Nat]
    /\ \A t \in mainLog : t[1] \in Ids /\ t[2] \in Nat /\ t[3] \in Lines
    /\ \A t \in storeLog : t[1] \in Nat /\ t[2] \in Records

UniqueIds == \A i \in Ids : decides[i] <= 1

MainKeepsLines ==
    \A t \in mainLog : t[2] <= Len(main[t[1]]) /\ main[t[1]][t[2]] = t[3]

StoreKeepsLines ==
    \A t \in storeLog : t[1] <= Len(store) /\ store[t[1]] = t[2]

RatifiedByPerson ==
    \A i \in Ids : \A k \in 1..Len(main[i]) :
        main[i][k].op = "ratify" => main[i][k].by \in People

NoAgentOneWayDoor ==
    \A i \in Ids : \A k \in 1..Len(main[i]) :
        (main[i][k].op = "decide" /\ main[i][k].door = "one") => main[i][k].by \in People

JournalsWellFormed ==
    \A i \in Ids :
        /\ WellFormed(main[i])
        /\ \A c \in Checkouts : WellFormed(files[c][i])

RatifiedOnMain == \E i \in Ids : StateOf(main[i]) = "ratified"

AgentProposalRatifiedOnMain ==
    \E i \in Ids :
        /\ Len(main[i]) > 0
        /\ main[i][1].op = "propose"
        /\ main[i][1].by \in Agents
        /\ StateOf(main[i]) = "ratified"

SupersededOnMain == \E i \in Ids : StateOf(main[i]) = "superseded"

BothBranchesOnOneDecision == \E i \in Ids : Len(main[i]) > MaxLen

GapInIds ==
    /\ pend = {}
    /\ \E i \in StoreIds :
        /\ Len(main[i]) = 0
        /\ \A c \in Checkouts : Len(files[c][i]) = 0
        /\ \E j \in Ids : j > i /\ Len(main[j]) > 0

\* ---------------------------------------------------------------- model

Init ==
    /\ files = [c \in Checkouts |-> EmptyJournals]
    /\ main = EmptyJournals
    /\ base = [c \in Checkouts |-> EmptyJournals]
    /\ store = <<>>
    /\ pend = {}
    /\ decides = [i \in Ids |-> 0]
    /\ mainLog = {}
    /\ storeLog = {}

CheckoutIds(c) == {i \in Ids : Len(files[c][i]) > 0}

NextId(c) == 1 + Max(StoreIds \cup CheckoutIds(c))

DecideLines(w) ==
    IF w \in People
    THEN {[op |-> "decide", by |-> w, door |-> d] : d \in {"one", "two"}}
    ELSE {[op |-> "decide", by |-> w, door |-> "two"],
          [op |-> "propose", by |-> w, door |-> "one"]}

Free == pend = {} /\ Len(store) < MaxWrites

NotBusy(c) == \A p \in pend : p.c # c

Record(rec) ==
    /\ store' = Append(store, rec)
    /\ storeLog' = storeLog \cup {<<Len(store) + 1, rec>>}

New(c, i) == SubSeq(files[c][i], Len(base[c][i]) + 1, Len(files[c][i]))

Taken(c) == [i \in Ids |-> main[i] \o New(c, i)]

\* The store records the new ID first; the file is written by Finish.
Decide(c, w, l) ==
    /\ Free
    /\ l \in DecideLines(w)
    /\ NextId(c) <= MaxId
    /\ Record([id |-> NextId(c), line |-> l])
    /\ pend' = {[c |-> c, id |-> NextId(c), line |-> l, todo |-> "file"]}
    /\ decides' = [decides EXCEPT ![NextId(c)] = @ + 1]
    /\ UNCHANGED <<files, main, base, mainLog>>

\* The line goes to the file first; the store records it in Finish.
Amend(c, i, l) ==
    /\ Free
    /\ Len(files[c][i]) > 0
    /\ Len(files[c][i]) < MaxLen
    /\ files' = [files EXCEPT ![c][i] = Append(@, l)]
    /\ pend' = {[c |-> c, id |-> i, line |-> l, todo |-> "store"]}
    /\ UNCHANGED <<main, base, store, decides, mainLog, storeLog>>

Ratify(c, p, i) ==
    /\ p \in People
    /\ StateOf(files[c][i]) \notin {"ratified", "superseded"}
    /\ Amend(c, i, [op |-> "ratify", by |-> p, door |-> "none"])

Supersede(c, w, i) == Amend(c, i, [op |-> "supersede", by |-> w, door |-> "none"])

Link(c, w, i) == Amend(c, i, [op |-> "link", by |-> w, door |-> "none"])

Finish ==
    \E p \in pend :
        /\ IF p.todo = "file"
           THEN /\ files' = [files EXCEPT ![p.c][p.id] = <<p.line>>]
                /\ UNCHANGED <<store, storeLog>>
           ELSE /\ Record([id |-> p.id, line |-> p.line])
                /\ UNCHANGED files
        /\ pend' = {}
        /\ UNCHANGED <<main, base, decides, mainLog>>

\* The process stops between its two effects.
Stop ==
    /\ pend # {}
    /\ pend' = {}
    /\ UNCHANGED <<files, main, base, store, decides, mainLog, storeLog>>

Merge(c) ==
    /\ NotBusy(c)
    /\ \A i \in Ids : IsPrefix(main[i], files[c][i])
    /\ main # files[c]
    /\ main' = files[c]
    /\ base' = [base EXCEPT ![c] = files[c]]
    /\ mainLog' = mainLog \cup Triples(files[c])
    /\ UNCHANGED <<files, store, pend, decides, storeLog>>

\* The branch takes main in: main's lines first, then the branch's new ones.
Sync(c) ==
    /\ NotBusy(c)
    /\ base[c] # main
    /\ \A i \in Ids : WellFormed(Taken(c)[i])
    /\ files' = [files EXCEPT ![c] = Taken(c)]
    /\ base' = [base EXCEPT ![c] = main]
    /\ UNCHANGED <<main, store, pend, decides, mainLog, storeLog>>

\* A ratify that would conflict with main is dropped from the branch; the store keeps it.
Drop(c, i) ==
    /\ NotBusy(c)
    /\ ~WellFormed(Taken(c)[i])
    /\ files' = [files EXCEPT ![c][i] =
                   base[c][i] \o SelectSeq(New(c, i), LAMBDA l : l.op # "ratify")]
    /\ UNCHANGED <<main, base, store, pend, decides, mainLog, storeLog>>

Idle == pend = {} /\ UNCHANGED vars

Next ==
    \/ \E c \in Checkouts, w \in Writers, l \in Lines : Decide(c, w, l)
    \/ \E c \in Checkouts, w \in Writers, i \in Ids :
         Ratify(c, w, i) \/ Supersede(c, w, i) \/ Link(c, w, i)
    \/ Finish
    \/ Stop
    \/ \E c \in Checkouts : Merge(c) \/ Sync(c)
    \/ \E c \in Checkouts, i \in Ids : Drop(c, i)
    \/ Idle

\* ---------------------------------------------------------------- known bugs

\* Decide writes the file first, and the process stops before the store records the ID.
DecideFileFirst ==
    \E c \in Checkouts, w \in Writers, l \in Lines :
        /\ pend = {}
        /\ l \in DecideLines(w)
        /\ NextId(c) <= MaxId
        /\ files' = [files EXCEPT ![c][NextId(c)] = <<l>>]
        /\ decides' = [decides EXCEPT ![NextId(c)] = @ + 1]
        /\ UNCHANGED <<main, base, store, pend, mainLog, storeLog>>

\* A branch merges without checking that main's lines are still in it.
MergeOverwrite ==
    \E c \in Checkouts :
        /\ NotBusy(c)
        /\ main # files[c]
        /\ main' = files[c]
        /\ base' = [base EXCEPT ![c] = files[c]]
        /\ mainLog' = mainLog \cup Triples(files[c])
        /\ UNCHANGED <<files, store, pend, decides, storeLog>>

\* A branch takes main in without checking for a ratify after a ratify or supersede.
SyncUnchecked ==
    \E c \in Checkouts :
        /\ NotBusy(c)
        /\ base[c] # main
        /\ files' = [files EXCEPT ![c] = Taken(c)]
        /\ base' = [base EXCEPT ![c] = main]
        /\ UNCHANGED <<main, store, pend, decides, mainLog, storeLog>>

\* An agent ratifies a decision.
AgentRatify ==
    \E c \in Checkouts, a \in Agents, i \in Ids :
        /\ StateOf(files[c][i]) \notin {"ratified", "superseded"}
        /\ Amend(c, i, [op |-> "ratify", by |-> a, door |-> "none"])

Spec == Init /\ [][Next]_vars
====
