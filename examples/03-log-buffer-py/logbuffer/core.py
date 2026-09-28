# +nagini
from typing import List

from nagini_contracts.contracts import *


class LogBuffer:
    """A buffer of log lines waiting to be shipped, whose capacity is chosen when it's made.

    Producers write lines at the back; the shipper sends the line at the front
    and removes it once the send goes through. A failed send leaves the line at
    the front and marks the buffer as retrying.
    """

    def __init__(self, capacity: int) -> None:
        Requires(capacity > 0)
        self.capacity = capacity
        self.lines = []  # type: List[int]
        self.retrying = False
        Ensures(Acc(self.capacity) and Acc(self.lines) and Acc(list_pred(self.lines)) and Acc(self.retrying))
        Ensures(self.capacity == capacity and len(self.lines) == 0 and not self.retrying)

    def write(self, line: int) -> bool:
        """Mirrors the TLA+ action Write(p): refused while the buffer is full."""
        Requires(Acc(self.capacity, 1/2) and Acc(self.lines, 1/2) and Acc(list_pred(self.lines)))
        Requires(len(self.lines) <= self.capacity)
        Ensures(Acc(self.capacity, 1/2) and Acc(self.lines, 1/2) and Acc(list_pred(self.lines)))
        Ensures(len(self.lines) <= self.capacity)
        Ensures(Implies(Old(len(self.lines)) < self.capacity, Result() and len(self.lines) == Old(len(self.lines)) + 1))
        Ensures(Implies(Old(len(self.lines)) >= self.capacity, not Result() and len(self.lines) == Old(len(self.lines))))
        Ensures(Forall(int, lambda i: Implies(0 <= i and i < Old(len(self.lines)) and i < len(self.lines), self.lines[i] == Old(self.lines[i]))))
        Ensures(Forall(int, lambda i: Implies(Old(len(self.lines)) <= i and i < len(self.lines), self.lines[i] == line)))
        if len(self.lines) < self.capacity:
            self.lines.append(line)
            return True
        return False

    def ship(self) -> bool:
        """Mirrors the TLA+ action Ship: the oldest line went out, so drop it."""
        Requires(Acc(self.capacity, 1/2) and Acc(self.lines) and Acc(list_pred(self.lines)) and Acc(self.retrying))
        Requires(len(self.lines) <= self.capacity)
        Ensures(Acc(self.capacity, 1/2) and Acc(self.lines) and Acc(list_pred(self.lines)) and Acc(self.retrying))
        Ensures(len(self.lines) <= self.capacity)
        Ensures(Implies(Old(len(self.lines)) > 0, Result() and len(self.lines) == Old(len(self.lines)) - 1 and not self.retrying))
        Ensures(Forall(int, lambda i: Implies(0 <= i and i < len(self.lines) and i + 1 < Old(len(self.lines)), self.lines[i] == Old(self.lines[i + 1]))))
        Ensures(Implies(Old(len(self.lines)) == 0, not Result() and len(self.lines) == 0 and self.retrying == Old(self.retrying)))
        if len(self.lines) > 0:
            self.lines = self.lines[1:]
            self.retrying = False
            return True
        return False

    def ship_fail(self) -> bool:
        """Mirrors the TLA+ action ShipFail: the oldest line stays, to be retried."""
        Requires(Acc(self.lines, 1/2) and Acc(list_pred(self.lines)) and Acc(self.retrying))
        Ensures(Acc(self.lines, 1/2) and Acc(list_pred(self.lines)) and Acc(self.retrying))
        Ensures(len(self.lines) == Old(len(self.lines)))
        Ensures(Forall(int, lambda i: Implies(0 <= i and i < len(self.lines) and i < Old(len(self.lines)), self.lines[i] == Old(self.lines[i]))))
        Ensures(Implies(Old(len(self.lines)) > 0 and not Old(self.retrying), Result() and self.retrying))
        Ensures(Implies(Old(len(self.lines)) == 0 or Old(self.retrying), not Result() and self.retrying == Old(self.retrying)))
        if len(self.lines) > 0 and not self.retrying:
            self.retrying = True
            return True
        return False
