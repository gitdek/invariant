# +nagini
"""Two-phase commit's core as a state machine, proved with Nagini.

Each step function restates one action of ../.invariant/specs/TwoPhase.tla.
Requires is the action's enabling condition. Ensures is its effect, including
everything the action leaves unchanged. Nagini proves every contract, every
list index, and that each function holds exactly the permissions it claims.
"""

from typing import List

from nagini_contracts.contracts import *

N = 3  # resource managers, matching the bound RM = {r1, r2, r3}

WORKING = 0
PREPARED = 1
COMMITTED = 2
ABORTED = 3


class State:
    """The spec's variables, one to one.

    rm[r] is rmState[r]; tm_done is tmState = "done"; tm_prepared[r] is
    r \\in tmPrepared; prepared_msg[r], commit_msg and abort_msg say which
    messages msgs holds.
    """

    def __init__(self) -> None:
        """Mirrors Init."""
        self.rm = [WORKING, WORKING, WORKING]  # type: List[int]
        self.tm_done = False
        self.tm_prepared = [False, False, False]  # type: List[bool]
        self.prepared_msg = [False, False, False]  # type: List[bool]
        self.commit_msg = False
        self.abort_msg = False
        Ensures(Acc(self.rm) and Acc(list_pred(self.rm)) and len(self.rm) == N)
        Ensures(Acc(self.tm_prepared) and Acc(list_pred(self.tm_prepared)) and len(self.tm_prepared) == N)
        Ensures(Acc(self.prepared_msg) and Acc(list_pred(self.prepared_msg)) and len(self.prepared_msg) == N)
        Ensures(Acc(self.tm_done) and Acc(self.commit_msg) and Acc(self.abort_msg))
        Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, self.rm[i] == WORKING and not self.tm_prepared[i] and not self.prepared_msg[i])))
        Ensures(not self.tm_done and not self.commit_msg and not self.abort_msg)


def tm_rcv_prepared(s: State, r: int) -> None:
    """Mirrors TMRcvPrepared(r): the coordinator records r's Prepared message."""
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Requires(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Requires(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Requires(0 <= r and r < N)
    Requires(not s.tm_done and s.prepared_msg[r])
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Ensures(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Ensures(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Ensures(s.tm_prepared[r])
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N and i != r, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.prepared_msg[i] == Old(s.prepared_msg[i]))))
    Ensures(s.tm_done == Old(s.tm_done) and s.commit_msg == Old(s.commit_msg) and s.abort_msg == Old(s.abort_msg))
    s.tm_prepared[r] = True


def tm_commit(s: State) -> None:
    """Mirrors TMCommit: once every resource manager has prepared, the coordinator commits."""
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Requires(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Requires(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Requires(not s.tm_done)
    Requires(Forall(int, lambda i: Implies(0 <= i and i < N, s.tm_prepared[i])))
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Ensures(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Ensures(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Ensures(s.tm_done and s.commit_msg)
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.prepared_msg[i] == Old(s.prepared_msg[i]))))
    Ensures(s.abort_msg == Old(s.abort_msg))
    s.tm_done = True
    s.commit_msg = True


def tm_abort(s: State) -> None:
    """Mirrors TMAbort: the coordinator may abort any time before it has decided."""
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Requires(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Requires(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Requires(not s.tm_done)
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Ensures(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Ensures(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Ensures(s.tm_done and s.abort_msg)
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.prepared_msg[i] == Old(s.prepared_msg[i]))))
    Ensures(s.commit_msg == Old(s.commit_msg))
    s.tm_done = True
    s.abort_msg = True


def rm_prepare(s: State, r: int) -> None:
    """Mirrors RMPrepare(r): a working resource manager prepares and tells the coordinator."""
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Requires(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Requires(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Requires(0 <= r and r < N)
    Requires(s.rm[r] == WORKING)
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Ensures(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Ensures(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Ensures(s.rm[r] == PREPARED and s.prepared_msg[r])
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N and i != r, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N and i != r, s.prepared_msg[i] == Old(s.prepared_msg[i]))))
    Ensures(s.tm_done == Old(s.tm_done) and s.commit_msg == Old(s.commit_msg) and s.abort_msg == Old(s.abort_msg))
    s.rm[r] = PREPARED
    s.prepared_msg[r] = True


def rm_choose_to_abort(s: State, r: int) -> None:
    """Mirrors RMChooseToAbort(r): a resource manager that hasn't prepared aborts on its own."""
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Requires(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Requires(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Requires(0 <= r and r < N)
    Requires(s.rm[r] == WORKING)
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Ensures(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Ensures(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Ensures(s.rm[r] == ABORTED)
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N and i != r, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.prepared_msg[i] == Old(s.prepared_msg[i]))))
    Ensures(s.tm_done == Old(s.tm_done) and s.commit_msg == Old(s.commit_msg) and s.abort_msg == Old(s.abort_msg))
    s.rm[r] = ABORTED


def rm_rcv_commit_msg(s: State, r: int) -> None:
    """Mirrors RMRcvCommitMsg(r): r commits once the Commit message has been sent."""
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Requires(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Requires(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Requires(0 <= r and r < N)
    Requires(s.commit_msg)
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Ensures(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Ensures(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Ensures(s.rm[r] == COMMITTED)
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N and i != r, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.prepared_msg[i] == Old(s.prepared_msg[i]))))
    Ensures(s.tm_done == Old(s.tm_done) and s.commit_msg == Old(s.commit_msg) and s.abort_msg == Old(s.abort_msg))
    s.rm[r] = COMMITTED


def rm_rcv_abort_msg(s: State, r: int) -> None:
    """Mirrors RMRcvAbortMsg(r): r aborts once the Abort message has been sent."""
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Requires(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Requires(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Requires(0 <= r and r < N)
    Requires(s.abort_msg)
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == N)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == N)
    Ensures(Acc(s.prepared_msg) and Acc(list_pred(s.prepared_msg)) and len(s.prepared_msg) == N)
    Ensures(Acc(s.tm_done) and Acc(s.commit_msg) and Acc(s.abort_msg))
    Ensures(s.rm[r] == ABORTED)
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N and i != r, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, s.prepared_msg[i] == Old(s.prepared_msg[i]))))
    Ensures(s.tm_done == Old(s.tm_done) and s.commit_msg == Old(s.commit_msg) and s.abort_msg == Old(s.abort_msg))
    s.rm[r] = ABORTED
