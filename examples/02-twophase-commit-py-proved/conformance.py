"""Invariant's conformance driver for the proved core.

The core is a small state machine, so rather than sample it at random, this
explores it completely, breadth first. It records one run for every step the
code can take: the path from the initial state to the step's start, then the
step. TLC checks every one, so conformance covers the whole state space.
"""

import json
import os
from typing import Any, Dict, List

from twophase.core import ABORTED, COMMITTED, N, PREPARED, WORKING, State
from twophase.explore import key, successors

NAMES = {WORKING: "working", PREPARED: "prepared", COMMITTED: "committed", ABORTED: "aborted"}
RM = ["r1", "r2", "r3"]


def abstract(s: State) -> Dict[str, Any]:
    """The abstraction: a state as the spec's variables."""
    msgs: List[Dict[str, Any]] = [{"type": "Prepared", "rm": {"$mv": RM[r]}} for r in range(N) if s.prepared_msg[r]]
    if s.commit_msg:
        msgs.append({"type": "Commit"})
    if s.abort_msg:
        msgs.append({"type": "Abort"})
    return {
        "rmState": {"$fn": [[{"$mv": RM[r]}, NAMES[s.rm[r]]] for r in range(N)]},
        "tmState": "done" if s.tm_done else "init",
        "tmPrepared": {"$set": [{"$mv": RM[r]} for r in range(N) if s.tm_prepared[r]]},
        "msgs": {"$set": msgs},
    }


def main() -> None:
    start = State()
    paths = {key(start): [start]}
    frontier = [start]
    runs = []
    while frontier:
        nxt = []
        for s in frontier:
            for t in successors(s):
                runs.append([abstract(u) for u in paths[key(s)]] + [abstract(t)])
                if key(t) not in paths:
                    paths[key(t)] = paths[key(s)] + [t]
                    nxt.append(t)
        frontier = nxt
    with open(os.environ.get("INVARIANT_TRACES", "traces.json"), "w") as out:
        json.dump({"traces": runs}, out)


if __name__ == "__main__":
    main()
