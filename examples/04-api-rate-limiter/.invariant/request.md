# Take the rate limiter's model bounds out of its code

Issue #28 in gitdek/invariant, opened by @gitdek.

The rate limiter's code still carries the model's bounds. Its calls stop at `MAX_CALLS = 5` and its clock at `MAX_TIME = 3`, because the model needs those limits to stay finite. Change the code so that it's the rate limiter alone, written the way someone would ship it (D-0068):

- Its capacity, and how many calls may wait, are chosen when it's made. The time is passed in with each call.
- Nothing in it depends on the bounds: not the number of calls, how far the clock runs, or which APIs there are.
- The conformance driver keeps the bounds, as constants named after the model's, and it can count, so the gate checks the code one size larger too (D-0076).

What must be true doesn't change. Keep every ratified statement exactly as it is.

Project: examples/04-api-rate-limiter


_Posted for @gitdek by a coding agent, through the dashboard._

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, through the dashboard._
