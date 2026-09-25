import unittest

from twophase import Transaction


class TransactionTest(unittest.TestCase):
    def test_everyone_commits_once_everyone_has_voted(self) -> None:
        t = Transaction(["a", "b"])
        for p in t.participants:
            self.assertTrue(t.prepare(p))
            self.assertTrue(t.record_vote(p))
        self.assertTrue(t.commit())
        for p in t.participants:
            self.assertTrue(t.learn(p))
            self.assertEqual(t.state_of(p), "committed")

    def test_no_commit_before_every_vote_is_in(self) -> None:
        t = Transaction(["a", "b"])
        t.prepare("a")
        t.record_vote("a")
        self.assertFalse(t.commit())
        self.assertFalse(t.decided)

    def test_a_participant_that_gave_up_learns_the_abort(self) -> None:
        t = Transaction(["a", "b"])
        self.assertTrue(t.give_up("a"))
        self.assertFalse(t.prepare("a"))
        self.assertTrue(t.abort())
        self.assertTrue(t.learn("b"))
        self.assertEqual(t.state_of("b"), "aborted")


if __name__ == "__main__":
    unittest.main()
