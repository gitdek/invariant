# Watcher crash recovery

Written by Invariant for [#23](https://github.com/gitdek/invariant/issues/23): Prove the factory recovers from a crash at any point, and from a second watcher. The factory rebuilt its explorer for [#68](https://github.com/gitdek/invariant/issues/68), and a coding agent amended it by hand for [#271](https://github.com/gitdek/invariant/issues/271), so a writer's retry can replace a failed draft with a new pull request.

@gitdek ratified these statements by hand, as [D-0203](../../decisions/journal/D-0203.jsonl), in place of a comment on the issue: they amend the statements he ratified [on #68](https://github.com/gitdek/invariant/issues/68#issuecomment-5875564945). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/WatcherRecovery.tla`](.invariant/specs/WatcherRecovery.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | The factory starts with an issue that has no commands, posts, branch or pull request, and every step is a Next step. |
| `NoDoubleAnswer` | invariant | No command, retry, build or merge is ever answered by more than one factory post. |
| `OnlyHolderActs` | invariant | No watcher ever takes an effect on GitHub while it doesn't hold the repository's lease. |
| `NoRunTwice` | invariant | No agent run is ever started twice for one draft or build. |
| `OnePullRequest` | invariant | Each build opens at most one pull request, even once that pull request is closed or merged. |
| `OneOpenPullRequest` | invariant | At most one of the ratified proposal's pull requests is open at a time: a retry's build opens its own only after the draft it replaces is closed. |
| `RetiredOnlyByRecord` | invariant | The factory closes a draft only once a record every watcher can read says a retry replaces it. |
| `NoReplacingMerged` | invariant | A retry whose draft someone merged starts no build and opens no pull request. |
| `OwnGate` | invariant | CI's gate counts only on the latest build's own pull request: it never stands passed before that pull request opens. |
| `OwnRun` | invariant | The latest build's result comes only from its own agent run, never from an earlier build's. |
| `OneMerge` | invariant | The pull request is merged at most once. |
| `MergeOnlyAfterGate` | invariant | The factory merges only after CI's gate passes on a pull request GitHub can merge. |
| `TypeOK` | invariant | Every part of the state always has the expected kind of value. |
| `Merged` | witness | The pull request can be merged and the merge reported. |
| `ReportedUnmergeable` | witness | A pull request whose gate passed can be reported as one that can't merge. |
| `StoppedRunReported` | witness | A build whose agent run stopped partway can be reported as stopped. |
| `RetryBuilt` | witness | A writer's retry of a stopped build can open the pull request and be answered. |
| `RecoveredAfterCrashes` | witness | After the most crashes allowed, the factory can still get the pull request merged and reported. |
| `Replaced` | witness | A writer's retry can replace a failed draft: the factory closes the draft, and the retry's build opens a new pull request and is answered. |
| `ReplacedAndMerged` | witness | The pull request that replaced a failed draft can be merged and the merge reported. |
| `FailedAgain` | witness | A retry's build can leave a failed draft too, which a writer can retry in turn. |
| `StoppedAfterRetiring` | witness | A retry's build can stop after the draft it replaces was closed, and be reported as stopped, keeping the record the next retry builds on. |
| `MergedDraftKept` | witness | A retry whose draft someone merged is answered without replacing it. |
| `ClosedDraftNoted` | witness | A retry whose draft someone else closed before its record was stored is answered without a build. |
| `CommandsAnswered` | property | Every command and retry a writer gives is eventually answered by a factory post. |
| `BuildsAnswered` | property | Every build a ratification or a retry starts is eventually answered: by its pull request, or by a report that it stopped. |
| `GatedPullRequestsSettled` | property | Once CI's gate passes on a pull request that isn't a failed draft, the factory eventually merges it or reports it: as one that can't merge, or as merged or closed by someone else. |
| `FairAcquire` | fairness | A running watcher eventually takes a lease that nobody holds. |
| `FairExpire` | fairness | A lease whose holder has crashed eventually runs out. |
| `FairAct` | fairness | A watcher holding the lease eventually takes the next effect that's due. |
| `FairFinishRun` | fairness | An agent run held by the lease's holder eventually finishes and is recorded. |
| `FairDropRun` | fairness | A watcher that has lost the lease eventually drops the agent run it was in. |
| `PostWithoutLooking` | bug | A watcher posts its answer without looking for an answer already there. |
| `PostWithoutLease` | bug | A watcher that woke after its lease ran out posts without checking the lease. |
| `RerunStoppedRun` | bug | A watcher that finds an agent run that stopped partway starts it again. |
| `OpenWithoutLookingClosed` | bug | A watcher finds no open pull request for the latest build, misses the one it opened that was since closed or merged, and opens another. |
| `OpenBeforeRetiring` | bug | A retry's build opens its pull request while the draft it replaces is still open. |
| `RetireWithoutRecord` | bug | The factory closes the draft before storing the retry's record, so after a crash its own close looks like someone else's. |
| `ReplaceMergedDraft` | bug | A retry whose draft someone merged builds again anyway. |
| `KeepGateAcrossRetry` | bug | A writer's retry keeps CI's result from the draft it replaces. |
| `FinishStaleRun` | bug | A watcher whose agent run a retry made stale records it as the latest build's. |
| `CloseWithoutLease` | bug | A watcher that woke after its lease ran out closes the draft without checking the lease. |

Checked within `MaxCrashes = 2`, `Watchers = {w1, w2}`. To run the gate yourself:

```bash
go run ./cmd/invariant verify factory/recovery
```
