import unittest

from twophase.core import (
    ABORTED,
    COMMITTED,
    PREPARED,
    WORKING,
    State,
    rm_choose_to_abort,
    rm_prepare,
    rm_rcv_commit_msg,
    tm_abort,
    tm_commit,
    tm_rcv_prepared,
)


class CoreTest(unittest.TestCase):
    def test_init_sizes_to_its_parameter(self) -> None:
        s = State(5)
        self.assertEqual(s.rm, [WORKING] * 5)
        self.assertEqual(s.tm_prepared, [False] * 5)
        self.assertFalse(s.tm_done)

    def test_prepare_runs_once(self) -> None:
        s = State(2)
        self.assertTrue(rm_prepare(s, 0))
        self.assertEqual(s.rm, [PREPARED, WORKING])
        self.assertFalse(rm_prepare(s, 0))
        self.assertFalse(rm_choose_to_abort(s, 0))
        self.assertEqual(s.rm, [PREPARED, WORKING])

    def test_commit_waits_for_every_prepared(self) -> None:
        s = State(3)
        self.assertTrue(tm_rcv_prepared(s, 0))
        self.assertTrue(tm_rcv_prepared(s, 1))
        self.assertFalse(tm_commit(s))
        self.assertFalse(s.tm_done)
        self.assertTrue(tm_rcv_prepared(s, 2))
        self.assertTrue(tm_commit(s))
        self.assertTrue(s.tm_done)
        self.assertFalse(tm_abort(s))
        self.assertFalse(tm_rcv_prepared(s, 0))
        rm_rcv_commit_msg(s, 1)
        self.assertEqual(s.rm, [WORKING, COMMITTED, WORKING])

    def test_abort_before_deciding(self) -> None:
        s = State(1)
        self.assertTrue(tm_abort(s))
        self.assertFalse(tm_commit(s))
        self.assertTrue(rm_choose_to_abort(s, 0))
        self.assertEqual(s.rm, [ABORTED])


if __name__ == "__main__":
    unittest.main()
