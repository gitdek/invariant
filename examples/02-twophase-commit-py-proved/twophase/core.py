# +nagini
"""Two-phase commit's core as a state machine, proved with Nagini.

Each step function restates one action of ../.invariant/specs/TwoPhase.tla.
It returns whether it ran: it runs exactly when the action's enabling
condition holds in the core's own state, and otherwise leaves the state as it
was. Ensures says both outcomes, including everything the action leaves
unchanged. The number of resource managers is a parameter, so every contract
holds at every size. The messages are the network's, not the core's: the
explorer delivers them, so an action that needs a message runs only when the
explorer has it to deliver.
"""

from typing import List

from nagini_contracts.contracts import *

WORKING = 0
PREPARED = 1
COMMITTED = 2
ABORTED = 3


class State:
    """The system's variables.

    rm[r] is rmState[r]; tm_done is tmState = "done"; tm_prepared[r] is
    r \\in tmPrepared. n is the number of resource managers.
    """

    def __init__(self, n: int) -> None:
        """Mirrors Init, for n resource managers."""
        Requires(n > 0)
        rm = []  # type: List[int]
        tm_prepared = []  # type: List[bool]
        i = 0
        while i < n:
            Invariant(Acc(list_pred(rm)) and Acc(list_pred(tm_prepared)))
            Invariant(0 <= i and i <= n and len(rm) == i and len(tm_prepared) == i)
            Invariant(Forall(int, lambda j: Implies(0 <= j and j < i, rm[j] == WORKING and not tm_prepared[j])))
            rm.append(WORKING)
            tm_prepared.append(False)
            i += 1
        self.n = n  # type: int
        self.rm = rm  # type: List[int]
        self.tm_done = False  # type: bool
        self.tm_prepared = tm_prepared  # type: List[bool]
        Ensures(Acc(self.n) and Acc(self.tm_done))
        Ensures(Acc(self.rm) and Acc(list_pred(self.rm)) and len(self.rm) == n)
        Ensures(Acc(self.tm_prepared) and Acc(list_pred(self.tm_prepared)) and len(self.tm_prepared) == n)
        Ensures(self.n == n and not self.tm_done)
        Ensures(Forall(int, lambda i: Implies(0 <= i and i < n, self.rm[i] == WORKING and not self.tm_prepared[i])))


def all_prepared(s: State) -> bool:
    """Whether the coordinator has every resource manager's Prepared message."""
    Requires(Acc(s.n, 1/4) and Acc(s.tm_prepared, 1/2) and Acc(list_pred(s.tm_prepared), 1/2))
    Requires(len(s.tm_prepared) == s.n)
    Ensures(Acc(s.n, 1/4) and Acc(s.tm_prepared, 1/2) and Acc(list_pred(s.tm_prepared), 1/2))
    Ensures(len(s.tm_prepared) == s.n)
    Ensures(Result() == Forall(int, lambda i: Implies(0 <= i and i < s.n, s.tm_prepared[i])))
    ok = True
    bad = 0
    i = 0
    while i < s.n:
        Invariant(Acc(s.n, 1/4) and Acc(s.tm_prepared, 1/2) and Acc(list_pred(s.tm_prepared), 1/2))
        Invariant(len(s.tm_prepared) == s.n)
        Invariant(0 <= i and i <= s.n)
        Invariant(Implies(ok, Forall(int, lambda j: Implies(0 <= j and j < i, s.tm_prepared[j]))))
        Invariant(Implies(not ok, 0 <= bad and bad < s.n and not s.tm_prepared[bad]))
        if not s.tm_prepared[i]:
            ok = False
            bad = i
        i += 1
    return ok


def tm_rcv_prepared(s: State, r: int) -> bool:
    """Mirrors TMRcvPrepared(r): the coordinator records r's Prepared message, until it has decided."""
    Requires(Acc(s.n, 1/2) and Acc(s.tm_done))
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Requires(0 <= r and r < s.n)
    Ensures(Acc(s.n, 1/2) and s.n == Old(s.n) and Acc(s.tm_done))
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Ensures(Result() == (not Old(s.tm_done)))
    Ensures(Implies(Result(), s.tm_prepared[r]))
    Ensures(Implies(not Result(), s.tm_prepared[r] == Old(s.tm_prepared[r])))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n and i != r, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(s.tm_done == Old(s.tm_done))
    if s.tm_done:
        return False
    s.tm_prepared[r] = True
    return True


