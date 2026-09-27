---- MODULE Answering ----
\* A watcher answers the commands people ask. The gate's liveness checks run
\* on it: a property under fairness, the fairness's action as a Next step,
\* and a known bug only a property catches.
EXTENDS Naturals
CONSTANT Commands
VARIABLES pending, answered

vars == <<pending, answered>>

TypeOK == pending \subseteq Commands /\ answered \subseteq Commands

AnsweredOnce == pending \cap answered = {}

SomeAnswered == answered # {}

\* The watcher answers one pending command.
Watch == \E c \in pending : pending' = pending \ {c} /\ answered' = answered \cup {c}

WatcherIsFair == WF_vars(Watch)

EventuallyAnswered == \A c \in Commands : c \in pending ~> c \in answered

AnswersStay == [][answered \subseteq answered']_vars

\* A known bug: the watcher drops a pending command without answering it.
Drop == \E c \in pending : pending' = pending \ {c} /\ UNCHANGED answered

Init == pending = {} /\ answered = {}

Ask(c) == c \notin pending \cup answered /\ pending' = pending \cup {c} /\ UNCHANGED answered

Finished == pending = {} /\ answered = Commands /\ UNCHANGED vars

Next == (\E c \in Commands : Ask(c)) \/ Watch \/ Finished

Spec == Init /\ [][Next]_vars
====
