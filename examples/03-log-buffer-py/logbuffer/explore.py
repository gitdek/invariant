"""State-space helpers for the core: canonical keys, copies, and Next's successors."""
from typing import List, Tuple

from logbuffer.core import CAP, MAXLINES, NPROD, State, done, ship, ship_fail, write


def key(s: State) -> Tuple:
    """A canonical tuple; slots past each sequence's length are ignored."""
    return (
        tuple(s.buf[: s.buf_len]),
        tuple(s.sent[: s.sent_len]),
        tuple(s.log[: s.log_len]),
        tuple(s.written),
        s.retrying,
    )


def copy(s: State) -> State:
    t = State()
    t.buf = list(s.buf)
    t.buf_len = s.buf_len
    t.sent = list(s.sent)
    t.sent_len = s.sent_len
    t.log = list(s.log)
    t.log_len = s.log_len
    t.written = list(s.written)
    t.retrying = s.retrying
    return t


def successors(s: State) -> List[State]:
    """The state after every enabled action, the way TLC expands Next."""
    out = []  # type: List[State]
    for p in range(NPROD):
        if s.written[p] < MAXLINES and s.buf_len < CAP:
            t = copy(s)
            write(t, p)
            out.append(t)
    if s.buf_len > 0:
        t = copy(s)
        ship(t)
        out.append(t)
    if s.buf_len > 0 and not s.retrying:
        t = copy(s)
        ship_fail(t)
        out.append(t)
    if all(w == MAXLINES for w in s.written) and s.buf_len == 0:
        t = copy(s)
        done(t)
        out.append(t)
    return out
