# Factory issue protocol

Written by Invariant for [#9](https://github.com/gitdek/invariant/issues/9): Prove the factory's issue protocol.

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/9#issuecomment-5852434500). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/IssueProtocol.tla`](.invariant/specs/IssueProtocol.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | The factory starts with no post on the issue, and every step is a Next step. |
| `TypeOK` | invariant | The issue's state is always one of the ten kinds of post, and everything else the factory knows has its expected type. |
| `OnlyDirectorsDirect` | invariant | Every command the factory acted on came from a person with write access, never from anyone else or from a bot. |
| `RatifiesCurrentProposal` | invariant | Whenever something is ratified, built or merged, it is the current proposal. |
| `NoOpenQuestionsWhenRatified` | invariant | Nothing is ratified while a question is still open. |
| `AmendsHeldLock` | invariant | A proposal is ratified only while the base branch holds the lock it amends. |
| `MergedGatePassed` | invariant | When the factory merges, CI's gate passed on the commit it merged. |
| `MergedCurrentHead` | invariant | When the factory merges, the commit it merged is the pull request's current head. |
| `MergedOneProject` | invariant | When the factory merges, the pull request changes only its one project. |
| `MergedLockRatified` | invariant | When the factory merges, the project's lock is exactly the ratified proposal. |
| `QuestionsAsked` | witness | The factory can ask questions. |
| `GotStuck` | witness | The factory can get stuck. |
| `FoundUnsupported` | witness | The factory can find an issue unsupported. |
| `PullRequestFailed` | witness | A pull request can fail CI's gate. |
| `OthersClosed` | witness | Someone else can close the pull request. |
| `OthersMerged` | witness | Someone else can merge the pull request. |
| `FactoryMerges` | witness | The factory can merge its pull request. |
| `AmendmentMerges` | witness | The factory can merge an amendment to an existing lock. |
| `ObeyAnyone` | bug | A bot or someone without write access gets the factory to take the issue. |
| `RatifyEarlier` | bug | A ratify naming an earlier proposal's hash is accepted. |
| `RatifyOverMovedLock` | bug | An amendment is ratified after the base branch's lock has moved. |
| `MergeOnAnyPass` | bug | The factory merges a commit that passed CI even though it is no longer the pull request's head. |
| `MergeIgnoringLock` | bug | The factory merges without checking that the project's lock is the ratified proposal. |

Checked within `Actors = {alice, mallory, invbot}`, `Bots = {invbot}`, `Heads = {h1, h2}`, `NoActor = NoActor`, `NoHead = NoHead`, `NoP = NoP`, `Proposals = {p1, p2}`, `Questions = {f1, f2}`, `Writers = {alice, invbot}`. To run the gate yourself:

```bash
go run ./cmd/invariant verify factory/protocol
```
