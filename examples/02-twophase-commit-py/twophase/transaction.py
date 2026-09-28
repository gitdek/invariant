"""Two-phase commit among a fixed set of participants.

The participants' and the coordinator's state lives in the verified core,
twophase.core; this module names the participants and carries the messages
they exchange. Messages are never lost.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal, Union

from twophase.core import TwoPhaseCommit

ParticipantState = Literal["working", "prepared", "committed", "aborted"]

# The core's number for each participant state, in order.
STATES: tuple[ParticipantState, ...] = ("working", "prepared", "committed", "aborted")


@dataclass(frozen=True)
class Prepared:
    """A participant's vote to commit."""

    sender: str


@dataclass(frozen=True)
class Commit:
    """The coordinator's decision to commit."""


@dataclass(frozen=True)
class Abort:
    """The coordinator's decision to abort."""


Message = Union[Prepared, Commit, Abort]

# A transaction's whole state: each participant's state number, the votes the
# coordinator has recorded, whether it has decided, and the messages sent.
Snapshot = tuple[tuple[int, ...], tuple[bool, ...], bool, tuple[Message, ...]]


class Transaction:
    """A two-phase commit. Messages are never lost."""

    def __init__(self, participants: list[str]) -> None:
        if not participants:
            raise ValueError("a transaction needs participants")
        self.participants: tuple[str, ...] = tuple(participants)
        self._index: dict[str, int] = {p: i for i, p in enumerate(participants)}
        self._core = TwoPhaseCommit(len(participants))
        self._messages: list[Message] = []

    def _number(self, participant: str) -> int:
        if participant not in self._index:
            raise KeyError(f"unknown participant {participant}")
        return self._index[participant]

    def state_of(self, participant: str) -> ParticipantState:
        return STATES[self._core.states[self._number(participant)]]

    @property
    def votes(self) -> frozenset[str]:
        """The votes the coordinator has recorded."""
        return frozenset(p for p in self.participants if self._core.votes[self._index[p]])

    @property
    def messages(self) -> tuple[Message, ...]:
        """Every message sent so far."""
        return tuple(self._messages)

    @property
    def decided(self) -> bool:
        """Whether the coordinator has decided."""
        return self._core.decided

    def prepare(self, participant: str) -> bool:
        """A working participant prepares and votes to commit."""
        if not self._core.prepare(self._number(participant)):
            return False
        self._send(Prepared(participant))
        return True

    def give_up(self, participant: str) -> bool:
        """A participant that hasn't prepared gives up on its own."""
        return self._core.give_up(self._number(participant))

    def record_vote(self, participant: str) -> bool:
        """The coordinator records a vote it has received."""
        r = self._number(participant)
        if Prepared(participant) not in self._messages:
            return False
        return self._core.record_vote(r)

    def commit(self) -> bool:
        """The coordinator commits, once every participant has voted."""
        if not self._core.commit():
            return False
        self._send(Commit())
        return True

    def abort(self) -> bool:
        """The coordinator aborts. It can do that any time before it has decided."""
        if not self._core.abort():
            return False
        self._send(Abort())
        return True

    def learn(self, participant: str) -> bool:
        """A participant learns the coordinator's decision and follows it."""
        r = self._number(participant)
        if Commit() in self._messages:
            self._core.learn_commit(r)
            return True
        if Abort() in self._messages:
            self._core.learn_abort(r)
            return True
        return False

    def snapshot(self) -> Snapshot:
        """Everything the transaction holds, to make it again with restore."""
        core = self._core
        return (tuple(core.states), tuple(core.votes), core.decided, tuple(self._messages))

    @classmethod
    def restore(cls, participants: list[str], snapshot: Snapshot) -> "Transaction":
        """The transaction a snapshot was taken of."""
        t = cls(participants)
        states, votes, decided, messages = snapshot
        t._core.states = list(states)
        t._core.votes = list(votes)
        t._core.decided = decided
        t._messages = list(messages)
        return t

    def _send(self, message: Message) -> None:
        if message not in self._messages:
            self._messages.append(message)
