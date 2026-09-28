---- MODULE RateLimiter ----
EXTENDS Naturals, Sequences

CONSTANTS Apis, Capacity, MaxCalls, MaxTime, MaxWaiting

VARIABLES tokens, waiting, sent, refused, made, clock

vars == <<tokens, waiting, sent, refused, made, clock>>

Waiter == [id : 1..MaxCalls, madeAt : 0..MaxTime]

SentCall == [id : 1..MaxCalls, madeAt : 0..MaxTime, at : 0..MaxTime]

\* A refused call, with the bucket and queue it met when it was made.
RefusedCall == [madeAt : 0..MaxTime, tokens : 0..Capacity, queued : 0..MaxCalls]

TypeOK ==
    /\ tokens \in [Apis -> 0..Capacity]
    /\ waiting \in [Apis -> Seq(Waiter)]
    /\ sent \in [Apis -> Seq(SentCall)]
    /\ refused \in [Apis -> Seq(RefusedCall)]
    /\ made \in [Apis -> 0..MaxCalls]
    /\ clock \in 0..MaxTime

\* Over any stretch of time, an API gets at most Capacity calls plus one per tick.
WithinRate ==
    \A a \in Apis : \A i, j \in 1..Len(sent[a]) :
        i <= j => j - i + 1 <= Capacity + (sent[a][j].at - sent[a][i].at)

\* Calls to an API go out, and wait, in the order they were made.
FirstInFirstOut ==
    \A a \in Apis :
        /\ \A i \in 1..Len(sent[a]) : sent[a][i].id = i
        /\ \A i \in 1..Len(waiting[a]) : waiting[a][i].id = Len(sent[a]) + i

\* A call only waits when its API's bucket is empty.
NoNeedlessWait ==
    \A a \in Apis : waiting[a] # <<>> => tokens[a] = 0

\* Every call made has gone out, is still waiting, or was refused.
NoCallLost ==
    \A a \in Apis : made[a] = Len(sent[a]) + Len(waiting[a]) + Len(refused[a])

\* No more than MaxWaiting calls ever wait for the same API.
BoundedWait ==
    \A a \in Apis : Len(waiting[a]) <= MaxWaiting

\* A call is only refused when its bucket was empty and the queue was full.
NoNeedlessRefusal ==
    \A a \in Apis : \A i \in 1..Len(refused[a]) :
        refused[a][i].tokens = 0 /\ refused[a][i].queued = MaxWaiting

BurstGoesOut == \E a \in Apis : clock = 0 /\ Len(sent[a]) = Capacity

CallWaits == \E a \in Apis : waiting[a] # <<>>

WaitingCallGoesOut ==
    \E a \in Apis : \E i \in 1..Len(sent[a]) : sent[a][i].at > sent[a][i].madeAt

BucketRefills == \E a \in Apis : Len(sent[a]) > 0 /\ tokens[a] = Capacity

CallRefused == \E a \in Apis : refused[a] # <<>>

Init ==
    /\ tokens = [a \in Apis |-> Capacity]
    /\ waiting = [a \in Apis |-> <<>>]
    /\ sent = [a \in Apis |-> <<>>]
    /\ refused = [a \in Apis |-> <<>>]
    /\ made = [a \in Apis |-> 0]
    /\ clock = 0

\* A call is made: it goes out now if a token is free and nobody waits,
\* else it queues if fewer than MaxWaiting wait, else it is refused.
MakeCall(a) ==
    /\ made[a] < MaxCalls
    /\ made' = [made EXCEPT ![a] = @ + 1]
    /\ LET id == Len(sent[a]) + Len(waiting[a]) + 1
       IN IF tokens[a] > 0 /\ waiting[a] = <<>>
             THEN /\ tokens' = [tokens EXCEPT ![a] = @ - 1]
                  /\ sent' = [sent EXCEPT ![a] =
                         Append(@, [id |-> id, madeAt |-> clock, at |-> clock])]
                  /\ UNCHANGED <<waiting, refused>>
          ELSE IF Len(waiting[a]) < MaxWaiting
             THEN /\ waiting' = [waiting EXCEPT ![a] =
                         Append(@, [id |-> id, madeAt |-> clock])]
                  /\ UNCHANGED <<tokens, sent, refused>>
          ELSE /\ refused' = [refused EXCEPT ![a] =
                      Append(@, [madeAt |-> clock, tokens |-> tokens[a],
                                 queued |-> Len(waiting[a])])]
               /\ UNCHANGED <<tokens, sent, waiting>>
    /\ UNCHANGED clock

