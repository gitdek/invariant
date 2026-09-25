# +nagini
from typing import List

from nagini_contracts.contracts import *

# Bounds: Producers = {p1, p2}, Capacity = 2, MaxLines = 2.
NPROD = 2
CAP = 2
MAXLINES = 2
# Longest the write history (and the shipped list) can get: NPROD * MAXLINES.
LOGCAP = 4

# A line <<p, n>> is encoded as the int p * MAXLINES + n, with p in 0..NPROD-1
# and n in 1..MAXLINES, so line codes are 1..LOGCAP and 0 marks an empty slot.
# The code spells MAXLINES as the literal 2 there, which keeps the arithmetic
# linear for the verifier.
# Each sequence is a fixed-length list plus its current length.


class State:
    def __init__(self) -> None:
        self.buf = [0, 0]  # type: List[int]
        self.buf_len = 0
        self.sent = [0, 0, 0, 0]  # type: List[int]
        self.sent_len = 0
        self.log = [0, 0, 0, 0]  # type: List[int]
        self.log_len = 0
        self.written = [0, 0]  # type: List[int]
        self.retrying = False
        Ensures(Acc(self.buf) and Acc(list_pred(self.buf)) and len(self.buf) == CAP)
        Ensures(Acc(self.buf_len) and Acc(self.sent) and Acc(list_pred(self.sent)) and len(self.sent) == LOGCAP)
        Ensures(Acc(self.sent_len) and Acc(self.log) and Acc(list_pred(self.log)) and len(self.log) == LOGCAP)
        Ensures(Acc(self.log_len) and Acc(self.written) and Acc(list_pred(self.written)) and len(self.written) == NPROD)
        Ensures(Acc(self.retrying))
        Ensures(self.buf_len == 0 and self.sent_len == 0 and self.log_len == 0)
        Ensures(Forall(int, lambda i: Implies(0 <= i and i < NPROD, self.written[i] == 0)))
        Ensures(not self.retrying)


def write(s: State, p: int) -> None:
    """Mirrors the TLA+ action Write(p)."""
    Requires(Acc(s.buf) and Acc(list_pred(s.buf)) and len(s.buf) == CAP)
    Requires(Acc(s.buf_len) and Acc(s.sent) and Acc(list_pred(s.sent)) and len(s.sent) == LOGCAP)
    Requires(Acc(s.sent_len) and Acc(s.log) and Acc(list_pred(s.log)) and len(s.log) == LOGCAP)
    Requires(Acc(s.log_len) and Acc(s.written) and Acc(list_pred(s.written)) and len(s.written) == NPROD)
    Requires(Acc(s.retrying))
    Requires(0 <= s.buf_len and s.buf_len <= CAP and 0 <= s.sent_len)
    Requires(s.sent_len + s.buf_len == s.log_len and s.log_len == s.written[0] + s.written[1])
    Requires(0 <= s.written[0] and s.written[0] <= MAXLINES and 0 <= s.written[1] and s.written[1] <= MAXLINES)
    Requires(0 <= p and p < NPROD)
    Requires(s.written[p] < MAXLINES and s.buf_len < CAP)
    Ensures(Acc(s.buf) and Acc(list_pred(s.buf)) and len(s.buf) == CAP)
    Ensures(Acc(s.buf_len) and Acc(s.sent) and Acc(list_pred(s.sent)) and len(s.sent) == LOGCAP)
    Ensures(Acc(s.sent_len) and Acc(s.log) and Acc(list_pred(s.log)) and len(s.log) == LOGCAP)
    Ensures(Acc(s.log_len) and Acc(s.written) and Acc(list_pred(s.written)) and len(s.written) == NPROD)
    Ensures(Acc(s.retrying))
    Ensures(0 <= s.buf_len and s.buf_len <= CAP and 0 <= s.sent_len)
    Ensures(s.sent_len + s.buf_len == s.log_len and s.log_len == s.written[0] + s.written[1])
    Ensures(0 <= s.written[0] and s.written[0] <= MAXLINES and 0 <= s.written[1] and s.written[1] <= MAXLINES)
    Ensures(s.buf_len == Old(s.buf_len) + 1)
    Ensures(s.buf[Old(s.buf_len)] == 2 * p + Old(s.written[p]) + 1)
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < CAP and i != Old(s.buf_len), s.buf[i] == Old(s.buf[i]))))
    Ensures(s.log_len == Old(s.log_len) + 1)
    Ensures(s.log[Old(s.log_len)] == 2 * p + Old(s.written[p]) + 1)
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < LOGCAP and i != Old(s.log_len), s.log[i] == Old(s.log[i]))))
    Ensures(s.written[p] == Old(s.written[p]) + 1)
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < NPROD and i != p, s.written[i] == Old(s.written[i]))))
    Ensures(s.sent_len == Old(s.sent_len))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < LOGCAP, s.sent[i] == Old(s.sent[i]))))
    Ensures(s.retrying == Old(s.retrying))
    line = 2 * p + s.written[p] + 1
    s.buf[s.buf_len] = line
    s.buf_len = s.buf_len + 1
    s.log[s.log_len] = line
    s.log_len = s.log_len + 1
    s.written[p] = s.written[p] + 1


