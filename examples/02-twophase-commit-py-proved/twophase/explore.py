"""Exploration for the proved core.

This is plain Python, so Nagini doesn't see it. It copies states and applies
every action enabled in a state, the way TLC expands Next. The gate's
conformance check covers it: every step it produces must be a step the model
allows.
"""

from typing import Callable, List, Tuple

from twophase.core import (
    N,
    WORKING,
    State,
    rm_choose_to_abort,
    rm_prepare,
    rm_rcv_abort_msg,
    rm_rcv_commit_msg,
    tm_abort,
    tm_commit,
    tm_rcv_prepared,
)

Key = Tuple[Tuple[int, ...], bool, Tuple[bool, ...], Tuple[bool, ...], bool, bool]


def key(s: State) -> Key:
    """A hashable snapshot of a state."""
    return (tuple(s.rm), s.tm_done, tuple(s.tm_prepared), tuple(s.prepared_msg), s.commit_msg, s.abort_msg)


def copy(s: State) -> State:
    t = State()
    t.rm = list(s.rm)
    t.tm_done = s.tm_done
    t.tm_prepared = list(s.tm_prepared)
    t.prepared_msg = list(s.prepared_msg)
    t.commit_msg = s.commit_msg
    t.abort_msg = s.abort_msg
    return t


def successors(s: State) -> List[State]:
    """The state after every action enabled in s."""
    out: List[State] = []

    def step(action: Callable[..., None], *args: int) -> None:
        t = copy(s)
        action(t, *args)
        out.append(t)

    if not s.tm_done and all(s.tm_prepared):
        step(tm_commit)
    if not s.tm_done:
        step(tm_abort)
    for r in range(N):
        if not s.tm_done and s.prepared_msg[r]:
            step(tm_rcv_prepared, r)
        if s.rm[r] == WORKING:
            step(rm_prepare, r)
            step(rm_choose_to_abort, r)
        if s.commit_msg:
            step(rm_rcv_commit_msg, r)
        if s.abort_msg:
            step(rm_rcv_abort_msg, r)
    return out
