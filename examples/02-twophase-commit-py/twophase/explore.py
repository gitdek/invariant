"""The environment of the two-phase commit, for Invariant's explorer.

This is the only place the model's bounds live. A node holds the core's state,
read out of it, and the messages sent so far, which the environment carries.
"""

from __future__ import annotations

from typing import Any, FrozenSet, NamedTuple, Optional, Tuple

from invariant_explore import Step

from twophase.core import TwoPhaseCommit

# The resource managers, r1 to rRM.
RM = 3

# The spec's name for each state the core numbers.
STATE_NAMES = ("working", "prepared", "committed", "aborted")

# Messages: ("Prepared", r) for r's vote, ("Commit",) and ("Abort",) for the
# coordinator's decisions.
Msg = Tuple[Any, ...]
COMMIT: Msg = ("Commit",)
ABORT: Msg = ("Abort",)


class Node(NamedTuple):
    states: Tuple[int, ...]
    votes: Tuple[bool, ...]
    decided: bool
    msgs: FrozenSet[Msg]


def rm_name(r: int) -> str:
    return f"r{r + 1}"


def rm_index(value: Any) -> int:
    return int(value["$mv"][1:]) - 1


def core_of(node: Node) -> TwoPhaseCommit:
    """The core in the state the node records."""
    core = TwoPhaseCommit(RM)
    core.states = list(node.states)
    core.votes = list(node.votes)
    core.decided = node.decided
    return core


def read(core: TwoPhaseCommit, msgs: FrozenSet[Msg]) -> Node:
    return Node(tuple(core.states), tuple(core.votes), core.decided, msgs)


initial = [read(TwoPhaseCommit(RM), frozenset())]


def mv(name: str) -> dict:
    return {"$mv": name}


def message(m: Msg) -> dict:
    if m[0] == "Prepared":
        return {"type": "Prepared", "rm": mv(rm_name(m[1]))}
    return {"type": m[0]}


def abstract(node: Node) -> dict:
    """The node's state as the spec's variables."""
    return {
        "rmState": {"$fn": [[mv(rm_name(r)), STATE_NAMES[s]] for r, s in enumerate(node.states)]},
        "tmState": "done" if node.decided else "init",
        "tmPrepared": {"$set": [mv(rm_name(r)) for r, v in enumerate(node.votes) if v]},
        "msgs": {"$set": [message(m) for m in sorted(node.msgs, key=repr)]},
    }


def tm_commit(node: Node) -> Optional[Node]:
    core = core_of(node)
    if not core.commit():
        return node
    return read(core, node.msgs | {COMMIT})


def tm_abort(node: Node) -> Optional[Node]:
    core = core_of(node)
    if not core.abort():
        return node
    return read(core, node.msgs | {ABORT})


def tm_rcv_prepared(node: Node, rm: Any) -> Optional[Node]:
    r = rm_index(rm)
    if ("Prepared", r) not in node.msgs:
        return node  # nothing to receive yet
    core = core_of(node)
    core.record_vote(r)
    return read(core, node.msgs)


def rm_prepare(node: Node, rm: Any) -> Optional[Node]:
    r = rm_index(rm)
    core = core_of(node)
    if not core.prepare(r):
        return node
    return read(core, node.msgs | {("Prepared", r)})


def rm_choose_to_abort(node: Node, rm: Any) -> Optional[Node]:
    core = core_of(node)
    core.give_up(rm_index(rm))
    return read(core, node.msgs)


def rm_rcv_commit_msg(node: Node, rm: Any) -> Optional[Node]:
    if COMMIT not in node.msgs:
        return node  # nothing to receive yet
    core = core_of(node)
    core.learn_commit(rm_index(rm))
    return read(core, node.msgs)


def rm_rcv_abort_msg(node: Node, rm: Any) -> Optional[Node]:
    if ABORT not in node.msgs:
        return node  # nothing to receive yet
    core = core_of(node)
    core.learn_abort(rm_index(rm))
    return read(core, node.msgs)


rms = [[mv(rm_name(r))] for r in range(RM)]

steps = [
    Step("TMCommit", tm_commit),
    Step("TMAbort", tm_abort),
    Step("TMRcvPrepared", tm_rcv_prepared, rms),
    Step("RMPrepare", rm_prepare, rms),
    Step("RMChooseToAbort", rm_choose_to_abort, rms),
    Step("RMRcvCommitMsg", rm_rcv_commit_msg, rms),
    Step("RMRcvAbortMsg", rm_rcv_abort_msg, rms),
]
