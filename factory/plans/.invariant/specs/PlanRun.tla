---- MODULE PlanRun ----
EXTENDS Integers, Sequences, FiniteSets

CONSTANTS N, Watchers, People, NoOne, MaxCrashes

VARIABLES ratifier, stopMark, issues, recorded, done, leader, crashes

vars == <<ratifier, stopMark, issues, recorded, done, leader, crashes>>

\* The plan's steps, in order.
Steps == 1..N

Statuses == {"open", "failed", "merged"}

\* Everything below except leader and crashes is on GitHub. issues is every issue the
\* factory created for the plan, in creation order; an issue's number is its index.
\* stopMark is -1 until the plan is stopped, then how many issues existed when it was.
Numbers == 1..Len(issues)

CreatedFor(s) == {i \in Numbers : issues[i].step = s}

Opened(s) == CreatedFor(s) # {}

Merged(s) == \E i \in CreatedFor(s) : issues[i].status = "merged"

Pending == {s \in Steps : ~Merged(s)}

NextStep == CHOOSE s \in Pending : \A t \in Pending : s <= t

Ratified == ratifier # NoOne

Stopped == stopMark # -1

TypeOK ==
    /\ ratifier \in People \cup {NoOne}
    /\ stopMark \in {-1} \cup Nat
    /\ issues \in Seq([step : Nat, author : People \cup {NoOne}, status : Statuses])
    /\ recorded \subseteq Nat
    /\ done \in BOOLEAN
    /\ leader \in Watchers \cup {NoOne}
    /\ crashes \in 0..MaxCrashes

OpenedAtMostOnce ==
    \A i, j \in Numbers : i # j => issues[i].step # issues[j].step

OpenedOnlyWhenRatified == Len(issues) > 0 => Ratified

NoneOpenedAfterStop == Stopped => Len(issues) <= stopMark

OnlyPlannedIssues == \A i \in Numbers : issues[i].step \in Steps

InOrderAfterMerge ==
    \A i \in Numbers : \A t \in Steps :
        t < issues[i].step =>
            \E j \in Numbers : j < i /\ issues[j].step = t /\ issues[j].status = "merged"