def ship(s: State) -> None:
    """Mirrors the TLA+ action Ship."""
    Requires(Acc(s.buf) and Acc(list_pred(s.buf)) and len(s.buf) == CAP)
    Requires(Acc(s.buf_len) and Acc(s.sent) and Acc(list_pred(s.sent)) and len(s.sent) == LOGCAP)
    Requires(Acc(s.sent_len) and Acc(s.log) and Acc(list_pred(s.log)) and len(s.log) == LOGCAP)
    Requires(Acc(s.log_len) and Acc(s.written) and Acc(list_pred(s.written)) and len(s.written) == NPROD)
    Requires(Acc(s.retrying))
    Requires(0 <= s.buf_len and s.buf_len <= CAP and 0 <= s.sent_len)
    Requires(s.sent_len + s.buf_len == s.log_len and s.log_len == s.written[0] + s.written[1])
    Requires(0 <= s.written[0] and s.written[0] <= MAXLINES and 0 <= s.written[1] and s.written[1] <= MAXLINES)
    Requires(s.buf_len > 0)
    Ensures(Acc(s.buf) and Acc(list_pred(s.buf)) and len(s.buf) == CAP)
    Ensures(Acc(s.buf_len) and Acc(s.sent) and Acc(list_pred(s.sent)) and len(s.sent) == LOGCAP)
    Ensures(Acc(s.sent_len) and Acc(s.log) and Acc(list_pred(s.log)) and len(s.log) == LOGCAP)
    Ensures(Acc(s.log_len) and Acc(s.written) and Acc(list_pred(s.written)) and len(s.written) == NPROD)
    Ensures(Acc(s.retrying))
    Ensures(0 <= s.buf_len and s.buf_len <= CAP and 0 <= s.sent_len)
    Ensures(s.sent_len + s.buf_len == s.log_len and s.log_len == s.written[0] + s.written[1])
    Ensures(0 <= s.written[0] and s.written[0] <= MAXLINES and 0 <= s.written[1] and s.written[1] <= MAXLINES)
    Ensures(s.sent_len == Old(s.sent_len) + 1)
    Ensures(s.sent[Old(s.sent_len)] == Old(s.buf[0]))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < LOGCAP and i != Old(s.sent_len), s.sent[i] == Old(s.sent[i]))))
    Ensures(s.buf_len == Old(s.buf_len) - 1)
    Ensures(s.buf[0] == Old(s.buf[1]) and s.buf[1] == 0)
    Ensures(not s.retrying)
    Ensures(s.log_len == Old(s.log_len))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < LOGCAP, s.log[i] == Old(s.log[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < NPROD, s.written[i] == Old(s.written[i]))))
    s.sent[s.sent_len] = s.buf[0]
    s.sent_len = s.sent_len + 1
    s.buf[0] = s.buf[1]
    s.buf[1] = 0
    s.buf_len = s.buf_len - 1
    s.retrying = False


