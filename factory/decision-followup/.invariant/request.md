# Model recoverable decision follow-up effect outcomes

Issue #423 in gitdek/invariant, opened by @invariant-code-factory[bot].

Project: factory/decision-followup

Before #421 wires #417/#420's Workflow, amend its boundary with real effects. Repair spends an allowance and enters Open/CIPending with no failed-repair outcome; AutoMerge enters terminal Merged before GitHub can refuse. After passing CI, a head change cannot invalidate that result because CheckCompletes accepts only pending CI. Plumbing cannot repair these gaps by resetting snapshots or inventing observations.

Model recoverable repair and merge authorization/outcomes, revision/CI invalidation and external merge observation. Preserve #417's policy: only an owed merged plan starts work; evidence determines decided-by-agent versus proposed; one stable follow-up stays in its allowed scope; failed drafts/creation retry automatically; at most two repairs occur per allowance; only an explicit authorized retry/resume renews it; closure and exhaustion wait for a person.

Charge each authorized repair attempt exactly once, including an agent that fails or leaves no publishable change. Such an outcome retains the real failure/conflict cause and can exhaust the allowance; it must neither fabricate a GitHub CI failure nor wait forever for a run that was never requested. Published repairs require fresh CI. Report repair execution failure separately from CI, and keep interrupted effects recoverable without running an attempt twice.

An accepted merge authorization is not a completed remote merge. Reconcile success or a lost response; on a definitive refusal return to a truthful nonterminal condition reflecting the observed head, CI, conflict or closure. New revisions invalidate prior CI without changing repair usage or lifting a hold. Closure during an in-flight effect remains effective; a person must explicitly resume that same PR. Record an externally merged PR as externally settled without pretending the factory merged it or supplying invented passing CI. Stable operation identities and outcome guards must reject stale completions.

Amend the model, proved core, explorer and project README through ratification and run the full gate. Add witnesses and counterexamples for failed/no-change repairs, exhaustion and renewal, head movement after CI, closure/conflict after merge authorization, lost replies, external merge and stale outcomes. Preserve the existing classification, scope and artifact guarantees. Later protocol/recovery work composes these operations; the adapters remain tested plumbing.

Basis: #417's ratified statements, #421's persisted-snapshot and real-effect requirements, D-0064/D-0069 and D-0122. The merger records this amendment's decision. Do not claim the watcher uses it yet.

◉ Opened by Invariant for step 1 of 6 of the plan of issues on #421, which @gitdek [ratified](https://github.com/gitdek/invariant/issues/421#issuecomment-6587654565).

<!-- invariant-plan #421 sha256:35403d5bf0549f22c8840b97420b89389648fabb0ba0fba8f24cd1ffc5dc23d2 step 1 -->

## Decided

- **F1. When a person explicitly requests resumption of a closed follow-up while an earlier repair or merge outcome is still unknown, when should resumption renew the allowance and release the closure hold?** A. Defer the explicit resume request and keep the closure hold and allowance unchanged until reconciliation confirms that the PR is still unmerged, then apply that request with a fresh allowance. (decided by @gitdek: https://github.com/gitdek/invariant/issues/423#issuecomment-6587773063)
- **F2. If GitHub definitively refuses a merge, for example because of permissions or branch protection, but the observed PR remains open at the same head with current passing CI and no conflict, should the factory try again automatically or wait for a person?** B. Hold the follow-up in a nonterminal merge-refused state until a person explicitly authorizes retry, which renews the repair allowance. (decided by @gitdek: https://github.com/gitdek/invariant/issues/423#issuecomment-6587773063)

## Discussion

**@gitdek:**

F1 A applies a person's resume once reconciliation settles the earlier outcome, so nobody has to ask twice and nothing runs twice. F2 B holds a definitively refused merge for a person: permissions or branch protection won't change on a retry, and retrying would loop.

_Posted for @gitdek by a coding agent, at his request._

**@gitdek:**

Checked against F1 A and F2 B and against #421's step 1. The weakened promises are honest about real effects: a merge attempt rather than a remote merge, a repair charged when it's authorized, and holds that also cover a definitive merge refusal. The new invariants keep every effect from running twice, reject stale outcomes, and keep anyone from inventing CI for a failed repair or an external merge. Every #417 guarantee that isn't amended is kept. TLC explored 20,889 states, reached all 38 witnesses, held all 7 properties and caught all 16 known bugs.

_Posted for @gitdek by a coding agent, at his request._
