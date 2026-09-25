"""Two-phase commit among a fixed set of participants.

This is ordinary code, with nothing in it written for a verifier. Invariant
checks it against the ratified TLA+ model by conformance testing.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal, Union

ParticipantState = Literal["working", "prepared", "committed", "aborted"]


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


class Transaction:
    """A two-phase commit. Messages are never lost."""

    def __init__(self, participants: list[str]) -> None:
        if not participants:
            raise ValueError("a transaction needs participants")
        self.participants: tuple[str, ...] = tuple(participants)
        self._states: dict[str, ParticipantState] = {p: "working" for p in participants}
        self._votes: set[str] = set()
        self._messages: list[Message] = []
        self._decided = False

    def state_of(self, participant: str) -> ParticipantState:
        if participant not in self._states:
            raise KeyError(f"unknown participant {participant}")
        return self._states[participant]

    @property
    def votes(self) -> frozenset[str]:
        """The votes the coordinator has recorded."""
        return frozenset(self._votes)

    @property
    def messages(self) -> tuple[Message, ...]:
        """Every message sent so far."""
        return tuple(self._messages)

    @property
    def decided(self) -> bool:
        """Whether the coordinator has decided."""
        return self._decided

    def prepare(self, participant: str) -> bool:
        """A working participant prepares and votes to commit."""
        if self.state_of(participant) != "working":
            return False
        self._states[participant] = "prepared"
        self._send(Prepared(participant))
        return True

    def give_up(self, participant: str) -> bool:
        """A participant that hasn't prepared gives up on its own."""
        if self.state_of(participant) != "working":
            return False
        self._states[participant] = "aborted"
        return True

    def record_vote(self, participant: str) -> bool:
        """The coordinator records a vote it has received."""
        if self._decided or Prepared(participant) not in self._messages:
            return False
        self._votes.add(participant)
        return True

    def commit(self) -> bool:
        """The coordinator commits, once every participant has voted."""
        if self._decided or len(self._votes) < len(self.participants):
            return False
        self._decided = True
        self._send(Commit())
        return True

    def abort(self) -> bool:
        """The coordinator aborts. It can do that any time before it has decided."""
        if self._decided:
            return False
        self._decided = True
        self._send(Abort())
        return True

    def learn(self, participant: str) -> bool:
        """A participant learns the coordinator's decision and follows it."""
        self.state_of(participant)
        if Commit() in self._messages:
            self._states[participant] = "committed"
            return True
        if Abort() in self._messages:
            self._states[participant] = "aborted"
            return True
        return False

    def _send(self, message: Message) -> None:
        if message not in self._messages:
            self._messages.append(message)
