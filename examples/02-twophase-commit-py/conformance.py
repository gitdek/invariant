"""Invariant's conformance driver for this project.

It calls the library's operations at random and records every state it
passes through, in the vocabulary of the ratified spec. Invariant hands the
runs to TLC, which checks that each one starts where Init allows and moves
only by Next steps. Refused operations are fine: a step that changes nothing
is always allowed.
"""

from __future__ import annotations

import json
import os
import random
from typing import Any, Callable

from twophase import Commit, Message, Prepared, Transaction


# Values in the encoding Invariant reads: model values, sets and functions say
# what they are; plain dicts are records.
def mv(name: str) -> dict[str, Any]:
    return {"$mv": name}


def tla_set(items: list[Any]) -> dict[str, Any]:
    return {"$set": items}


def fn(pairs: list[tuple[Any, Any]]) -> dict[str, Any]:
    return {"$fn": [[k, v] for k, v in pairs]}


def message(m: Message) -> dict[str, Any]:
    if isinstance(m, Prepared):
        return {"type": "Prepared", "rm": mv(m.sender)}
    return {"type": "Commit" if isinstance(m, Commit) else "Abort"}


def abstract(t: Transaction) -> dict[str, Any]:
    """The abstraction: the transaction's state as the spec's variables."""
    return {
        "rmState": fn([(mv(p), t.state_of(p)) for p in t.participants]),
        "tmState": "done" if t.decided else "init",
        "tmPrepared": tla_set([mv(p) for p in sorted(t.votes)]),
        "msgs": tla_set([message(m) for m in t.messages]),
    }


def main() -> None:
    runs = int(os.environ.get("INVARIANT_RUNS", "300"))
    steps = int(os.environ.get("INVARIANT_STEPS", "40"))
    rng = random.Random(int(os.environ.get("INVARIANT_SEED", "1")))
    participants = ["r1", "r2", "r3"]

    def anyone() -> str:
        return rng.choice(participants)

    # The coordinator's abort ends a run's interesting life, so it's picked
    # less often than the rest.
    operations: list[Callable[[Transaction], object]] = [
        lambda t: t.prepare(anyone()),
        lambda t: t.prepare(anyone()),
        lambda t: t.give_up(anyone()),
        lambda t: t.record_vote(anyone()),
        lambda t: t.record_vote(anyone()),
        lambda t: t.commit(),
        lambda t: t.learn(anyone()),
        lambda t: t.learn(anyone()),
        lambda t: t.abort() if rng.random() < 0.6 else False,
    ]
    traces = []
    for _ in range(runs):
        t = Transaction(participants)
        trace = [abstract(t)]
        for _ in range(steps):
            rng.choice(operations)(t)
            trace.append(abstract(t))
        traces.append(trace)
    with open(os.environ.get("INVARIANT_TRACES", "traces.json"), "w") as out:
        json.dump({"traces": traces}, out)


if __name__ == "__main__":
    main()
