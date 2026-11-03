# Authorize decision follow-ups in the issue protocol

Issue #428 in gitdek/invariant, opened by @invariant-code-factory[bot].

Project: factory/protocol

After the follow-up effect-outcome amendment, authorize its integration in factory/protocol. D-0064 requires every issue step to follow this core. The current protocol ends at the merged source issue and allows factory merges only for its ordinary proposal, ratified lock and one-project scope. A decision follow-up needs distinct authority.

Amend the ratified protocol to authorize a linked decision-follow-up work item whose authority is the exact ratified plan, its merged pull request and its owed decision. Keep the source plan's merged history and ratification intact. The follow-up has its own stable pull-request identity, current head, CI and scope. It requires the corresponding accepted step of the amended factory/decision-followup.Workflow. Its classification, retries, two repairs per allowance, holds and effect outcomes remain that core's policy. Only an authenticated person with repository write access can request retry or resume, never a bot. Closing the follow-up preserves its human hold. A retry of blocked work renews its allowance; a retry of closed work explicitly resumes the same pull request. Neither restarts or replaces the already merged source build.

Define the narrow follow-up artifact class: only decisions/, SPEC.md and README.md, tied to its source obligation, with no new plan ratification or fabricated project lock. Its current CI includes the decision graph check. Existing docs/decisions CI may intentionally skip the full project gate; that is admissible only together with successful current-revision decision checks and independently verified follow-up scope. Ordinary build scope, current-head CI, lock ratification, trusted-base merge restrictions and command authorization retain their existing meaning. An unrelated PR cannot acquire follow-up authority merely by a branch name or a claimed source.

Prove the amended core, update its explorer, pinned statements through the normal ratification flow and project README, and run the project's complete verification gate. Include witnesses for an eligible human-merged plan leading to a follow-up merge, a closed follow-up resuming under the same identity, and continued ordinary build behavior. Include counterexamples for an unmerged or non-owing source, a forged association, broader scope, stale CI/head and an unauthorized retry. This issue changes the modeled authorization boundary, not the watcher adapters. Keep ordinary operations usable by the existing watcher until later plumbing enables follow-ups.

Basis: D-0064, D-0105/D-0109 and D-0079/D-0080; #417's ratified authority, scope, identity and current-CI rules and the preceding effect-outcome amendment. The merger records this amendment's decision and keeps root docs accurate under D-0122 without claiming live watcher integration.

◉ Opened by Invariant for step 2 of 6 of the plan of issues on #421, which @gitdek [ratified](https://github.com/gitdek/invariant/issues/421#issuecomment-6587654565).

<!-- invariant-plan #421 sha256:35403d5bf0549f22c8840b97420b89389648fabb0ba0fba8f24cd1ffc5dc23d2 step 2 -->

## Discussion

**@gitdek:**

@gitdek chose to ratify. Every existing protocol guarantee is unchanged; only `TypeOK` widens, for the new state. Follow-up authority comes only from the exact ratified plan of a merged source that still owes a decision. A follow-up merges only on its current head, with independently verified narrow scope, current decision checks and a trusted base. Retries and resumes come only from authenticated writers, and a resume keeps the same pull request. TLC explored 132,486 states, reached all 21 witnesses and caught all 20 known bugs.

_Posted for @gitdek by a coding agent, at his request._
