------------------------------ MODULE TwoPhase ------------------------------
CONSTANT RM            \* the resource managers
VARIABLES
    rmState,           \* rmState[r] is the state of resource manager r
    tmState,           \* the state of the transaction manager
    tmPrepared,        \* the RMs the TM has received a Prepared message from
    msgs               \* every message ever sent

vars == <<rmState, tmState, tmPrepared, msgs>>

Messages == [type : {"Prepared"}, rm : RM] \cup [type : {"Commit", "Abort"}]

TypeOK ==
    /\ rmState \in [RM -> {"working", "prepared", "committed", "aborted"}]
    /\ tmState \in {"init", "done"}
    /\ tmPrepared \subseteq RM
    /\ msgs \subseteq Messages

TCConsistent ==
    \A r1, r2 \in RM : ~(rmState[r1] = "aborted" /\ rmState[r2] = "committed")

AllCommitted == \A r \in RM : rmState[r] = "committed"

AllAborted == \A r \in RM : rmState[r] = "aborted"

\* A known bug the invariants must catch: the coordinator commits once any
\* resource manager has prepared, instead of waiting for all of them.
EarlyCommit ==
    /\ tmState = "init"
    /\ tmPrepared # {}
    /\ tmState' = "done"
    /\ msgs' = msgs \cup {[type |-> "Commit"]}
    /\ UNCHANGED <<rmState, tmPrepared>>

\* ---------------------------------------------------------------------------
\* The model. The factory writes Init, one operator per action, and Next
\* here. Everything else in this module is pinned and must not change.
\* ---------------------------------------------------------------------------

Init ==
    /\ rmState = [r \in RM |-> "working"]
    /\ tmState = "init"
    /\ tmPrepared = {}
    /\ msgs = {}

\* The TM receives a Prepared message from resource manager r.
TMRcvPrepared(r) ==
    /\ tmState = "init"
    /\ [type |-> "Prepared", rm |-> r] \in msgs
    /\ tmPrepared' = tmPrepared \cup {r}
    /\ UNCHANGED <<rmState, tmState, msgs>>

\* The TM commits once every resource manager has prepared.
TMCommit ==
    /\ tmState = "init"
    /\ tmPrepared = RM
    /\ tmState' = "done"
    /\ msgs' = msgs \cup {[type |-> "Commit"]}
    /\ UNCHANGED <<rmState, tmPrepared>>

\* The TM aborts at any time before it has decided.
TMAbort ==
    /\ tmState = "init"
    /\ tmState' = "done"
    /\ msgs' = msgs \cup {[type |-> "Abort"]}
    /\ UNCHANGED <<rmState, tmPrepared>>

\* Resource manager r prepares and tells the TM.
RMPrepare(r) ==
    /\ rmState[r] = "working"
    /\ rmState' = [rmState EXCEPT ![r] = "prepared"]
    /\ msgs' = msgs \cup {[type |-> "Prepared", rm |-> r]}
    /\ UNCHANGED <<tmState, tmPrepared>>

\* Resource manager r aborts on its own before it has prepared.
RMChooseToAbort(r) ==
    /\ rmState[r] = "working"
    /\ rmState' = [rmState EXCEPT ![r] = "aborted"]
    /\ UNCHANGED <<tmState, tmPrepared, msgs>>

\* Resource manager r follows the TM's Commit decision.
RMRcvCommitMsg(r) ==
    /\ [type |-> "Commit"] \in msgs
    /\ rmState' = [rmState EXCEPT ![r] = "committed"]
    /\ UNCHANGED <<tmState, tmPrepared, msgs>>

\* Resource manager r follows the TM's Abort decision.
RMRcvAbortMsg(r) ==
    /\ [type |-> "Abort"] \in msgs
    /\ rmState' = [rmState EXCEPT ![r] = "aborted"]
    /\ UNCHANGED <<tmState, tmPrepared, msgs>>

Next ==
    \/ TMCommit
    \/ TMAbort
    \/ \E r \in RM :
        \/ TMRcvPrepared(r)
        \/ RMPrepare(r)
        \/ RMChooseToAbort(r)
        \/ RMRcvCommitMsg(r)
        \/ RMRcvAbortMsg(r)

Spec == Init /\ [][Next]_vars
=============================================================================
