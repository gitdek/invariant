# Merged plan decision follow-up

Written by Invariant for [#417](https://github.com/gitdek/invariant/issues/417): Have the factory record a merged plan's decision.

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/417#issuecomment-6585445786). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/DecisionFollowup.tla`](.invariant/specs/DecisionFollowup.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | The workflow starts in Init and advances only through Next steps. |
| `MergeRequiresCurrentCI` | invariant | The factory merges a follow-up only with passing CI for its current contents. |
| `OneFollowup` | invariant | A plan's decision obligation produces at most one follow-up pull request. |
| `RepairAllowance` | invariant | The factory uses at most two automatic repairs in each allowance. |
| `HumanHoldRespected` | invariant | A follow-up held for closure or exhausted repairs stays closed or blocked until a person explicitly resumes or retries it. |
| `TypeOK` | invariant | Every workflow value stays within its declared type. |
| `OnlyOwedMergedPlans` | invariant | Decision work starts only after the plan's pull request merges and only if the plan owes a decision. |
| `StableFollowupIdentity` | invariant | Creation, repairs, retries and resumption all refer to the same follow-up pull request. |
| `DraftUsesEvidence` | invariant | The drafting agent uses the plan, merged change and review as the decision's sources. |
| `DecisionClassification` | invariant | The decision is decided by agent exactly when the agent infers that an executed two-way door needs no owner input, and is proposed otherwise. |
| `FollowupHasDecision` | invariant | Every created follow-up contains a drafted decision classified as decided or proposed. |
| `DecisionCommandUsed` | invariant | The follow-up records its decision by running invariant decisions decide. |
| `JournalAndLogAgree` | invariant | The follow-up contains both a journal entry and its log row for the same new decision. |
| `CitationsCoverChangedText` | invariant | Every SPEC line and README passage written by the merged change cites the new decision in the follow-up. |
| `SpecIsRolledUp` | invariant | The follow-up includes the SPEC roll-up. |
| `FollowupScope` | invariant | The follow-up and all its repairs change only decisions/, SPEC.md and the README. |
| `MergeWithoutConflict` | invariant | The factory merges a follow-up only when it has no merge conflict. |
| `CurrentCIResults` | invariant | A completed CI result always describes the follow-up's current contents. |
| `EachRepairStartsFreshCI` | invariant | Every repair consumes one repair from the allowance and starts fresh CI with prior results invalidated. |
| `RequestsRenewAllowance` | invariant | Every explicit retry or resume starts with a fresh allowance of two repairs. |
| `AllowanceAccounting` | invariant | Only a repair consumes the repair allowance, and only an explicit retry or resume resets it. |
| `ExhaustionIsBlocked` | invariant | Work blocked for exhausted repairs has used both repairs and still has failed CI or a merge conflict. |
| `DecidedDecisionMerged` | witness | An executed two-way door inferred to need no owner input can be recorded as decided by agent and merged. |
| `OneWayProposedMerged` | witness | A one-way door can be recorded as proposed and merged. |
| `OwnerInputProposedMerged` | witness | An executed two-way door needing owner input can be recorded as proposed and merged. |
| `UnexecutedProposedMerged` | witness | A two-way door that was not executed can be recorded as proposed and merged. |
| `NoDecisionWork` | witness | A plan can merge without creating a follow-up when it owes no decision. |
| `DraftRetried` | witness | The factory can automatically retry a failed drafting attempt. |
| `CreationRetried` | witness | The factory can automatically retry a failed follow-up creation attempt. |
| `RepairCanMerge` | witness | A repaired follow-up can pass fresh CI and merge. |
| `RepairsExhausted` | witness | A follow-up can exhaust both repairs and block for a person. |
| `PersonRenewsExhaustedWork` | witness | A person can retry an exhausted follow-up with two fresh repairs available. |
| `ClosedFollowupWaits` | witness | A person can close the follow-up and leave its decision obligation waiting for explicit resumption. |
| `ResumeRenewsUnusedRepairs` | witness | Resuming a follow-up closed after one repair can restore two available repairs. |
| `ResumeRenewsExhaustedRepairs` | witness | Resuming a follow-up closed after both repairs can restore two available repairs. |
| `DraftProgress` | property | An eligible merged plan or a failed drafting attempt eventually starts a drafting attempt. |
| `CreationProgress` | property | A completed draft or a failed creation attempt eventually starts a follow-up creation attempt. |
| `RepairProgress` | property | A failed or conflicted follow-up with repairs available eventually receives a repair unless a person closes it. |
| `BlockingProgress` | property | A failed or conflicted follow-up with no repairs available eventually blocks unless a person closes it. |
| `MergeProgress` | property | A follow-up ready to merge eventually merges unless a person closes it or a new conflict requires repair. |
| `DraftFairness` | fairness | The factory eventually starts a drafting attempt whenever that step remains enabled. |
| `CreationFairness` | fairness | The factory eventually starts a creation attempt whenever that step remains enabled. |
| `RepairFairness` | fairness | The factory eventually performs an available repair whenever that step remains enabled. |
| `BlockingFairness` | fairness | The factory eventually blocks exhausted work whenever that step remains enabled. |
| `MergeFairness` | fairness | The factory eventually merges a ready follow-up whenever that step remains enabled. |
| `MergeBeforeCI` | bug | The factory incorrectly merges while the current CI run is still pending. |
| `CreateDuplicate` | bug | The factory incorrectly creates a second follow-up for the same plan. |
| `RepairPastAllowance` | bug | The factory incorrectly makes a third repair without an explicit request. |
| `ResumeWithoutPerson` | bug | The factory incorrectly resumes a closed follow-up without an explicit request. |
| `DecideOneWay` | bug | The drafting agent incorrectly records a one-way door as decided by agent. |

Decided on the issue:

- What evidence lets the factory conclude that an executed two-way door needs no owner input and may be recorded as decided by agent? **B.** The drafting agent may infer from the plan, merged change and review that an executed two-way door needs no owner input and record it as decided by agent, leaving all other decisions proposed. (@gitdek)
- If drafting or creating the follow-up fails, its CI fails, or it has a merge conflict, how should the factory recover? **C.** The factory retries failed drafting and creation automatically, but failed CI or a merge conflict blocks the work until a person requests another attempt. (@gitdek)
- If a person closes the follow-up without merging it while the plan still owes the decision, what should the factory do? **A.** The factory leaves the follow-up closed and waits for an explicit request to resume the same follow-up. (@gitdek)
- After the follow-up has used both automatic repairs and a person asks for another attempt, how many additional repairs does that request authorize? **B.** The factory receives a fresh allowance of up to two repairs in the same follow-up, checks each with fresh CI, and then waits for another explicit request if CI fails or a merge conflict remains after both repairs. (@gitdek)
- When a person explicitly requests resumption of a follow-up closed before both repairs in its current allowance were used, how many repairs should remain available? **B.** The factory resumes the same follow-up with a fresh allowance of up to two repairs, checking each repair with fresh CI. (@gitdek)

Checked within `Followups = {f1, f2}`, `ReadmePassages = {r1}`, `RepairsPerRequest = 2`, `SpecLines = {s1}`. To run the gate yourself:

```bash
go run ./cmd/invariant verify examples/06-decision-followup
```