\* One tick: every bucket refills one token, spent at once on the oldest waiting call.
Tick ==
    /\ clock < MaxTime
    /\ clock' = clock + 1
    /\ tokens' = [a \in Apis |->
          IF waiting[a] = <<>> /\ tokens[a] < Capacity THEN tokens[a] + 1 ELSE tokens[a]]
    /\ sent' = [a \in Apis |->
          IF waiting[a] = <<>> THEN sent[a]
          ELSE Append(sent[a], [id |-> Head(waiting[a]).id,
                                madeAt |-> Head(waiting[a]).madeAt,
                                at |-> clock + 1])]
    /\ waiting' = [a \in Apis |-> IF waiting[a] = <<>> THEN <<>> ELSE Tail(waiting[a])]
    /\ UNCHANGED <<made, refused>>

\* The checked run is over: time and calls are used up.
Done ==
    /\ clock = MaxTime
    /\ \A a \in Apis : made[a] = MaxCalls
    /\ UNCHANGED vars

Next == (\E a \in Apis : MakeCall(a)) \/ Tick \/ Done

\* A known bug: a call goes out even though the bucket is empty.
SendWithoutToken ==
    \E a \in Apis :
        /\ made[a] < MaxCalls
        /\ tokens[a] = 0
        /\ waiting[a] = <<>>
        /\ made' = [made EXCEPT ![a] = @ + 1]
        /\ sent' = [sent EXCEPT ![a] =
               Append(@, [id |-> made[a] + 1, madeAt |-> clock, at |-> clock])]
        /\ UNCHANGED <<tokens, waiting, refused, clock>>

\* A known bug: a refilled token goes to the newest waiting call instead of the oldest.
JumpQueue ==
    \E a \in Apis :
        /\ clock < MaxTime
        /\ Len(waiting[a]) >= 2
        /\ clock' = clock + 1
        /\ LET w == waiting[a][Len(waiting[a])]
           IN sent' = [sent EXCEPT ![a] =
                  Append(@, [id |-> w.id, madeAt |-> w.madeAt, at |-> clock + 1])]
        /\ waiting' = [waiting EXCEPT ![a] = SubSeq(@, 1, Len(@) - 1)]
        /\ UNCHANGED <<tokens, made, refused>>

\* A known bug: a call queues even though MaxWaiting calls already wait.
QueuePastLimit ==
    \E a \in Apis :
        /\ made[a] < MaxCalls
        /\ tokens[a] = 0
        /\ Len(waiting[a]) >= MaxWaiting
        /\ made' = [made EXCEPT ![a] = @ + 1]
        /\ waiting' = [waiting EXCEPT ![a] =
               Append(@, [id |-> Len(sent[a]) + Len(waiting[a]) + 1, madeAt |-> clock])]
        /\ UNCHANGED <<tokens, sent, refused, clock>>

\* A known bug: a call is refused while there is still room in the queue.
RefuseEarly ==
    \E a \in Apis :
        /\ made[a] < MaxCalls
        /\ tokens[a] = 0
        /\ Len(waiting[a]) < MaxWaiting
        /\ made' = [made EXCEPT ![a] = @ + 1]
        /\ refused' = [refused EXCEPT ![a] =
               Append(@, [madeAt |-> clock, tokens |-> tokens[a],
                          queued |-> Len(waiting[a])])]
        /\ UNCHANGED <<tokens, sent, waiting, clock>>

Spec == Init /\ [][Next]_vars
====
