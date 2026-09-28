import unittest
from typing import Dict, List, Tuple

from invariant_explore import Step

from twophase.core import ABORTED, COMMITTED
from twophase.explore import COMMIT, Node, initial, steps


def reach(all_steps: List[Step]) -> Tuple[Dict[Node, int], int]:
    """Every node reachable from the initial ones, breadth first, with its level, and the levels searched."""
    level = {node: 1 for node in initial()}
    frontier = list(level)
    depth = 1
    while frontier:
        nxt = []
        for node in frontier:
            for step in all_steps:
                for args in step.args:
                    t = step.take(node, *args)
                    if t is not None and t not in level:
                        level[t] = level[node] + 1
                        depth = max(depth, level[t])
                        nxt.append(t)
        frontier = nxt
    return level, depth


def consistent(node: Node) -> bool:
    rm = node[0]
    return not (ABORTED in rm and COMMITTED in rm)


def take_early_commit(node: Node) -> Node:
    rm, tm_done, tm_prepared, msgs = node
    if tm_done or not any(tm_prepared):
        return node
    return (rm, True, tm_prepared, msgs | {COMMIT})


class TwoPhaseTest(unittest.TestCase):
    def test_state_space_matches_the_model(self) -> None:
        seen, depth = reach(steps())
        self.assertEqual((len(seen), depth), (288, 11), "TLC reports 288 states, depth 11")

    def test_every_reachable_state_is_consistent(self) -> None:
        seen, _ = reach(steps())
        self.assertTrue(all(consistent(n) for n in seen))

    def test_both_outcomes_are_reachable(self) -> None:
        seen, _ = reach(steps())
        self.assertTrue(any(all(r == COMMITTED for r in n[0]) for n in seen))
        self.assertTrue(any(all(r == ABORTED for r in n[0]) for n in seen))

    def test_early_commit_is_caught(self) -> None:
        seen, _ = reach(steps() + [Step("EarlyCommit", take_early_commit)])
        self.assertFalse(all(consistent(n) for n in seen))


if __name__ == "__main__":
    unittest.main()
