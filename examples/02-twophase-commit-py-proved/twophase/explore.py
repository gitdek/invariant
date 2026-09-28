"""The environment around the proved core, for Invariant's harness.

This is plain Python, so Nagini doesn't see it, and it's the only place the
model's bounds live. A node is the core's state, read out of it, and the
network's: msgs, every message ever sent. Each step makes a core from a node,
calls the operation, and reads the state back. When the core refuses, the
step returns the node it got. A step that needs a message the network doesn't
hold can't happen yet, so it also returns the node it got.
"""

from typing import Any, Dict, FrozenSet, List, Tuple

from invariant_explore import Step

from twophase.core import (
    ABORTED,
    COMMITTED,
    PREPARED,
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

RM = 3  # the resource managers, r1 .. rRM

NAMES = [f"r{i + 1}" for i in range(RM)]
STATES = {WORKING: "working", PREPARED: "prepared", COMMITTED: "committed", ABORTED: "aborted"}

# A message is ("Prepared", r), ("Commit", -1) or ("Abort", -1).
Msg = Tuple[str, int]
COMMIT: Msg = ("Commit", -1)
ABORT: Msg = ("Abort", -1)

# (rm, tm_done, tm_prepared, msgs)
Node = Tuple[Tuple[int, ...], bool, Tuple[bool, ...], FrozenSet[Msg]]


def core(node: Node) -> State:
    """The core in node's state."""
    s = State(RM)
    s.rm = list(node[0])
    s.tm_done = node[1]
    s.tm_prepared = list(node[2])
    return s


def read(s: State, msgs: FrozenSet[Msg]) -> Node:
    """The node holding the core's state s and the network's msgs."""
    return (tuple(s.rm), s.tm_done, tuple(s.tm_prepared), msgs)


def initial() -> List[Node]:
    """Init: a fresh core and an empty network."""
    return [read(State(RM), frozenset())]


def index(r: Dict[str, str]) -> int:
    return NAMES.index(r["$mv"])


def take_tm_commit(node: Node) -> Node:
    s = core(node)
    if not tm_commit(s):
        return node
    return read(s, node[3] | {COMMIT})


def take_tm_abort(node: Node) -> Node:
    s = core(node)
    if not tm_abort(s):
        return node
    return read(s, node[3] | {ABORT})


def take_tm_rcv_prepared(node: Node, r: Dict[str, str]) -> Node:
    i = index(r)
    if ("Prepared", i) not in node[3]:
        return node
    s = core(node)
    if not tm_rcv_prepared(s, i):
        return node
    return read(s, node[3])


def take_rm_prepare(node: Node, r: Dict[str, str]) -> Node:
    i = index(r)
    s = core(node)
    if not rm_prepare(s, i):
        return node
    return read(s, node[3] | {("Prepared", i)})


def take_rm_choose_to_abort(node: Node, r: Dict[str, str]) -> Node:
    s = core(node)
    if not rm_choose_to_abort(s, index(r)):
        return node
    return read(s, node[3])


def take_rm_rcv_commit_msg(node: Node, r: Dict[str, str]) -> Node:
    if COMMIT not in node[3]:
        return node
    s = core(node)
    rm_rcv_commit_msg(s, index(r))
    return read(s, node[3])


def take_rm_rcv_abort_msg(node: Node, r: Dict[str, str]) -> Node:
    if ABORT not in node[3]:
        return node
    s = core(node)
    rm_rcv_abort_msg(s, index(r))
    return read(s, node[3])


def steps() -> List[Step]:
    """Every step Next names, with every resource manager it may name."""
    rms = [[{"$mv": name}] for name in NAMES]
    return [
        Step("TMCommit", take_tm_commit),
        Step("TMAbort", take_tm_abort),
        Step("TMRcvPrepared", take_tm_rcv_prepared, rms),
        Step("RMPrepare", take_rm_prepare, rms),
        Step("RMChooseToAbort", take_rm_choose_to_abort, rms),
        Step("RMRcvCommitMsg", take_rm_rcv_commit_msg, rms),
        Step("RMRcvAbortMsg", take_rm_rcv_abort_msg, rms),
    ]


def abstract(node: Node) -> Dict[str, Any]:
    """A node as the spec's variables."""
    rm, tm_done, tm_prepared, msgs = node
    out: List[Dict[str, Any]] = [{"type": "Prepared", "rm": {"$mv": NAMES[r]}} for r in range(RM) if ("Prepared", r) in msgs]
    if COMMIT in msgs:
        out.append({"type": "Commit"})
    if ABORT in msgs:
        out.append({"type": "Abort"})
    return {
        "rmState": {"$fn": [[{"$mv": NAMES[r]}, STATES[rm[r]]] for r in range(RM)]},
        "tmState": "done" if tm_done else "init",
        "tmPrepared": {"$set": [{"$mv": NAMES[r]} for r in range(RM) if tm_prepared[r]]},
        "msgs": {"$set": out},
    }
