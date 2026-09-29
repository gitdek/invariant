# Prove the decision journal: no two decisions share an ID, the journal only grows, and only a person ratifies

Issue #91 in gitdek/invariant, opened by @gitdek.

Every decision is written through `invariant decisions` to two places: the decision's journal file in a checkout of its repository, and one store on the machine that runs the factory, which every process opens directly (D-0096, D-0097). Several checkouts of one repository can write at once, one per branch, and each branch reaches main through a pull request. Formalize how decisions are written and merged, so the factory can build a core proved against it, and `invariant decisions` can run on it, the way the watcher runs on `factory/protocol` (D-0101, D-0102, D-0103).

**What a write does.** A write holds the store's lock from start to finish, so writes happen one at a time:

- **Decide** takes the project's next ID: one more than the highest the store has ever journaled for the project, or than any in the writer's checkout. Then it writes the decision's journal file in the checkout, and records the line in the store.
- **Ratify**, **supersede** and **link** each add one line to an existing decision's file in the checkout, then record the line in the store.

The file and the store's record are two effects, and a process can stop between them.

**What a merge does.** A branch merges into main only when every line main has is still in the branch, in its place: each of main's journal files is the start of the branch's file (D-0103). A merge adds the branch's new files and new lines to main. Two branches can each add a line to the same decision. The second to merge then has to take main in first, and a line main already has stays where it is.

**The rules.**

- A journal's first line records its decision, and no later line does.
- An agent never decides a one-way door. It proposes one, and only a person ratifies anything: a ratify line names a person.
- A decision that's superseded or ratified already isn't ratified again.
- The store's journal only grows, and it keeps the lines of branches that were never merged.

**What must be true.**

- No two decisions share an ID, in any checkout, on any branch, merged or not. A branch that's abandoned may leave a gap in the numbers.
- Merging never changes or removes a line main already has.
- Every ratify line on main names a person, and no one-way door on main was decided by an agent.
- A decision's state is its lines, folded in order, so every checkout that holds the same lines agrees on it.

Model one project, two checkouts on two branches, and main. Include a person and an agent writing, a process that can stop between writing a file and recording it in the store, and merges in either order. Allow a small number of decisions and lines, so the model stays finite.

Project: factory/journal

_Opened for @gitdek by a coding agent. It isn't labeled for the factory yet: that's his call._

## Decided

- **F1. A decide writes the journal file first and records it in the store second. If the process stops between the two, the store never learns the new ID, so the other checkout's next decide takes the same ID. That breaks 'no two decisions share an ID'. What should happen?** A. Decide records the ID in the store first and writes the file second, so a stop can only leave a gap in the numbers, never a reused ID. (decided by @gitdek: https://github.com/gitdek/invariant/issues/91#issuecomment-5881341825)
- **F2. Two branches each ratify, or one supersedes and the other ratifies, the same decision. Which journal does the rule 'a decision that's superseded or ratified already isn't ratified again' check, and what happens at merge?** B. A write checks only its own checkout, and the second branch can't merge while taking main in would put a ratify after a ratify or supersede; its line has to be dropped from the branch first, though the store keeps it. (decided by @gitdek: https://github.com/gitdek/invariant/issues/91#issuecomment-5881341825)
