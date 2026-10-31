# Let a retry build a failed plumbing build again

Issue #271 in gitdek/invariant, opened by @gitdek: part 2 of 3, amended by hand and ratified as D-0203.

A retry that builds again replaces the failed draft with a new pull request (#271). The recovery core pinned one pull request per ratification, so it needs a chain of replacements:

- Each writer's retry starts a new build, with its own agent run, which replaces the failed draft the build before left. At most one of the ratified proposal's pull requests is open at a time, and each build opens at most one.
- Before it closes that draft, the factory stores a record, where every watcher can read it, that the retry replaces it. Then it closes the draft and answers the retry, and the new build opens its own pull request and is answered apart. So after a crash, the factory's own close is never taken for someone else's.
- A retry whose draft someone merged, or closed before the record was stored, is answered without a build. A run a retry superseded never counts for the new build, and CI's gate counts only on the latest build's own pull request.
- Every retry is eventually answered, and so is every build: by its pull request, or by a report that it stopped.

The watcher runs on this core, so keep every exported name it uses and what it means, except the latest build's pull request, which now has a state: `Opened` says the build opened one, and `PR` whether it's a draft, ready, closed or merged. The watcher maps none of its steps onto a retry's replacement until #271's plumbing connects them.

Project: factory/recovery
