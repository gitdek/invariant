---- MODULE StoreJournal ----
\* How the machine's store keeps the journal (#177, part 4). Checkouts on
\* branches of their own write journal lines, and the store journals each
\* line as it's written. A rebuild replaces a project's graph with what one
\* checkout holds, and a branch can be abandoned, never to merge. The store's
\* journal only grows, and keeps every line, merged or not.
EXTENDS Naturals, FiniteSets

CONSTANTS Checkouts, MaxLines

VARIABLES store, files, written, abandoned, ever

vars == <<store, files, written, abandoned, ever>>

\* A line is the checkout that wrote it and which of its lines it is.
Lines == Checkouts \X (1..MaxLines)

TypeOK ==
    /\ store \subseteq Lines
    /\ files \in [Checkouts -> SUBSET Lines]
    /\ written \in [Checkouts -> 0..MaxLines]
    /\ abandoned \subseteq Checkouts
    /\ ever \subseteq Lines

\* The store's journal only grows: every line it ever held, it holds now.
StoreOnlyGrows == ever \subseteq store

\* The store keeps every line any checkout wrote, merged or not.
KeepsEveryLine == \A c \in Checkouts : \A i \in 1..written[c] : <<c, i>> \in store

\* Witnesses.
KeptAbandoned == \E c \in abandoned : \E l \in store : l[1] = c
RebuiltFromOther ==
    \E c, d \in Checkouts : c # d /\ written[c] > 0 /\ written[d] > 0 /\ files[d] \subseteq files[c]

Init ==
    /\ store = {}
    /\ files = [c \in Checkouts |-> {}]
    /\ written = [c \in Checkouts |-> 0]
    /\ abandoned = {}
    /\ ever = {}

\* A checkout writes a line, and the store journals it.
Write(c) ==
    /\ c \notin abandoned
    /\ written[c] < MaxLines
    /\ LET l == <<c, written[c] + 1>> IN
        /\ files' = [files EXCEPT ![c] = @ \cup {l}]
        /\ store' = store \cup {l}
        /\ ever' = ever \cup {l}
    /\ written' = [written EXCEPT ![c] = @ + 1]
    /\ UNCHANGED abandoned

\* A checkout takes another's lines in, as a merge through main brings them.
TakeIn(c, d) ==
    /\ c # d
    /\ c \notin abandoned
    /\ d \notin abandoned
    /\ files[d] \ files[c] # {}
    /\ files' = [files EXCEPT ![c] = @ \cup files[d]]
    /\ UNCHANGED <<store, written, abandoned, ever>>

\* The store's project is rebuilt from a checkout: the lines it holds are
\* journaled, and none the store has is dropped.
Rebuild(c) ==
    /\ files[c] \ store # {}
    /\ store' = store \cup files[c]
    /\ ever' = ever \cup files[c]
    /\ UNCHANGED <<files, written, abandoned>>

\* A branch is abandoned, never to merge.
Abandon(c) ==
    /\ c \notin abandoned
    /\ abandoned' = abandoned \cup {c}
    /\ UNCHANGED <<store, files, written, ever>>

Done ==
    /\ \A c \in Checkouts : written[c] = MaxLines \/ c \in abandoned
    /\ UNCHANGED vars

Next ==
    \/ \E c \in Checkouts : Write(c)
    \/ \E c \in Checkouts : Rebuild(c)
    \/ \E c \in Checkouts : Abandon(c)
    \/ \E c \in Checkouts, d \in Checkouts : TakeIn(c, d)
    \/ Done

\* A known bug: a rebuild replaces the store's journal with what one checkout
\* holds, dropping the lines only other branches have.
RebuildReplaces ==
    \E c \in Checkouts :
        /\ store # files[c]
        /\ store' = files[c]
        /\ ever' = ever \cup files[c]
        /\ UNCHANGED <<files, written, abandoned>>

\* A known bug: abandoning a branch drops its lines from the store.
AbandonDrops ==
    \E c \in Checkouts :
        /\ c \notin abandoned
        /\ abandoned' = abandoned \cup {c}
        /\ store' = {l \in store : l[1] # c}
        /\ UNCHANGED <<files, written, ever>>

\* A known bug: a checkout writes a line the store never journals.
WriteNotJournaled ==
    \E c \in Checkouts :
        /\ c \notin abandoned
        /\ written[c] < MaxLines
        /\ files' = [files EXCEPT ![c] = @ \cup {<<c, written[c] + 1>>}]
        /\ written' = [written EXCEPT ![c] = @ + 1]
        /\ UNCHANGED <<store, abandoned, ever>>

Spec == Init /\ [][Next]_vars
====
