# Decision journal

Written by Invariant for [#91](https://github.com/gitdek/invariant/issues/91): Prove the decision journal: no two decisions share an ID, the journal only grows, and only a person ratifies.

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/91#issuecomment-5881475888). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/DecisionJournal.tla`](.invariant/specs/DecisionJournal.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | The journal starts empty, and every step is a decide, ratify, supersede or link write, a process finishing or stopping mid-write, a merge, a branch taking main in, or a branch dropping a conflicting ratify. |
| `TypeOK` | invariant | Every checkout and main hold a journal of lines per decision ID, the store holds a sequence of recorded lines, and at most one write is in progress. |
| `UniqueIds` | invariant | No two decisions ever take the same ID, in any checkout, on any branch, merged or not. |
| `MainKeepsLines` | invariant | Every line main has ever had is still on main, in the same place. |
| `StoreKeepsLines` | invariant | The store's journal only grows: every line it ever recorded is still there, in the same place, including lines from branches that never merged. |
| `RatifiedByPerson` | invariant | Every ratify line on main names a person. |
| `NoAgentOneWayDoor` | invariant | No one-way door on main was decided by an agent; an agent can only propose one. |
| `JournalsWellFormed` | invariant | In every checkout and on main, a journal's first line records its decision, no later line does, and no ratify comes after a ratify or a supersede. |
| `RatifiedOnMain` | witness | A decision can be ratified and reach main. |
| `AgentProposalRatifiedOnMain` | witness | An agent can propose a one-way door and a person can ratify it on main. |
| `SupersededOnMain` | witness | A decision can be superseded on main. |
| `BothBranchesOnOneDecision` | witness | Two branches can each add a line to the same decision and both reach main. |
| `GapInIds` | witness | A process that stops mid-decide can leave a gap in the IDs while a later decision reaches main. |
| `DecideFileFirst` | bug | A decide writes the journal file first and stops before the store records the ID. |
| `MergeOverwrite` | bug | A branch merges without checking that main's lines are still in it, in place. |
| `AgentRatify` | bug | An agent writes a ratify line. |
| `SyncUnchecked` | bug | A branch takes main in without checking whether that puts a ratify after a ratify or supersede. |

Decided on the issue:

- A decide writes the journal file first and records it in the store second. If the process stops between the two, the store never learns the new ID, so the other checkout's next decide takes the same ID. That breaks 'no two decisions share an ID'. What should happen? **A.** Decide records the ID in the store first and writes the file second, so a stop can only leave a gap in the numbers, never a reused ID. (@gitdek)
- Two branches each ratify, or one supersedes and the other ratifies, the same decision. Which journal does the rule 'a decision that's superseded or ratified already isn't ratified again' check, and what happens at merge? **B.** A write checks only its own checkout, and the second branch can't merge while taking main in would put a ratify after a ratify or supersede; its line has to be dropped from the branch first, though the store keeps it. (@gitdek)

Checked within `Agents = {a1}`, `Checkouts = {c1, c2}`, `MaxId = 2`, `MaxLen = 2`, `MaxWrites = 3`, `People = {p1}`. To run the gate yourself:

```bash
go run ./cmd/invariant verify factory/journal
```
