import unittest

from twophase import TwoPhaseCommit


class TwoPhaseCommitTest(unittest.TestCase):
    def test_everyone_starts_working_without_votes(self) -> None:
        t = TwoPhaseCommit(3)
        self.assertEqual(t.states, [0, 0, 0])
        self.assertEqual(t.votes, [False, False, False])
        self.assertFalse(t.decided)

    def test_commit_waits_for_every_vote(self) -> None:
        t = TwoPhaseCommit(2)
        self.assertTrue(t.prepare(0))
        self.assertTrue(t.record_vote(0))
        self.assertFalse(t.commit())
        self.assertTrue(t.prepare(1))
        self.assertTrue(t.record_vote(1))
        self.assertTrue(t.commit())
        self.assertTrue(t.decided)
        self.assertFalse(t.abort())

    def test_only_a_working_participant_prepares_or_gives_up(self) -> None:
        t = TwoPhaseCommit(2)
        self.assertTrue(t.give_up(0))
        self.assertFalse(t.prepare(0))
        self.assertTrue(t.prepare(1))
        self.assertFalse(t.give_up(1))
        self.assertEqual(t.states, [3, 1])

    def test_no_vote_is_recorded_after_the_decision(self) -> None:
        t = TwoPhaseCommit(2)
        t.prepare(0)
        self.assertTrue(t.abort())
        self.assertFalse(t.record_vote(0))
        self.assertEqual(t.votes, [False, False])

    def test_participants_follow_the_decision(self) -> None:
        t = TwoPhaseCommit(2)
        t.abort()
        t.learn_abort(0)
        t.learn_abort(1)
        self.assertEqual(t.states, [3, 3])


if __name__ == "__main__":
    unittest.main()
