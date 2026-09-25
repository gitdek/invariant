import unittest
from typing import Callable, List, Set, Tuple

from twophase.core import ABORTED, COMMITTED, State
from twophase.explore import Key, copy, key, successors


def explore(step: Callable[[State], List[State]]) -> Tuple[Set[Key], int]:
    """Every state reachable from State(), breadth first, and the levels searched."""
    start = State()
    seen = {key(start)}
    frontier = [start]
    depth = 0
    while frontier:
        depth += 1
        nxt = []
        for s in frontier:
            for t in step(s):
                if key(t) not in seen:
                    seen.add(key(t))
                    nxt.append(t)
        frontier = nxt
    return seen, depth


def consistent(k: Key) -> bool:
    rm = k[0]
    return not (ABORTED in rm and COMMITTED in rm)


class CoreTest(unittest.TestCase):
    def test_state_space_matches_the_model(self) -> None:
        seen, depth = explore(successors)
        self.assertEqual((len(seen), depth), (288, 11), "TLC reports 288 states, depth 11")

    def test_every_reachable_state_is_consistent(self) -> None:
        seen, _ = explore(successors)
        self.assertTrue(all(consistent(k) for k in seen))

    def test_early_commit_is_caught(self) -> None:
        def early(s: State) -> List[State]:
            out = successors(s)
            if not s.tm_done and any(s.tm_prepared) and not all(s.tm_prepared):
                t = copy(s)
                t.tm_done, t.commit_msg = True, True
                out.append(t)
            return out

        seen, _ = explore(early)
        self.assertFalse(all(consistent(k) for k in seen))


if __name__ == "__main__":
    unittest.main()
