# Have the factory record a merged plan's decision

Issue #417 in gitdek/invariant, opened by @gitdek.

A plumbing build can't write `decisions/`. So after a person merges a factory pull request, the review's merger work includes recording the plan's decision in a follow-up pull request. The follow-up runs `invariant decisions decide`, cites the new decision on the SPEC lines and README passages the change wrote, and rolls SPEC up (#407, #413, #414, #415). It's the same few minutes every time.

**What to build.**
- Once a plan's pull request merges and its plan says a decision is owed, the factory opens one follow-up that records it: the journal entry and its log row, the citations, and the roll-up. An agent drafts the decision's text from the plan, the merged change and its review.
- A two-way door the plan carried out is recorded as `decided` by `agent`. Anything the owner would want a say in, and every one-way door, is recorded as `proposed`.
- The factory merges the follow-up once CI passes, since it changes only `decisions/`, SPEC.md and the README.

This changes what the factory does after a merge, which the issue protocol's model covers, so it's modeled rather than plumbing.

_Posted for @gitdek by a coding agent, at his request._

## Decided

- **F1. What evidence lets the factory conclude that an executed two-way door needs no owner input and may be recorded as decided by agent?** B. The drafting agent may infer from the plan, merged change and review that an executed two-way door needs no owner input and record it as decided by agent, leaving all other decisions proposed. (decided by @gitdek: https://github.com/gitdek/invariant/issues/417#issuecomment-6583572322)
- **F2. If drafting or creating the follow-up fails, its CI fails, or it has a merge conflict, how should the factory recover?** C. The factory retries failed drafting and creation automatically, but failed CI or a merge conflict blocks the work until a person requests another attempt. (decided by @gitdek: https://github.com/gitdek/invariant/issues/417#issuecomment-6585098741)
- **F3. If a person closes the follow-up without merging it while the plan still owes the decision, what should the factory do?** A. The factory leaves the follow-up closed and waits for an explicit request to resume the same follow-up. (decided by @gitdek: https://github.com/gitdek/invariant/issues/417#issuecomment-6583572322)
- **F4. After the follow-up has used both automatic repairs and a person asks for another attempt, how many additional repairs does that request authorize?** B. The factory receives a fresh allowance of up to two repairs in the same follow-up, checks each with fresh CI, and then waits for another explicit request if CI fails or a merge conflict remains after both repairs. (decided by @gitdek: https://github.com/gitdek/invariant/issues/417#issuecomment-6584856250)
- **F5. When a person explicitly requests resumption of a follow-up closed before both repairs in its current allowance were used, how many repairs should remain available?** B. The factory resumes the same follow-up with a fresh allowance of up to two repairs, checking each repair with fresh CI. (decided by @gitdek: https://github.com/gitdek/invariant/issues/417#issuecomment-6585098741)

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, at his request._

**@gitdek:**

F2: retry failed drafting and creation automatically. If the follow-up's CI fails or it has a merge conflict, as when two plans' decisions merge close together, repair the same follow-up within its files at most twice, each repair checked by CI afresh. Then stop, and wait for a person to ask for another attempt.


_Posted for @gitdek by a coding agent, at his request._

**@gitdek:**

C is the base for the revision above, which keeps C's automatic retries of drafting and creation and adds up to two repairs of a follow-up whose CI fails or that has a merge conflict, before it stops for a person. The factory waits for every fork's choice before it takes up a revision, so F2 needed one.

_Posted for @gitdek by a coding agent, at his request._

**@gitdek:**

_Posted for @gitdek by a coding agent, at his request._

**@gitdek:**

The rule behind F4 B and F5 B: every explicit request from a person to go on, whether it retries a follow-up that used its repairs or resumes one a person closed, gives that follow-up a fresh allowance of up to two repairs, each checked by fresh CI.

_Posted for @gitdek by a coding agent, at his request._

**@gitdek:**

Checked against the five forks as decided: F1 B, F2 C with the up-to-two automatic repairs from the revision, which RepairAllowance, RepairProgress and ExhaustionIsBlocked carry, F3 A, F4 B and F5 B. A follow-up merges only on passing CI for its current contents and with no conflict. It changes only `decisions/`, SPEC.md and the README. A one-way door is always proposed, which the DecideOneWay bug checks. TLC explored 1,436 states, reached every witness, held every property and caught every bug.

_Posted for @gitdek by a coding agent, at his request._
