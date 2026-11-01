---- MODULE JournalMerges ----
\* How branches' journals merge into main (#177, part 3). A decision's
\* journal file is a list of lines, and branches add lines to the files they
\* hold. A branch merges into main only when each of main's journal files is
\* the start of the branch's file (D-0103), and a branch that main has moved
\* past takes main in first, keeping main's lines where they are.
EXTENDS Naturals, Sequences

CONSTANTS Branches, Files, MaxLines

VARIABLES main, branch, added, ever

vars == <<main, branch, added, ever>>

\* A line is the branch that added it and which of its lines it is.
Lines == Branches \X (1..MaxLines)

\* Journals: each file's lines, <<>> for a file that isn't there.
Journals == [Files -> Seq(Lines)]

IsPrefix(s, t) == Len(s) <= Len(t) /\ SubSeq(t, 1, Len(s)) = s

\* The lines a branch's file holds past main's.
Past(f, b) == SubSeq(branch[b][f], Len(main[f]) + 1, Len(branch[b][f]))

Range(s) == {s[i] : i \in DOMAIN s}

TypeOK ==
    /\ main \in Journals
    /\ branch \in [Branches -> Journals]
    /\ added \in [Branches -> 0..MaxLines]
    /\ ever \in Journals

\* Merging never changes or removes a line main already has: every file main
\* ever held is the start of the file main holds now.
MainOnlyGrows == \A f \in Files : IsPrefix(ever[f], main[f])

\* No line is lost: every line a branch added is in its branch, or in main.
NoLineLost ==
    \A b \in Branches : \A i \in 1..added[b] :
        \E f \in Files : <<b, i>> \in Range(branch[b][f]) \cup Range(main[f])

\* Witnesses.
BothMergedOneFile == \E f \in Files : \A b \in Branches : \E l \in Range(main[f]) : l[1] = b
TookMainIn == \E b \in Branches, f \in Files : main[f] # <<>> /\ IsPrefix(main[f], branch[b][f]) /\ Len(branch[b][f]) > Len(main[f])

Init ==
    /\ main = [f \in Files |-> <<>>]
    /\ branch = [b \in Branches |-> [f \in Files |-> <<>>]]
    /\ added = [b \in Branches |-> 0]
    /\ ever = [f \in Files |-> <<>>]

\* A branch adds a line to one of its files.
Add(b, f) ==
    /\ added[b] < MaxLines
    /\ branch' = [branch EXCEPT ![b][f] = Append(@, <<b, added[b] + 1>>)]
    /\ added' = [added EXCEPT ![b] = @ + 1]
    /\ UNCHANGED <<main, ever>>

\* A branch merges into main, only when each of main's files is the start of
\* the branch's.
Merge(b) ==
    /\ \A f \in Files : IsPrefix(main[f], branch[b][f])
    /\ \E f \in Files : Len(branch[b][f]) > Len(main[f])
    /\ main' = branch[b]
    /\ ever' = branch[b]
    /\ UNCHANGED <<branch, added>>

\* A branch that main has moved past takes main in: each file is main's
\* lines, where they are, then the branch's own lines past what it had of
\* main.
TakeMainIn(b) ==
    /\ \E f \in Files : ~IsPrefix(main[f], branch[b][f])
    /\ branch' = [branch EXCEPT ![b] =
        [f \in Files |-> main[f] \o SelectSeq(branch[b][f], LAMBDA l : l \notin Range(main[f]))]]
    /\ UNCHANGED <<main, added, ever>>

Done ==
    /\ \A b \in Branches : added[b] = MaxLines /\ branch[b] = main
    /\ UNCHANGED vars

Next ==
    \/ \E b \in Branches, f \in Files : Add(b, f)
    \/ \E b \in Branches : Merge(b) \/ TakeMainIn(b)
    \/ Done

\* A known bug: a branch merges without the check, and its files replace
\* main's, dropping main's lines it doesn't have.
MergeUnchecked ==
    \E b \in Branches :
        /\ \E f \in Files : branch[b][f] # main[f]
        /\ main' = branch[b]
        /\ ever' = [f \in Files |-> IF IsPrefix(ever[f], branch[b][f]) THEN branch[b][f] ELSE ever[f]]
        /\ UNCHANGED <<branch, added>>

\* A known bug: taking main in drops the branch's own lines past what it had
\* of main, so they're lost.
TakeMainInDropsOwn ==
    \E b \in Branches :
        /\ \E f \in Files : ~IsPrefix(main[f], branch[b][f])
        /\ branch' = [branch EXCEPT ![b] = main]
        /\ UNCHANGED <<main, added, ever>>

Spec == Init /\ [][Next]_vars
====