def ship_fail(s: State) -> None:
    """Mirrors the TLA+ action ShipFail."""
    Requires(Acc(s.buf) and Acc(list_pred(s.buf)) and len(s.buf) == CAP)
    Requires(Acc(s.buf_len) and Acc(s.sent) and Acc(list_pred(s.sent)) and len(s.sent) == LOGCAP)
    Requires(Acc(s.sent_len) and Acc(s.log) and Acc(list_pred(s.log)) and len(s.log) == LOGCAP)
    Requires(Acc(s.log_len) and Acc(s.written) and Acc(list_pred(s.written)) and len(s.written) == NPROD)
    Requires(Acc(s.retrying))
    Requires(s.buf_len > 0 and not s.retrying)
    Ensures(Acc(s.buf) and Acc(list_pred(s.buf)) and len(s.buf) == CAP)
    Ensures(Acc(s.buf_len) and Acc(s.sent) and Acc(list_pred(s.sent)) and len(s.sent) == LOGCAP)
    Ensures(Acc(s.sent_len) and Acc(s.log) and Acc(list_pred(s.log)) and len(s.log) == LOGCAP)
    Ensures(Acc(s.log_len) and Acc(s.written) and Acc(list_pred(s.written)) and len(s.written) == NPROD)
    Ensures(Acc(s.retrying))
    Ensures(s.retrying)
    Ensures(s.buf_len == Old(s.buf_len) and s.sent_len == Old(s.sent_len) and s.log_len == Old(s.log_len))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < CAP, s.buf[i] == Old(s.buf[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < LOGCAP, s.sent[i] == Old(s.sent[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < LOGCAP, s.log[i] == Old(s.log[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < NPROD, s.written[i] == Old(s.written[i]))))
    s.retrying = True


def done(s: State) -> None:
    """Mirrors the TLA+ action Done: a stutter once everything is written and shipped."""
    Requires(Acc(s.buf) and Acc(list_pred(s.buf)) and len(s.buf) == CAP)
    Requires(Acc(s.buf_len) and Acc(s.sent) and Acc(list_pred(s.sent)) and len(s.sent) == LOGCAP)
    Requires(Acc(s.sent_len) and Acc(s.log) and Acc(list_pred(s.log)) and len(s.log) == LOGCAP)
    Requires(Acc(s.log_len) and Acc(s.written) and Acc(list_pred(s.written)) and len(s.written) == NPROD)
    Requires(Acc(s.retrying))
    Requires(Forall(int, lambda i: Implies(0 <= i and i < NPROD, s.written[i] == MAXLINES)))
    Requires(s.buf_len == 0)
    Ensures(Acc(s.buf) and Acc(list_pred(s.buf)) and len(s.buf) == CAP)
    Ensures(Acc(s.buf_len) and Acc(s.sent) and Acc(list_pred(s.sent)) and len(s.sent) == LOGCAP)
    Ensures(Acc(s.sent_len) and Acc(s.log) and Acc(list_pred(s.log)) and len(s.log) == LOGCAP)
    Ensures(Acc(s.log_len) and Acc(s.written) and Acc(list_pred(s.written)) and len(s.written) == NPROD)
    Ensures(Acc(s.retrying))
    Ensures(s.buf_len == Old(s.buf_len) and s.sent_len == Old(s.sent_len) and s.log_len == Old(s.log_len))
    Ensures(s.retrying == Old(s.retrying))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < CAP, s.buf[i] == Old(s.buf[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < LOGCAP, s.sent[i] == Old(s.sent[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < LOGCAP, s.log[i] == Old(s.log[i]))))
    Ensures(Forall(int, lambda i: Implies(0 <= i and i < NPROD, s.written[i] == Old(s.written[i]))))
    pass
