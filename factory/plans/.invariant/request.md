# Prove how a ratified plan of issues is worked: in order, one at a time, each opened once

Issue #94 in gitdek/invariant, opened by @gitdek.

A person can hand the factory a whole PRD (D-0105). On an issue that holds or links it, `/invariant plan` has the factory draft a plan of issues: at most 10, in order, each one either modeled or plumbing, with the questions it can't settle. A person ratifies the plan by its hash, and then the factory opens the issues and works them one at a time. Each issue then goes through the issue protocol, `factory/protocol`, as any issue does. Formalize the plan's own life, from ratified to finished, so the factory can build a core proved against it, and the watcher can run on it.

**What the factory does with a ratified plan.**

- **Open the next issue.** It opens the plan's issues in the plan's order, one at a time. It opens the next only once the one before it has merged. Opening an issue is two effects: creating it on GitHub, then recording on the plan's issue that it's open, with its number.
- **Solve it.** Each issue it opens is solved on the authority of the person who ratified the plan, as if they had written it.
- **Follow it.** An issue that merges lets the next one open. One that fails waits for a person on that issue, and the plan waits with it.
- **Finish.** Once every issue has merged, the factory says the plan is done on the plan's issue.
- **Stop.** A person can stop the plan at any point. Then no issue opens after that, and one already open goes on as it is.

**What a watcher sees.** Only what's on GitHub: the plan's issue and the factory's posts on it, the ratified plan and its hash, and the issues the factory opened with the plan they belong to. It remembers nothing else across a restart.

**What can go wrong.** A crash can come between any two effects, and a second watcher can take over when the lease runs out. Look before each effect: an issue already created for the plan's next step is that step, and isn't created again.

**What must be true.**

- No issue opens before the plan is ratified, or after it's stopped.
- Each of the plan's issues is opened at most once, however the factory crashes and restarts.
- The factory never opens an issue that isn't in the ratified plan.
- The issues open in the plan's order, and one opens only after every one before it has merged.
- At most one of the plan's issues is open at a time.
- Every issue the plan opens is solved on the ratifier's authority, and no one else's.
- As long as every issue eventually merges, and some watcher keeps running once the crashes stop, the plan eventually finishes.

Model one plan of three issues, each of which can merge or fail and later be retried to merge. Include a person who can stop the plan, a crash between any two effects, and a second watcher. Allow a small number of crashes, so the model stays finite.

Project: factory/plans


_Opened for @gitdek by a coding agent._
