# Let a retry build a failed plumbing build again

Issue #271 in gitdek/invariant, opened by @gitdek: part 1 of 3, amended by hand and ratified as D-0202, and its exact proposal as D-0209.

When a plumbing build fails in the factory's own run, the factory keeps its work in a draft pull request: its agent stopped with partial work, its tests failed, or its review asked for changes. A writer's `/invariant retry` can only watch that draft again. #271 asks that a retry build the ratified plan again and replace the failed draft, while a retry after CI's gate fails still watches its pull request again.

The protocol lets a failed build with a pull request go back only to `pr_open`, so the rebuild needs a step of its own:

- `Rebuild`, at a writer's word, after the build failed its own gate in the factory's run and left a draft. The issue goes back to `ratified` with its ratification, and the draft's head, CI's result there, its lock, scope and merge state are retired, so none of them stands for the next build. The stops count afresh.
- `Retry` keeps its meaning, for stopped builds, CI failures, pull requests that can't merge and modeled projects.
- Every ratified statement keeps its meaning.

Keep `State`, `Successors` and every constant the watcher uses. The watcher takes no `Rebuild` step until #271's plumbing connects it.

Project: factory/protocol
