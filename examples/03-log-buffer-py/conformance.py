"""Explore the core with Invariant's harness, trying every step of Next everywhere."""
from invariant_explore import explore

from logbuffer.explore import INITIAL, STEPS, abstract


def main():
    explore(INITIAL, STEPS, abstract)


if __name__ == "__main__":
    main()
