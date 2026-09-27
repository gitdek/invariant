# Prove the factory recovers from a crash at any point, and from a second watcher

Issue #23 in gitdek/invariant, opened by @gitdek.

The watcher takes each step on an issue as a few effects on GitHub, one after another, and it can stop between any two of them: a crash, a restart, a laptop going to sleep. Two watchers could also run on one repository at once. Formalize how the watcher recovers, so that the factory can build a core proved against it, and the watcher can run on it (D-0069). The issue protocol, `factory/protocol`, already says which steps the factory may take. This says how each step's effects happen exactly once.

**Steps and their effects.** Each step answers one command, or one event such as CI finishing, with its effects in this order:

- **Draft**, for solve, revise, or the last answer to the questions: an agent run, then a post that answers the command.
- **Ratify**: a push of the ratification to the issue's branch, then a post that answers the command.
- **Build**, right after a ratification, or for a writer's retry of a build that stopped: an agent run, a push of the code, a pull request from the branch, then a post.
- **Merge**, once CI's gate passes on the pull request's head: the merge, then a post.
- **Note**, for a command that changes nothing: a post.

**What a watcher sees.** Only what's on GitHub and what's recorded where every watcher can read it: the factory's posts and the command each one answers, the issue's branch and what's on it, the pull request and whether it's merged and by whom, and each agent run that was recorded and whether it finished. It remembers nothing else across a restart.

**The rules.**

- **Before each effect, a watcher looks for it.** If it's already there, the watcher takes it as done and goes on to the next one: a post that already answers the command, a branch that already holds the ratification or the code, a pull request already open from the branch, a merge already made.
- **An agent run is recorded before it starts.** A run that was recorded and never finished stopped partway. A watcher that finds one says on the issue that the run stopped, and doesn't start another. Only a writer's command starts a new run.
- **One watcher acts at a time.** A watcher acts only while it holds the repository's lease. Taking the lease is atomic, so two watchers can't both hold it. The holder renews it while it runs, agent runs included. A lease that isn't renewed runs out after a few minutes, and then another watcher can take it. A watcher checks that it still holds the lease just before each effect. The check and the effect take far less time than a lease lasts, so treat them as one step.
- **A crash can come between any two effects, or during an agent run.** The watcher that restarts, or another one, knows only what it sees.

**What must be true.**

- No effect happens twice: no second post that answers one command, no second pull request for one ratification, and no second merge.
- No agent run starts twice for one command.
- Only the lease's holder takes an effect.
- Every command a writer gives is eventually answered, and every pull request whose gate passes is eventually merged, or reported as one that can't merge, as long as some watcher keeps running and the crashes stop.

Model one issue with a few commands, one watcher that can crash and restart, and a second watcher that can take the lease when it runs out. Allow a small number of crashes, so the model stays finite, and don't count polls.

Project: factory/recovery


_Posted for @gitdek by a coding agent, through the dashboard._

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, through the dashboard._
