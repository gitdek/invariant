---- MODULE BoundedBuffer ----
EXTENDS Naturals, Sequences
CONSTANTS Cap, Msgs
VARIABLES buf

vars == <<buf>>

WithinCap == Len(buf) <= Cap

TypeOK == buf \in Seq(Msgs)

CanFill == Len(buf) = Cap

Init == buf = <<>>

Put(m) == Len(buf) < Cap /\ buf' = Append(buf, m)

Take == Len(buf) > 0 /\ buf' = Tail(buf)

Next == (\E m \in Msgs : Put(m)) \/ Take

\* A known bug: a producer adds to a full buffer.
PutWhenFull == \E m \in Msgs : buf' = Append(buf, m)

Spec == Init /\ [][Next]_vars
====
