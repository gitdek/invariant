import unittest

from conformance import explore
from logbuffer.core import CAP, State, ship, ship_fail, write
from logbuffer.explore import key, successors


class LogBufferTest(unittest.TestCase):
    def test_init(self):
        s = State()
        self.assertEqual(key(s), ((), (), (), (0, 0), False))

    def test_ships_in_order_once(self):
        s = State()
        write(s, 0)
        write(s, 1)
        ship(s)
        ship(s)
        self.assertEqual(s.sent[: s.sent_len], s.log[: s.log_len])
        self.assertEqual(s.buf_len, 0)

    def test_failed_send_is_retried(self):
        s = State()
        write(s, 0)
        ship_fail(s)
        self.assertTrue(s.retrying)
        self.assertEqual(s.buf[: s.buf_len], [1])
        ship(s)
        self.assertEqual(s.sent[: s.sent_len], [1])
        self.assertFalse(s.retrying)

    def test_full_buffer_blocks_writers(self):
        s = State()
        write(s, 0)
        write(s, 1)
        self.assertEqual(s.buf_len, CAP)
        self.assertFalse(any(t.log_len > s.log_len for t in successors(s)))

    def test_invariants_hold_everywhere(self):
        traces, _ = explore()
        self.assertTrue(traces)
        seen = set()
        stack = [State()]
        while stack:
            s = stack.pop()
            if key(s) in seen:
                continue
            seen.add(key(s))
            log = s.log[: s.log_len]
            self.assertLessEqual(s.buf_len, CAP)
            self.assertEqual(s.sent[: s.sent_len], log[: s.sent_len])
            self.assertEqual(s.buf[: s.buf_len], log[s.sent_len:])
            self.assertEqual(len(set(s.sent[: s.sent_len])), s.sent_len)
            succ = successors(s)
            self.assertTrue(succ)
            stack.extend(succ)


if __name__ == "__main__":
    unittest.main()
