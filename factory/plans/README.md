# Ratified plan runner

Written by Invariant for [#94](https://github.com/gitdek/invariant/issues/94): Prove how a ratified plan of issues is worked: in order, one at a time, each opened once.

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/94#issuecomment-5881897708). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/PlanRun.tla`](.invariant/specs/PlanRun.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | The plan starts unratified with no issues opened, and every step is one of the model's steps. |
| `OpenedAtMostOnce` | invariant | Each of the plan's issues is created on GitHub at most once, however the factory crashes and restarts. |
| `NoneOpenedAfterStop` | invariant | No issue is opened after the plan is stopped. |
| `SolvedOnRatifierAuthority` | invariant | Every issue the plan opens is solved on the authority of the person who ratified the plan, and no one else's. |
| `TypeOK` | invariant | The plan's ratifier, stop, opened issues, records, done post, lease holder and crash count always have their expected types. |
| `OpenedOnlyWhenRatified` | invariant | No issue is opened before the plan is ratified. |
| `OnlyPlannedIssues` | invariant | Every issue the factory opens is one of the ratified plan's issues. |
| `InOrderAfterMerge` | invariant | Issues open in the plan's order, and each opens only after every issue before it in the plan has been opened and merged. |
| `AtMostOneOpen` | invariant | At most one of the plan's issues is open (not yet merged) at a time. |
| `RecordsAreReal` | invariant | The plan's issue only records issues that were actually created. |
| `DoneOnlyWhenAllMerged` | invariant | The factory says the plan is done only once every one of its issues has merged. |
| `PlanDone` | witness | A plan can run to the end, with every issue opened, recorded and merged, and the plan said done. |
| `IssueFailed` | witness | One of the plan's issues can fail and hold the plan up. |
| `StoppedWithIssueOpen` | witness | A person can stop the plan while one of its issues is still open. |
| `FinishedAfterCrashes` | witness | A plan can still finish after the factory has crashed and another watcher took over. |
| `PlanFinishes` | property | As long as every opened issue eventually merges and a watcher keeps running once crashes stop, a ratified plan eventually finishes unless a person stops it. |
| `AcquireFair` | fairness | When no watcher holds the lease, a watcher eventually takes it. |
| `CreateFair` | fairness | A watcher holding the lease that can create the next issue eventually does. |
| `RecordFair` | fairness | A watcher holding the lease eventually records each created issue on the plan's issue. |
| `FinishFair` | fairness | A watcher holding the lease eventually says the plan is done once every issue has merged. |
| `CreateUnlessRecorded` | bug | The factory trusts the plan's issue instead of GitHub, and creates the next issue again after a crash between creating and recording it. |
| `CreateIgnoringStop` | bug | The factory opens the next issue without checking whether the plan was stopped. |
| `CreateAsSomeoneElse` | bug | The factory opens an issue on the authority of someone other than the ratifier. |

Checked within `MaxCrashes = 2`, `N = 3`, `NoOne = NoOne`, `People = {p1, p2}`, `Watchers = {w1, w2}`. To run the gate yourself:

```bash
go run ./cmd/invariant verify factory/plans
```
