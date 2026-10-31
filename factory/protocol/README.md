# Factory issue protocol

Written by Invariant for [#9](https://github.com/gitdek/invariant/issues/9): Prove the factory's issue protocol. The factory amended it through [#13](https://github.com/gitdek/invariant/issues/13) and [#69](https://github.com/gitdek/invariant/issues/69), and a coding agent amended it by hand for [#271](https://github.com/gitdek/invariant/issues/271), so a writer's retry can build a failed build again.

@gitdek ratified these statements by hand, as [D-0202](../../decisions/journal/D-0202.jsonl), in place of a comment on the issue: they amend the statements he ratified [on #69](https://github.com/gitdek/invariant/issues/69#issuecomment-5875565342). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/IssueProtocol.tla`](.invariant/specs/IssueProtocol.tla).

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
| `StopLimit` | invariant | At most two builds of an issue stop partway before a writer says retry. |
| `StoppedHasNoPullRequest` | invariant | When a build stops, or the factory refuses to start one, there is no pull request. |
| `BuildStartsClean` | invariant | When a ratified proposal waits for its build, nothing is left of an earlier pull request: no head, CI result, lock, scope or merge. |
| `RebuildsOwnFailures` | invariant | A retry retires a pull request to build its ratified proposal again only after the build failed its own gate in the factory's run, never after CI's gate failed or the pull request couldn't merge. |
| `QuestionsAsked` | witness | The factory can ask questions. |
| `GotStuck` | witness | The factory can get stuck. |
| `FoundUnsupported` | witness | The factory can find an issue unsupported. |
| `PullRequestFailed` | witness | A pull request can fail CI's gate. |
| `OthersClosed` | witness | Someone else can close the pull request. |
| `OthersMerged` | witness | Someone else can merge the pull request. |
| `FactoryMerges` | witness | The factory can merge its pull request. |
| `AmendmentMerges` | witness | The factory can merge an amendment to an existing lock. |
| `BuildStopped` | witness | A build can stop before it makes a pull request, and the factory posts that it failed. |
| `BuildRefusedAtLimit` | witness | After two builds of an issue stop partway, the factory can refuse to start a third and post that the build failed. |
| `OwnGateFailed` | witness | When the code fails the gate in the factory's own run, the factory can open a draft pull request and post that the build failed. |
| `CannotMerge` | witness | A pull request that passed CI's gate but changes more than its one project or has a lock other than the ratified proposal can fail. |
| `RebuiltAndMerged` | witness | A writer's retry can build a build that failed its own gate again, and the factory can merge the new build's pull request. |
| `RebuiltAndStopped` | witness | After a retry builds a failed build again, the new build can stop before it makes a pull request, and the factory posts that it failed. |
| `RebuiltFailedAgain` | witness | The new build can fail its own gate again and leave another draft pull request, for another retry. |
| `ObeyAnyone` | bug | A bot or someone without write access gets the factory to take the issue. |
| `RatifyEarlier` | bug | A ratify naming an earlier proposal's hash is accepted. |
| `RatifyOverMovedLock` | bug | An amendment is ratified after the base branch's lock has moved. |
| `MergeOnAnyPass` | bug | The factory merges a commit that passed CI even though it is no longer the pull request's head. |
| `MergeIgnoringLock` | bug | The factory merges without checking that the project's lock is the ratified proposal. |
| `StartPastLimit` | bug | The factory starts a third build after two builds of the issue have stopped partway and no writer has said retry. |
| `MergeFailedBuild` | bug | The factory merges a pull request whose build failed, before a writer says retry and CI's gate passes. |
| `RebuildKeepsDraft` | bug | A retry builds a failed build again but keeps its draft's head, CI's result and lock for the next build. |
| `RebuildAfterCI` | bug | A retry builds a pull request that failed CI's gate again, instead of watching it again. |

Checked within `Actors = {alice, mallory, invbot}`, `Bots = {invbot}`, `Heads = {h1, h2}`, `NoActor = NoActor`, `NoHead = NoHead`, `NoP = NoP`, `Proposals = {p1, p2}`, `Questions = {f1, f2}`, `Writers = {alice, invbot}`. To run the gate yourself:

```bash
go run ./cmd/invariant verify factory/protocol
```
