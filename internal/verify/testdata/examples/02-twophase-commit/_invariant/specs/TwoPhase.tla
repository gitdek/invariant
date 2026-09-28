------------------------------ MODULE TwoPhase ------------------------------
(***************************************************************************)
(* Two-phase commit, after Gray and Lamport, "Consensus on Transaction     *)
(* Commit" (2006). A transaction manager (TM) commits only once every      *)
(* resource manager (RM) has prepared. Until it prepares, an RM may choose *)
(* to abort. Messages are never lost or removed, so msgs only grows.       *)
(***************************************************************************)
CONSTANT RM            \* the resource managers

VARIABLES
    rmState,           \* rmState[r] is the state of resource manager r
    tmState,           \* the state of the transaction manager
    tmPrepared,        \* the RMs the TM has received a Prepared message from
    msgs               \* every message ever sent

vars == <<rmState, tmState, tmPrepared, msgs>>

Messages == [type : {"Prepared"}, rm : RM] \cup [type : {"Commit", "Abort"}]

\* Ratified statements. Their text, and the text of everything they depend on,
\* is pinned in ../ratified.lock. The factory may not change them.

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

\* The model.

Init ==
    /\ rmState = [r \in RM |-> "working"]
    /\ tmState = "init"
    /\ tmPrepared = {}
    /\ msgs = {}

TMRcvPrepared(r) ==
    /\ tmState = "init"
    /\ [type |-> "Prepared", rm |-> r] \in msgs
    /\ tmPrepared' = tmPrepared \cup {r}
    /\ UNCHANGED <<rmState, tmState, msgs>>

TMCommit ==
    /\ tmState = "init"
    /\ tmPrepared = RM
    /\ tmState' = "done"
    /\ msgs' = msgs \cup {[type |-> "Commit"]}
    /\ UNCHANGED <<rmState, tmPrepared>>

TMAbort ==
    /\ tmState = "init"
    /\ tmState' = "done"
    /\ msgs' = msgs \cup {[type |-> "Abort"]}
    /\ UNCHANGED <<rmState, tmPrepared>>

RMPrepare(r) ==
    /\ rmState[r] = "working"
    /\ rmState' = [rmState EXCEPT ![r] = "prepared"]
    /\ msgs' = msgs \cup {[type |-> "Prepared", rm |-> r]}
    /\ UNCHANGED <<tmState, tmPrepared>>

RMChooseToAbort(r) ==
    /\ rmState[r] = "working"
    /\ rmState' = [rmState EXCEPT ![r] = "aborted"]
    /\ UNCHANGED <<tmState, tmPrepared, msgs>>

RMRcvCommitMsg(r) ==
    /\ [type |-> "Commit"] \in msgs
    /\ rmState' = [rmState EXCEPT ![r] = "committed"]
    /\ UNCHANGED <<tmState, tmPrepared, msgs>>

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
