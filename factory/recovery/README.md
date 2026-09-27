# Watcher crash recovery

Written by Invariant for [#23](https://github.com/gitdek/invariant/issues/23): Prove the factory recovers from a crash at any point, and from a second watcher.

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/23#issuecomment-5856010917). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/WatcherRecovery.tla`](.invariant/specs/WatcherRecovery.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | The factory starts with an issue that has no commands, posts, branch or pull request, and every step is a Next step. |
| `NoDoubleAnswer` | invariant | No command, build or merge is ever answered by more than one factory post. |
| `OnlyHolderActs` | invariant | No watcher ever takes an effect on GitHub while it doesn't hold the repository's lease. |
| `NoRunTwice` | invariant | No agent run is ever started twice for one command or build. |
| `OnePullRequest` | invariant | At most one pull request is ever opened for the ratification. |
| `OneMerge` | invariant | The pull request is merged at most once. |
| `MergeOnlyAfterGate` | invariant | The factory merges only after CI's gate passes on a pull request GitHub can merge. |
| `TypeOK` | invariant | Every part of the state always has the expected kind of value. |
| `Merged` | witness | The pull request can be merged and the merge reported. |
| `ReportedUnmergeable` | witness | A pull request whose gate passed can be reported as one that can't merge. |
| `StoppedRunReported` | witness | A build whose agent run stopped partway can be reported as stopped. |
| `RetryBuilt` | witness | A writer's retry of a stopped build can open the pull request and be answered. |
| `RecoveredAfterCrashes` | witness | After the most crashes allowed, the factory can still get the pull request merged and reported. |
| `CommandsAnswered` | property | Every command a writer gives is eventually answered by a factory post. |
| `GatedPullRequestsSettled` | property | Once CI's gate passes on the pull request, it is eventually merged or reported as one that can't merge. |
| `FairAcquire` | fairness | A running watcher eventually takes a lease that nobody holds. |
| `FairExpire` | fairness | A lease whose holder has crashed eventually runs out. |
| `FairAct` | fairness | A watcher holding the lease eventually takes the next effect that's due. |
| `FairFinishRun` | fairness | An agent run held by the lease's holder eventually finishes and is recorded. |
| `FairDropRun` | fairness | A watcher that has lost the lease eventually drops the agent run it was in. |
| `PostWithoutLooking` | bug | A watcher posts its answer without looking for an answer already there. |
| `PostWithoutLease` | bug | A watcher that woke after its lease ran out posts without checking the lease. |
| `RerunStoppedRun` | bug | A watcher that finds an agent run that stopped partway starts it again. |

Checked within `MaxCrashes = 2`, `Watchers = {w1, w2}`. To run the gate yourself:

```bash
go run ./cmd/invariant verify factory/recovery
```
