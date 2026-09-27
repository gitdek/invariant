---- MODULE Live ----
EXTENDS Naturals
CONSTANT N
VARIABLES pending, answered
vars == <<pending, answered>>

Init == pending = {} /\ answered = {}
Ask(c) == c \notin pending \cup answered /\ pending' = pending \cup {c} /\ UNCHANGED answered
Answer(c) == c \in pending /\ pending' = pending \ {c} /\ answered' = answered \cup {c}
Watcher == \E c \in pending : Answer(c)
Done == pending = {} /\ answered = 1..N /\ UNCHANGED vars
Next == (\E c \in 1..N : Ask(c)) \/ Watcher \/ Done

Spec == Init /\ [][Next]_vars
WatcherIsFair == WF_vars(Watcher)
FairSpec == Spec /\ WatcherIsFair

TypeOK == pending \subseteq 1..N /\ answered \subseteq 1..N
EventuallyAnswered == \A c \in 1..N : (c \in pending) ~> (c \in answered)
NeverUnanswered == [][\A c \in answered : c \in answered']_vars
AllAnswered == <>(answered = 1..N)

Forget == \E c \in pending : pending' = pending \ {c} /\ UNCHANGED answered
BuggySpec == Init /\ [][Next \/ Forget]_vars /\ WatcherIsFair

Other == \E c \in 1..N : answered' = answered \cup {c} /\ UNCHANGED pending
SubCheckSpec == Init /\ [][Next \/ Other]_vars
OtherIsNext == [][Other => Next]_vars
WatcherIsNext == [][Watcher => Next]_vars
SubCheckSpec2 == Init /\ [][Next \/ Watcher]_vars
====