AtMostOneOpen == Cardinality({i \in Numbers : issues[i].status # "merged"}) <= 1

SolvedOnRatifierAuthority == \A i \in Numbers : issues[i].author = ratifier

RecordsAreReal == recorded \subseteq Numbers

DoneOnlyWhenAllMerged == done => Pending = {}

PlanDone == done /\ recorded = Numbers

IssueFailed == \E i \in Numbers : issues[i].status = "failed"

StoppedWithIssueOpen == Stopped /\ \E i \in Numbers : issues[i].status # "merged"

FinishedAfterCrashes == done /\ crashes = MaxCrashes

\* If every opened issue eventually merges, a ratified plan eventually finishes or is stopped.
PlanFinishes ==
    (\A s \in Steps : Opened(s) ~> Merged(s)) => (Ratified ~> (done \/ Stopped))

Init ==
    /\ ratifier = NoOne
    /\ stopMark = -1
    /\ issues = <<>>
    /\ recorded = {}
    /\ done = FALSE
    /\ leader = NoOne
    /\ crashes = 0

Ratify(p) ==
    /\ ~Ratified
    /\ ~Stopped
    /\ ratifier' = p
    /\ UNCHANGED <<stopMark, issues, recorded, done, leader, crashes>>

Stop(p) ==
    /\ ~Stopped
    /\ ~done
    /\ stopMark' = Len(issues)
    /\ UNCHANGED <<ratifier, issues, recorded, done, leader, crashes>>

Acquire(w) ==
    /\ leader = NoOne
    /\ leader' = w
    /\ UNCHANGED <<ratifier, stopMark, issues, recorded, done, crashes>>

Crash(w) ==
    /\ leader = w
    /\ crashes < MaxCrashes
    /\ leader' = NoOne
    /\ crashes' = crashes + 1
    /\ UNCHANGED <<ratifier, stopMark, issues, recorded, done>>

\* First effect of opening: look on GitHub, and create the next step's issue only if none exists.
Create(w) ==
    /\ leader = w
    /\ Ratified
    /\ ~Stopped
    /\ Pending # {}
    /\ ~Opened(NextStep)
    /\ issues' = Append(issues, [step |-> NextStep, author |-> ratifier, status |-> "open"])
    /\ UNCHANGED <<ratifier, stopMark, recorded, done, leader, crashes>>

\* Second effect of opening: post on the plan's issue that an issue is open, with its number.
Record(w) ==
    /\ leader = w
    /\ \E i \in Numbers \ recorded : recorded' = recorded \cup {i}
    /\ UNCHANGED <<ratifier, stopMark, issues, done, leader, crashes>>

Finish(w) ==
    /\ leader = w
    /\ Ratified
    /\ Pending = {}
    /\ ~done
    /\ done' = TRUE
    /\ UNCHANGED <<ratifier, stopMark, issues, recorded, leader, crashes>>

\* The issue protocol, from outside the plan: an issue fails, or merges, including after failing.
Fail(i) ==
    /\ issues[i].status = "open"
    /\ issues' = [issues EXCEPT ![i].status = "failed"]
    /\ UNCHANGED <<ratifier, stopMark, recorded, done, leader, crashes>>

Merge(i) ==
    /\ issues[i].status \in {"open", "failed"}
    /\ issues' = [issues EXCEPT ![i].status = "merged"]
    /\ UNCHANGED <<ratifier, stopMark, recorded, done, leader, crashes>>

Rest ==
    /\ done \/ Stopped
    /\ UNCHANGED vars

Next ==
    \/ \E p \in People : Ratify(p) \/ Stop(p)
    \/ \E w \in Watchers : Acquire(w) \/ Crash(w) \/ Create(w) \/ Record(w) \/ Finish(w)
    \/ \E i \in Numbers : Fail(i) \/ Merge(i)
    \/ Rest

AcquireFair == \A w \in Watchers : WF_vars(Acquire(w))

CreateFair == \A w \in Watchers : WF_vars(Create(w))

RecordFair == \A w \in Watchers : WF_vars(Record(w))

FinishFair == \A w \in Watchers : WF_vars(Finish(w))

\* A known bug: trusting the plan's issue instead of GitHub, it creates the next step's
\* issue again when a crash came between creating it and recording it.
CreateUnlessRecorded ==
    \E w \in Watchers :
        /\ leader = w
        /\ Ratified
        /\ ~Stopped
        /\ Pending # {}
        /\ CreatedFor(NextStep) \cap recorded = {}
        /\ issues' = Append(issues, [step |-> NextStep, author |-> ratifier, status |-> "open"])
        /\ UNCHANGED <<ratifier, stopMark, recorded, done, leader, crashes>>

\* A known bug: it opens the next issue without checking whether the plan was stopped.
CreateIgnoringStop ==
    \E w \in Watchers :
        /\ leader = w
        /\ Ratified
        /\ Pending # {}
        /\ ~Opened(NextStep)
        /\ issues' = Append(issues, [step |-> NextStep, author |-> ratifier, status |-> "open"])
        /\ UNCHANGED <<ratifier, stopMark, recorded, done, leader, crashes>>

\* A known bug: it opens the next issue on the authority of someone other than the ratifier.
CreateAsSomeoneElse ==
    \E w \in Watchers, p \in People :
        /\ leader = w
        /\ Ratified
        /\ ~Stopped
        /\ Pending # {}
        /\ ~Opened(NextStep)
        /\ issues' = Append(issues, [step |-> NextStep, author |-> p, status |-> "open"])
        /\ UNCHANGED <<ratifier, stopMark, recorded, done, leader, crashes>>

Spec == Init /\ [][Next]_vars
====
