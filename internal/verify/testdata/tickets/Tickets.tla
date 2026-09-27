---- MODULE Tickets ----
\* A pool of tickets that clients take and give back. The gate's check one
\* size larger runs on it, with a TypeScript driver that counts.
EXTENDS Naturals, FiniteSets
CONSTANTS Capacity, Clients
VARIABLES held

vars == <<held>>

TypeOK == held \subseteq Clients

AtMostCapacity == Cardinality(held) <= Capacity

Full == Cardinality(held) = Capacity

\* A known bug: a client takes a ticket when none is left.
TakeWhenFull == \E c \in Clients : c \notin held /\ held' = held \cup {c}

Init == held = {}

Take(c) == c \notin held /\ Cardinality(held) < Capacity /\ held' = held \cup {c}

Give(c) == c \in held /\ held' = held \ {c}

Next == \E c \in Clients : Take(c) \/ Give(c)

Spec == Init /\ [][Next]_vars
====
