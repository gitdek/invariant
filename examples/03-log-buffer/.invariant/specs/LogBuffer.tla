---- MODULE LogBuffer ----
EXTENDS Naturals, Sequences

CONSTANTS Producers, Capacity, MaxLines

VARIABLES buf, sent, log, written, retrying

vars == <<buf, sent, log, written, retrying>>

\* A line is identified by the producer that wrote it and its per-producer number.
Lines == Producers \X (1..MaxLines)

Range(s) == {s[i] : i \in DOMAIN s}

TypeOK ==
    /\ buf \in Seq(Lines)
    /\ sent \in Seq(Lines)
    /\ log \in Seq(Lines)
    /\ written \in [Producers -> 0..MaxLines]
    /\ retrying \in BOOLEAN

BoundedCapacity == Len(buf) <= Capacity

NoLineLost == \A i \in 1..Len(log) : log[i] \in Range(sent) \cup Range(buf)

ShippedOnce == \A i, j \in 1..Len(sent) : i # j => sent[i] # sent[j]

ShippedInOrder ==
    /\ Len(sent) <= Len(log)
    /\ sent = SubSeq(log, 1, Len(sent))

BufferInOrder ==
    /\ Len(sent) <= Len(log)
    /\ buf = SubSeq(log, Len(sent) + 1, Len(log))

BufferFull == Len(buf) = Capacity

RetryPending == retrying /\ buf # <<>>

AllShipped ==
    /\ \A p \in Producers : written[p] = MaxLines
    /\ buf = <<>>
    /\ Len(sent) = Len(log)
    /\ Len(log) > 0

Init ==
    /\ buf = <<>>
    /\ sent = <<>>
    /\ log = <<>>
    /\ written = [p \in Producers |-> 0]
    /\ retrying = FALSE

\* A producer writes its next line. Capacity is the buffer's own limit: a write into
\* a full buffer is not enabled, so the code must refuse it and change nothing.
\* MaxLines bounds the environment: a producer that has written all its lines is not asked again.
Write(p) ==
    /\ written[p] < MaxLines
    /\ Len(buf) < Capacity
    /\ LET line == <<p, written[p] + 1>> IN
        /\ buf' = Append(buf, line)
        /\ log' = Append(log, line)
    /\ written' = [written EXCEPT ![p] = @ + 1]
    /\ UNCHANGED <<sent, retrying>>

\* The shipper sends the oldest line successfully and removes it.
Ship ==
    /\ buf # <<>>
    /\ sent' = Append(sent, Head(buf))
    /\ buf' = Tail(buf)
    /\ retrying' = FALSE
    /\ UNCHANGED <<log, written>>

\* Sending the oldest line fails; it stays at the front to be retried.
ShipFail ==
    /\ buf # <<>>
    /\ ~retrying
    /\ retrying' = TRUE
    /\ UNCHANGED <<buf, sent, log, written>>

\* Every producer has written all its lines and everything has been shipped.
Done ==
    /\ \A p \in Producers : written[p] = MaxLines
    /\ buf = <<>>
    /\ UNCHANGED vars

Next ==
    \/ \E p \in Producers : Write(p)
    \/ Ship
    \/ ShipFail
    \/ Done

\* A known bug: a producer appends a line even though the buffer is full.
WriteOverCapacity ==
    \E p \in Producers :
        /\ written[p] < MaxLines
        /\ Len(buf) = Capacity
        /\ LET line == <<p, written[p] + 1>> IN
            /\ buf' = Append(buf, line)
            /\ log' = Append(log, line)
        /\ written' = [written EXCEPT ![p] = @ + 1]
        /\ UNCHANGED <<sent, retrying>>

\* A known bug: when a send fails, the line is dropped instead of retried.
DropOnFailure ==
    /\ buf # <<>>
    /\ buf' = Tail(buf)
    /\ retrying' = FALSE
    /\ UNCHANGED <<sent, log, written>>

\* A known bug: a send that actually went through is treated as failed and retried,
\* so the line goes out twice before it is removed.
ResendDelivered ==
    /\ buf # <<>>
    /\ sent' = sent \o <<Head(buf), Head(buf)>>
    /\ buf' = Tail(buf)
    /\ retrying' = FALSE
    /\ UNCHANGED <<log, written>>

Spec == Init /\ [][Next]_vars
====
