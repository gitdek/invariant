"""Invariant's exploration harness for a Python conformance driver (D-0082, D-0085).

The gate puts this module on the driver's path before the driver runs, so the
copy that runs is always Invariant's own.

A driver gives it the nodes to start from, every step the model's Next names,
and how to read a node as a state in the spec's vocabulary. The harness
explores breadth first, tries every step with every argument in every node it
reaches, and records each attempt, refusals included. The gate then checks
that no step went untried except where only the environment's bounds rule it
out.
"""

import json
import os


class Step:
    """A step the model's Next names, such as MakeCall(a).

    name is the step's name, exactly as Next names it. args is every argument
    list to try it with, as Next passes them, in the spec's encoding:
    [[{"$mv": "a1"}], [{"$mv": "a2"}]]; leave it out for a step with no
    arguments. take(node, *args) tries the step from node and returns the node
    it reaches: a new node when the code takes the step, a node equal to the
    one it was given when the code refuses, and None only when the
    environment's bounds rule the step out, such as a sixth call when five is
    the bound. It must not change the node it's given.
    """

    def __init__(self, name, take, args=None):
        self.name = name
        self.take = take
        self.args = args if args is not None else [[]]


def explore(initial, steps, abstract, key=None):
    """Explore from initial, trying every step everywhere, and write what it finds.

    abstract(node) is the node's state in the spec's vocabulary, one entry per
    variable. key(node) says what makes two nodes the same; by default, their
    states. The result goes to $INVARIANT_TRACES. When $INVARIANT_COUNT is
    set, it explores the same way and writes only how many states it reached,
    and how many breadth-first levels its search took, to that file.
    """
    counting = os.environ.get("INVARIANT_COUNT")
    out = counting or os.environ.get("INVARIANT_TRACES")
    if not out:
        raise RuntimeError("neither INVARIANT_TRACES nor INVARIANT_COUNT is set")
    if key is None:
        key = lambda node: json.dumps(abstract(node), sort_keys=True)
    nodes, levels, states, index, attempts = [], [], [], {}, []
    depth = 0

    def add(node, level):
        nonlocal depth
        k = key(node)
        if k in index:
            return index[k]
        i = len(nodes)
        index[k] = i
        nodes.append(node)
        levels.append(level)
        if not counting:
            states.append(abstract(node))
        depth = max(depth, level)
        return i

    init = [add(node, 1) for node in initial]
    i = 0
    while i < len(nodes):
        for step in steps:
            for args in step.args:
                nxt = step.take(nodes[i], *args)
                if nxt is None:
                    continue
                j = add(nxt, levels[i] + 1)
                if not counting:
                    attempts.append([i, step.name, list(args), j])
        i += 1
    with open(out, "w") as f:
        if counting:
            json.dump({"states": len(nodes), "depth": depth}, f)
        else:
            json.dump({"states": states, "init": init, "attempts": attempts}, f)
    if counting:
        print(f"{len(nodes)} states, depth {depth}")
    else:
        print(f"{len(nodes)} states, {len(attempts)} attempts")
