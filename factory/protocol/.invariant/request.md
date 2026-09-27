# Add the watcher's failure steps to its protocol

Issue #13 in gitdek/invariant, opened by @gitdek.

In #12 the watcher checks each step it takes against its own protocol, `factory/protocol` from #9, and takes a step only if the ratified model has it. Three steps the watcher takes today aren't in the model, so the protocol refuses them, and the watcher's own tests show all three. The protocol should have them.

**A build that stops.** After a ratification, the build can stop before it makes a pull request. The agent stops, or can't run. Or two builds of the issue have already stopped partway, so the factory won't start a third. The factory posts that the build failed, with no pull request, and the issue needs a person.

**A build that fails its own gate.** When the code the factory wrote doesn't pass the gate in the factory's own run, the factory still opens the pull request, as a draft for people to look at. It posts that the build failed, and the issue needs a person. Nothing merges until a writer says retry, and then only once CI's gate passes on the pull request's current head.

**A pull request that can't merge.** CI's gate can pass on the pull request's current head while the pull request changes more than its one project, or its lock isn't exactly the ratified proposal. Then the factory posts that it failed, and the issue needs a person, just as when CI's gate fails. A writer can say retry once it's fixed.

Today, after a build stops with no pull request, nothing moves the issue on. Retry needs a pull request, and solve and revise don't work after a failure. Decide what a writer can do then.

Everything else stays as it is, including every rule about who directs the factory, what it ratifies and what it merges.


Project: factory/protocol

## Decided

- **F1. After a build stops before it makes a pull request, what can a writer do to move the issue on?** C. A writer can do either: retry builds the same ratified proposal again, and solve or revise drop the ratification and draft new statements. (decided by @gitdek: https://github.com/gitdek/invariant/issues/13#issuecomment-5852700646)
- **F2. If a writer can retry a stopped build, what happens when two builds of the issue have already stopped partway?** B. A writer's retry resets the limit, so the factory starts a new build and allows two more stops before it refuses again. (decided by @gitdek: https://github.com/gitdek/invariant/issues/13#issuecomment-5852700646)
