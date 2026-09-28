"""Invariant's conformance driver for this project.

It explores every state the code can reach with Invariant's harness, trying
every step the model's Next names, for every resource manager, in every state,
and records each attempt, refusals included. Invariant hands what it finds to
TLC, which checks that it starts where Init allows and moves only by Next
steps, and that it reaches exactly the states the model does.
"""

from invariant_explore import explore

from twophase.explore import abstract, initial, steps


def main() -> None:
    explore(initial, steps, abstract)


if __name__ == "__main__":
    main()