def tm_commit(s: State) -> bool:
    """Mirrors TMCommit: once every resource manager has prepared, the coordinator commits."""
    Requires(Acc(s.n, 1/2) and Acc(s.tm_done))
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Ensures(Acc(s.n, 1/2) and s.n == Old(s.n) and Acc(s.tm_done))
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Ensures(Result() == (not Old(s.tm_done) and Forall(int, lambda i: Implies(0 <= i and i < s.n, s.tm_prepared[i]))))
    Ensures(Implies(Result(), s.tm_done))
    Ensures(Implies(not Result(), s.tm_done == Old(s.tm_done)))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    if s.tm_done:
        return False
    if not all_prepared(s):
        return False
    s.tm_done = True
    return True


def tm_abort(s: State) -> bool:
    """Mirrors TMAbort: the coordinator may abort any time before it has decided."""
    Requires(Acc(s.n, 1/2) and Acc(s.tm_done))
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Ensures(Acc(s.n, 1/2) and s.n == Old(s.n) and Acc(s.tm_done))
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Ensures(Result() == (not Old(s.tm_done)))
    Ensures(s.tm_done)
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    if s.tm_done:
        return False
    s.tm_done = True
    return True


def rm_prepare(s: State, r: int) -> bool:
    """Mirrors RMPrepare(r): a working resource manager prepares."""
    Requires(Acc(s.n, 1/2) and Acc(s.tm_done))
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Requires(0 <= r and r < s.n)
    Ensures(Acc(s.n, 1/2) and s.n == Old(s.n) and Acc(s.tm_done))
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Ensures(Result() == (Old(s.rm[r]) == WORKING))
    Ensures(Implies(Result(), s.rm[r] == PREPARED))
    Ensures(Implies(not Result(), s.rm[r] == Old(s.rm[r])))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n and i != r, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(s.tm_done == Old(s.tm_done))
    if s.rm[r] != WORKING:
        return False
    s.rm[r] = PREPARED
    return True


def rm_choose_to_abort(s: State, r: int) -> bool:
    """Mirrors RMChooseToAbort(r): a resource manager that hasn't prepared aborts on its own."""
    Requires(Acc(s.n, 1/2) and Acc(s.tm_done))
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Requires(0 <= r and r < s.n)
    Ensures(Acc(s.n, 1/2) and s.n == Old(s.n) and Acc(s.tm_done))
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Ensures(Result() == (Old(s.rm[r]) == WORKING))
    Ensures(Implies(Result(), s.rm[r] == ABORTED))
    Ensures(Implies(not Result(), s.rm[r] == Old(s.rm[r])))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n and i != r, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(s.tm_done == Old(s.tm_done))
    if s.rm[r] != WORKING:
        return False
    s.rm[r] = ABORTED
    return True


def rm_rcv_commit_msg(s: State, r: int) -> None:
    """Mirrors RMRcvCommitMsg(r): r commits when the Commit message reaches it."""
    Requires(Acc(s.n, 1/2) and Acc(s.tm_done))
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Requires(0 <= r and r < s.n)
    Ensures(Acc(s.n, 1/2) and s.n == Old(s.n) and Acc(s.tm_done))
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Ensures(s.rm[r] == COMMITTED)
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n and i != r, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(s.tm_done == Old(s.tm_done))
    s.rm[r] = COMMITTED


def rm_rcv_abort_msg(s: State, r: int) -> None:
    """Mirrors RMRcvAbortMsg(r): r aborts when the Abort message reaches it."""
    Requires(Acc(s.n, 1/2) and Acc(s.tm_done))
    Requires(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Requires(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Requires(0 <= r and r < s.n)
    Ensures(Acc(s.n, 1/2) and s.n == Old(s.n) and Acc(s.tm_done))
    Ensures(Acc(s.rm) and Acc(list_pred(s.rm)) and len(s.rm) == s.n)
    Ensures(Acc(s.tm_prepared) and Acc(list_pred(s.tm_prepared)) and len(s.tm_prepared) == s.n)
    Ensures(s.rm[r] == ABORTED)
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n and i != r, s.rm[i] == Old(s.rm[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < s.n, s.tm_prepared[i] == Old(s.tm_prepared[i]))))
    Ensures(s.tm_done == Old(s.tm_done))
    s.rm[r] = ABORTED
