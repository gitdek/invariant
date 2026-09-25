"""Explore the core breadth first and record one run per step for TLC to check."""
import json
import os
from collections import deque

from logbuffer.core import MAXLINES, State
from logbuffer.explore import key, successors

PRODUCERS = ["p1", "p2"]


def line(code):
    p = (code - 1) // MAXLINES
    n = code - p * MAXLINES
    return {"$seq": [{"$mv": PRODUCERS[p]}, n]}


def seq(items, length):
    return {"$seq": [line(c) for c in items[:length]]}


def abstract(s):
    return {
        "buf": seq(s.buf, s.buf_len),
        "sent": seq(s.sent, s.sent_len),
        "log": seq(s.log, s.log_len),
        "written": {"$fn": [[{"$mv": p}, s.written[i]] for i, p in enumerate(PRODUCERS)]},
        "retrying": s.retrying,
    }


def explore():
    init = State()
    paths = {key(init): [abstract(init)]}
    queue = deque([init])
    traces = []
    while queue:
        s = queue.popleft()
        path = paths[key(s)]
        for t in successors(s):
            traces.append(path + [abstract(t)])
            k = key(t)
            if k not in paths:
                paths[k] = path + [abstract(t)]
                queue.append(t)
    return traces, len(paths)


def main():
    traces, _ = explore()
    with open(os.environ["INVARIANT_TRACES"], "w") as f:
        json.dump({"traces": traces}, f)


if __name__ == "__main__":
    main()
