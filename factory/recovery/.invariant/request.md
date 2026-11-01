# Model a retry that watches a failed draft again

Issue #354 in gitdek/invariant, opened by @gitdek: amended by hand and ratified, for its exact proposal, as D-0225.

The recovery model has no step for a writer's retry that watches a failed draft again. Merging needs a ready pull request, the merge is triggered only off one that isn't a draft, and `GatedPullRequestsSettled` leaves drafts out. The watcher still does it, as #145 and #263 have it: after a modeled project's build fails in the factory's own run, a retry marks its draft ready and merges it once CI's gate passes. Since #271 the adapter maps that draft to the model's ready pull request, and only a plumbing build's failed draft to the model's draft, so one GitHub state is mapped two ways, and the watch-again path is outside the proof.

- A writer's retry can watch the latest build's failed draft again, as a modeled project's does. It starts no build, and a draft a retry watches is never replaced.
- The factory answers that retry once. Once CI's gate passes on the draft, it marks the draft ready and merges it, or reports one it can't merge: GitHub refuses it, or it changes the trusted base, which only a person merges.
- The factory marks a draft ready only once it answered a retry that watches it, CI's gate passed on it and it can merge it.
- Every such retry is eventually answered, and every watched draft whose gate passed is eventually merged or reported.

The watcher runs on this core, so keep every exported name it uses and what it means. The adapter maps each pull request as GitHub has it, a draft as a draft and a ready one as ready, a modeled project's retry of a failed draft as a watch, and a plan's as a retry that replaces it.

Project: factory/recovery
