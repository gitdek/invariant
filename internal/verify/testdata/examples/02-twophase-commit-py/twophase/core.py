# +nagini
"""The state of a two-phase commit, and the operations that change it.

Participants are numbered 0 to n - 1. A participant's state is 0 while it's
working, 1 once it has prepared, 2 once it has committed and 3 once it has
aborted. The messages the participants and the coordinator exchange are
carried by whoever runs the transaction; this is what each side remembers.
"""

from typing import List

from nagini_contracts.contracts import *


class TwoPhaseCommit:
    """A two-phase commit among n participants and one coordinator."""

    def __init__(self, n: int) -> None:
        Requires(n > 0)
        states = []  # type: List[int]
        votes = []  # type: List[bool]
        i = 0
        while i < n:
            Invariant(Acc(list_pred(states)) and Acc(list_pred(votes)))
            Invariant(0 <= i and i <= n and len(states) == i and len(votes) == i)
            Invariant(Forall(int, lambda j: Implies(0 <= j and j < i, states[j] == 0)))
            Invariant(Forall(int, lambda j: Implies(0 <= j and j < i, not votes[j])))
            states.append(0)
            votes.append(False)
            i += 1
        self.n = n  # type: int
        self.states = states  # type: List[int]
        self.votes = votes  # type: List[bool]
        self.decided = False  # type: bool
        Ensures(Acc(self.n) and Acc(self.states) and Acc(list_pred(self.states)))
        Ensures(Acc(self.votes) and Acc(list_pred(self.votes)) and Acc(self.decided))
        Ensures(self.n == n and len(self.states) == n and len(self.votes) == n)
        Ensures(not self.decided)
        Ensures(Forall(int, lambda j: Implies(0 <= j and j < n, self.states[j] == 0)))
        Ensures(Forall(int, lambda j: Implies(0 <= j and j < n, not self.votes[j])))

    def prepare(self, r: int) -> bool:
        """A working participant prepares and votes to commit."""
        Requires(Acc(self.n, 1/2) and Acc(self.states, 1/2) and Acc(list_pred(self.states)))
        Requires(len(self.states) == self.n and 0 <= r and r < self.n)
        Ensures(Acc(self.n, 1/2) and Acc(self.states, 1/2) and Acc(list_pred(self.states)))
        Ensures(self.n == Old(self.n) and len(self.states) == self.n)
        Ensures(Result() == (Old(self.states[r]) == 0))
        Ensures(Implies(Result(), self.states[r] == 1))
        Ensures(Implies(not Result(), self.states[r] == Old(self.states[r])))
        Ensures(Forall(int, lambda j: Implies(0 <= j and j < self.n and j != r,
                                              self.states[j] == Old(self.states[j]))))
        if self.states[r] != 0:
            return False
        self.states[r] = 1
        return True

    def give_up(self, r: int) -> bool:
        """A participant that hasn't prepared gives up on its own."""
        Requires(Acc(self.n, 1/2) and Acc(self.states, 1/2) and Acc(list_pred(self.states)))
        Requires(len(self.states) == self.n and 0 <= r and r < self.n)
        Ensures(Acc(self.n, 1/2) and Acc(self.states, 1/2) and Acc(list_pred(self.states)))
        Ensures(self.n == Old(self.n) and len(self.states) == self.n)
        Ensures(Result() == (Old(self.states[r]) == 0))
        Ensures(Implies(Result(), self.states[r] == 3))
        Ensures(Implies(not Result(), self.states[r] == Old(self.states[r])))
        Ensures(Forall(int, lambda j: Implies(0 <= j and j < self.n and j != r,
                                              self.states[j] == Old(self.states[j]))))
        if self.states[r] != 0:
            return False
        self.states[r] = 3
        return True

    def record_vote(self, r: int) -> bool:
        """The coordinator records participant r's vote, which it has received."""
        Requires(Acc(self.n, 1/2) and Acc(self.decided, 1/2))
        Requires(Acc(self.votes, 1/2) and Acc(list_pred(self.votes)))
        Requires(len(self.votes) == self.n and 0 <= r and r < self.n)
        Ensures(Acc(self.n, 1/2) and Acc(self.decided, 1/2))
        Ensures(Acc(self.votes, 1/2) and Acc(list_pred(self.votes)))
        Ensures(self.n == Old(self.n) and len(self.votes) == self.n)
        Ensures(Result() == (not self.decided))
        Ensures(Implies(Result(), self.votes[r]))
        Ensures(Implies(not Result(), self.votes[r] == Old(self.votes[r])))
        Ensures(Forall(int, lambda j: Implies(0 <= j and j < self.n and j != r,
                                              self.votes[j] == Old(self.votes[j]))))
        if self.decided:
            return False
        self.votes[r] = True
        return True

    def commit(self) -> bool:
        """The coordinator commits, once every participant has voted."""
        Requires(Acc(self.n, 1/2) and Acc(self.decided))
        Requires(Acc(self.votes, 1/2) and Acc(list_pred(self.votes)))
        Requires(len(self.votes) == self.n)
        Ensures(Acc(self.n, 1/2) and Acc(self.decided))
        Ensures(Acc(self.votes, 1/2) and Acc(list_pred(self.votes)))
        Ensures(self.n == Old(self.n) and len(self.votes) == self.n)
        Ensures(Forall(int, lambda j: Implies(0 <= j and j < self.n,
                                              self.votes[j] == Old(self.votes[j]))))
        Ensures(Result() == (not Old(self.decided) and
                             Forall(int, lambda j: Implies(0 <= j and j < self.n, self.votes[j]))))
        Ensures(self.decided == (Old(self.decided) or Result()))
        if self.decided:
            return False
        votes = self.votes
        n = self.n
        ok = True
        i = 0
        while i < n:
            Invariant(Acc(list_pred(votes)))
            Invariant(len(votes) == n and 0 <= i and i <= n)
            Invariant(Forall(int, lambda j: Implies(0 <= j and j < n, votes[j] == Old(self.votes[j]))))
            Invariant(ok == Forall(int, lambda j: Implies(0 <= j and j < i, votes[j])))
            if not votes[i]:
                ok = False
            i += 1
        if not ok:
            return False
        self.decided = True
        return True

    def abort(self) -> bool:
        """The coordinator aborts. It can do that any time before it has decided."""
        Requires(Acc(self.decided))
        Ensures(Acc(self.decided))
        Ensures(Result() == (not Old(self.decided)))
        Ensures(self.decided)
        if self.decided:
            return False
        self.decided = True
        return True

    def learn_commit(self, r: int) -> None:
        """Participant r learns that the coordinator committed, and commits."""
        Requires(Acc(self.n, 1/2) and Acc(self.states, 1/2) and Acc(list_pred(self.states)))
        Requires(len(self.states) == self.n and 0 <= r and r < self.n)
        Ensures(Acc(self.n, 1/2) and Acc(self.states, 1/2) and Acc(list_pred(self.states)))
        Ensures(self.n == Old(self.n) and len(self.states) == self.n)
        Ensures(self.states[r] == 2)
        Ensures(Forall(int, lambda j: Implies(0 <= j and j < self.n and j != r,
                                              self.states[j] == Old(self.states[j]))))
        self.states[r] = 2

    def learn_abort(self, r: int) -> None:
        """Participant r learns that the coordinator aborted, and aborts."""
        Requires(Acc(self.n, 1/2) and Acc(self.states, 1/2) and Acc(list_pred(self.states)))
        Requires(len(self.states) == self.n and 0 <= r and r < self.n)
        Ensures(Acc(self.n, 1/2) and Acc(self.states, 1/2) and Acc(list_pred(self.states)))
        Ensures(self.n == Old(self.n) and len(self.states) == self.n)
        Ensures(self.states[r] == 3)
        Ensures(Forall(int, lambda j: Implies(0 <= j and j < self.n and j != r,
                                              self.states[j] == Old(self.states[j]))))
        self.states[r] = 3
