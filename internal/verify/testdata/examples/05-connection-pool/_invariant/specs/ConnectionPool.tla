---- MODULE ConnectionPool ----
EXTENDS Naturals, FiniteSets
CONSTANTS Size, Clients
VARIABLES held

vars == <<held>>

Conns == 1..Size

Out == UNION {held[c] : c \in Clients}

Loans == {p \in Clients \X Conns : p[2] \in held[p[1]]}

TypeOK == held \in [Clients -> SUBSET Conns]

NeverOverSize == Cardinality(Loans) <= Size

AtMostOnePerClient == \A c \in Clients : Cardinality(held[c]) <= 1

NoSharedConnection == \A c, d \in Clients : c # d => held[c] \cap held[d] = {}

PoolFull == Cardinality(Out) = Size /\ Size > 0

SomeoneHolds == \E c \in Clients : held[c] # {}

Init == held = [c \in Clients |-> {}]

Acquire(c) ==
    /\ held[c] = {}
    /\ \E k \in Conns \ Out : held' = [held EXCEPT ![c] = {k}]

Refuse(c) ==
    /\ Out = Conns
    /\ UNCHANGED vars

Release(c) ==
    \E k \in held[c] : held' = [held EXCEPT ![c] = held[c] \ {k}]

Next == \E c \in Clients : Acquire(c) \/ Refuse(c) \/ Release(c)

\* A known bug: when every connection is out, the pool hands out one that is already out.
AcquireWhenFull ==
    \E c \in Clients, k \in Conns :
        /\ held[c] = {}
        /\ Out = Conns
        /\ held' = [held EXCEPT ![c] = {k}]

\* A known bug: a client that already holds a connection is given a second one.
AcquireTwice ==
    \E c \in Clients, k \in Conns \ Out :
        /\ held[c] # {}
        /\ held' = [held EXCEPT ![c] = held[c] \cup {k}]

Spec == Init /\ [][Next]_vars
====
