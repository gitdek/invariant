"""The environment of the two-phase commit, for Invariant's explorer.

This is the only place the model's bounds live. A node is a transaction's
whole state, read back from Transaction: each participant's state, the votes
the coordinator has recorded, whether it has decided, and every message sent.
Every step makes the transaction again from the node, calls it, and lets it
refuse, so the messages it sends and the ones it waits for are the code's own.
"""

from __future__ import annotations

from typing import Any, Optional

from invariant_explore import Step

from twophase.transaction import Commit, Message, Prepared, Snapshot, Transaction

# The resource managers, r1 to rRM.
RM = 3

PARTICIPANTS = [f"r{r + 1}" for r in range(RM)]

# The spec's name for each state the core numbers.
STATE_NAMES = ("working", "prepared", "committed", "aborted")

Node = Snapshot

initial = [Transaction(PARTICIPANTS).snapshot()]


def mv(name: str) -> dict:
    return {"$mv": name}


def message(m: Message) -> dict:
    if isinstance(m, Prepared):
        return {"type": "Prepared", "rm": mv(m.sender)}
    return {"type": "Commit" if isinstance(m, Commit) else "Abort"}


def abstract(node: Node) -> dict:
    """The node's state as the spec's variables."""
    states, votes, decided, messages = node
    return {
        "rmState": {"$fn": [[mv(p), STATE_NAMES[s]] for p, s in zip(PARTICIPANTS, states)]},
        "tmState": "done" if decided else "init",
        "tmPrepared": {"$set": [mv(p) for p, v in zip(PARTICIPANTS, votes) if v]},
        # A set: in a fixed order, so the same messages sent in another order
        # are the same state.
        "msgs": {"$set": sorted((message(m) for m in messages), key=repr)},
    }


def step(operation: str):
    """A step that calls one of the transaction's operations on a participant,
    or on the coordinator, and reads the transaction back, refusal or not."""

    def take(node: Node, rm: Optional[Any] = None) -> Node:
        t = Transaction.restore(PARTICIPANTS, node)
        call = getattr(t, operation)
        if rm is None:
            call()
        else:
            call(rm["$mv"])
        return t.snapshot()

    return take


rms = [[mv(p)] for p in PARTICIPANTS]

# Both of a participant's receive steps are its learn: it follows whichever
# decision the coordinator actually sent, and refuses when there's none.
steps = [
    Step("TMCommit", step("commit")),
    Step("TMAbort", step("abort")),
    Step("TMRcvPrepared", step("record_vote"), rms),
    Step("RMPrepare", step("prepare"), rms),
    Step("RMChooseToAbort", step("give_up"), rms),
    Step("RMRcvCommitMsg", step("learn"), rms),
    Step("RMRcvAbortMsg", step("learn"), rms),
]
