import unittest

from logbuffer.core import LogBuffer
from logbuffer.explore import Capacity, INITIAL, STEPS, abstract


def reachable():
    seen = {}
    stack = list(INITIAL)
    while stack:
        node = stack.pop()
        if node in seen:
            continue
        succ = []
        for step in STEPS:
            for args in step.args:
                nxt = step.take(node, *args)
                if nxt is not None:
                    succ.append(nxt)
        seen[node] = succ
        stack.extend(succ)
    return seen


class LogBufferTest(unittest.TestCase):
    def test_init(self):
        b = LogBuffer(Capacity)
        self.assertEqual(b.lines, [])
        self.assertFalse(b.retrying)
        state = abstract(INITIAL[0])
        self.assertEqual(state["buf"], {"$seq": []})
        self.assertFalse(state["retrying"])

    def test_ships_in_order_once(self):
        b = LogBuffer(2)
        self.assertTrue(b.write(1))
        self.assertTrue(b.write(3))
        self.assertTrue(b.ship())
        self.assertEqual(b.lines, [3])
        self.assertTrue(b.ship())
        self.assertEqual(b.lines, [])
        self.assertFalse(b.ship())

    def test_failed_send_is_retried(self):
        b = LogBuffer(2)
        b.write(1)
        self.assertTrue(b.ship_fail())
        self.assertTrue(b.retrying)
        self.assertEqual(b.lines, [1])
        self.assertFalse(b.ship_fail())
        self.assertTrue(b.ship())
        self.assertEqual(b.lines, [])
        self.assertFalse(b.retrying)

    def test_full_buffer_blocks_writers(self):
        b = LogBuffer(2)
        b.write(1)
        b.write(3)
        self.assertFalse(b.write(2))
        self.assertEqual(b.lines, [1, 3])

    def test_invariants_hold_everywhere(self):
        graph = reachable()
        self.assertTrue(graph)
        for node, succ in graph.items():
            log = list(node.log)
            sent = list(node.sent)
            self.assertLessEqual(len(node.buf), Capacity)
            self.assertEqual(sent, log[: len(sent)])
            self.assertEqual(list(node.buf), log[len(sent):])
            self.assertEqual(len(set(sent)), len(sent))
            self.assertTrue(succ)


if __name__ == "__main__":
    unittest.main()
