---- MODULE DecisionIDs ----
\* How `invariant decisions decide` takes a new decision's ID on one machine
\* (#177, part 1). Checkouts, each on a branch of its own, decide at once.
\* One store on the machine holds the IDs taken, and a decide holds its lock.
\* A decide can stop at any step, and branches merge into main and take main
\* in.
EXTENDS Naturals, FiniteSets

CONSTANTS Checkouts, MaxDecides, MaxStops

VARIABLES
    store,    \* the IDs the store has taken for the project
    files,    \* each checkout's journal: the decisions it holds
    main,     \* the decisions merged into main
    lock,     \* the checkout whose decide holds the store's lock, or NoOne
    pc,       \* each checkout's decide: idle, taken, reserved or written
    next,     \* the ID each checkout's decide took
    decides,  \* how many decisions each checkout has written
    stops     \* how many decides have stopped partway

vars == <<store, files, main, lock, pc, next, decides, stops>>

NoOne == "none"

\* A decision is its ID, the checkout that wrote it, and which of that
\* checkout's decisions it is.
Decision(id, c, n) == [id |-> id, by |-> c, n |-> n]

IDs == 1..(Cardinality(Checkouts) * MaxDecides + MaxStops)

Decisions == [id : IDs, by : Checkouts, n : 1..MaxDecides]

Max(S) == IF S = {} THEN 0 ELSE CHOOSE x \in S : \A y \in S : y <= x

Everywhere == main \cup UNION {files[c] : c \in Checkouts}

TypeOK ==
    /\ store \subseteq IDs
    /\ files \in [Checkouts -> SUBSET Decisions]
    /\ main \subseteq Decisions
    /\ lock \in Checkouts \cup {NoOne}
    /\ pc \in [Checkouts -> {"idle", "taken", "reserved", "written"}]
    /\ next \in [Checkouts -> 0..Max(IDs)]
    /\ decides \in [Checkouts -> 0..MaxDecides]
    /\ stops \in 0..MaxStops

\* No two decisions share an ID, in any checkout, on any branch, merged or not.
NoSharedID == \A x, y \in Everywhere : x.id = y.id => x = y

\* Witnesses.
BothDecided == \A c \in Checkouts : decides[c] > 0
BothMerged == \A c \in Checkouts : \E d \in main : d.by = c
GapAfterStop == \E id \in store : ~\E d \in Everywhere : d.id = id

Init ==
    /\ store = {}
    /\ files = [c \in Checkouts |-> {}]
    /\ main = {}
    /\ lock = NoOne
    /\ pc = [c \in Checkouts |-> "idle"]
    /\ next = [c \in Checkouts |-> 0]
    /\ decides = [c \in Checkouts |-> 0]
    /\ stops = 0

\* A decide takes the store's lock, and takes one more than the highest ID
\* the store has taken or the checkout holds.
Take(c) ==
    /\ pc[c] = "idle"
    /\ lock = NoOne
    /\ decides[c] < MaxDecides
    /\ lock' = c
    /\ next' = [next EXCEPT ![c] = Max(store \cup {d.id : d \in files[c]}) + 1]
    /\ pc' = [pc EXCEPT ![c] = "taken"]
    /\ UNCHANGED <<store, files, main, decides, stops>>

\* The store records the ID before the decision is written anywhere.
Reserve(c) ==
    /\ pc[c] = "taken"
    /\ store' = store \cup {next[c]}
    /\ pc' = [pc EXCEPT ![c] = "reserved"]
    /\ UNCHANGED <<files, main, lock, next, decides, stops>>

\* The decision's journal file is written in the checkout.
Write(c) ==
    /\ pc[c] = "reserved"
    /\ files' = [files EXCEPT ![c] = @ \cup {Decision(next[c], c, decides[c] + 1)}]
    /\ decides' = [decides EXCEPT ![c] = @ + 1]
    /\ pc' = [pc EXCEPT ![c] = "written"]
    /\ UNCHANGED <<store, main, lock, next, stops>>

\* The decide lets the lock go.
Finish(c) ==
    /\ pc[c] = "written"
    /\ lock' = NoOne
    /\ pc' = [pc EXCEPT ![c] = "idle"]
    /\ UNCHANGED <<store, files, main, next, decides, stops>>

\* A decide stops partway: the lock goes, and whatever it recorded or wrote
\* stays.
Stop(c) ==
    /\ pc[c] # "idle"
    /\ stops < MaxStops
    /\ lock' = NoOne
    /\ pc' = [pc EXCEPT ![c] = "idle"]
    /\ stops' = stops + 1
    /\ UNCHANGED <<store, files, main, next, decides>>

\* A checkout's branch merges into main.
Merge(c) ==
    /\ pc[c] = "idle"
    /\ files[c] \ main # {}
    /\ main' = main \cup files[c]
    /\ UNCHANGED <<store, files, lock, pc, next, decides, stops>>

\* A checkout takes main in.
Pull(c) ==
    /\ pc[c] = "idle"
    /\ main \ files[c] # {}
    /\ files' = [files EXCEPT ![c] = @ \cup main]
    /\ UNCHANGED <<store, main, lock, pc, next, decides, stops>>

\* Every checkout has written its decisions, and every branch holds all of
\* them.
Done ==
    /\ \A c \in Checkouts : pc[c] = "idle" /\ decides[c] = MaxDecides /\ files[c] = main
    /\ UNCHANGED vars

Next ==
    \/ \E c \in Checkouts :
        \/ Take(c) \/ Reserve(c) \/ Write(c) \/ Finish(c)
        \/ Stop(c) \/ Merge(c) \/ Pull(c)
    \/ Done

\* A known bug, the order decide had: the journal file is written before the
\* store records the ID, so a stop between them leaves a decision whose ID
\* the store doesn't know, and another checkout takes it again.
WriteBeforeReserve ==
    \E c \in Checkouts :
        /\ pc[c] = "taken"
        /\ files' = [files EXCEPT ![c] = @ \cup {Decision(next[c], c, decides[c] + 1)}]
        /\ decides' = [decides EXCEPT ![c] = @ + 1]
        /\ pc' = [pc EXCEPT ![c] = "idle"]
        /\ lock' = NoOne
        /\ UNCHANGED <<store, main, next, stops>>

\* A known bug: a decide takes its ID without the store's lock, so two
\* checkouts can take the same one.
TakeWithoutLock ==
    \E c \in Checkouts :
        /\ pc[c] = "idle"
        /\ decides[c] < MaxDecides
        /\ next' = [next EXCEPT ![c] = Max(store \cup {d.id : d \in files[c]}) + 1]
        /\ pc' = [pc EXCEPT ![c] = "taken"]
        /\ UNCHANGED <<store, files, main, lock, decides, stops>>

\* A known bug: a decide takes one more than the highest ID its checkout
\* holds, and never asks the store.
CheckoutOnly ==
    \E c \in Checkouts :
        /\ pc[c] = "idle"
        /\ lock = NoOne
        /\ decides[c] < MaxDecides
        /\ lock' = c
        /\ next' = [next EXCEPT ![c] = Max({d.id : d \in files[c]}) + 1]
        /\ pc' = [pc EXCEPT ![c] = "taken"]
        /\ UNCHANGED <<store, files, main, decides, stops>>

Spec == Init /\ [][Next]_vars
====
