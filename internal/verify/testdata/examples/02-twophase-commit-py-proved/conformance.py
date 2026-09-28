"""Invariant's conformance driver for the proved core.

It explores with Invariant's harness: breadth first, every step Next names,
with every resource manager, in every state the code reaches. The core
refuses what the model rules out, and the harness records every attempt,
refusals included, so the gate can check that no step went untried.
"""

from invariant_explore import explore

from twophase.explore import abstract, initial, steps


def main() -> None:
    explore(initial(), steps(), abstract)


if __name__ == "__main__":
    main()
