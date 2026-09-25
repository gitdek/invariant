# API rate limiter

Written by Invariant for [#3](https://github.com/gitdek/invariant/issues/3): Add a rate limiter for outgoing API calls.

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/3#issuecomment-5840633638). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/RateLimiter.tla`](.invariant/specs/RateLimiter.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | The system starts in Init, and every step is a Next step. |
| `TypeOK` | invariant | Each API's bucket always holds between zero and its capacity in tokens, and every call is tracked with when it was made and when it went out. |
| `WithinRate` | invariant | Over any stretch of time, an API never receives more calls than its bucket's capacity plus one per refill period in that stretch. |
| `FirstInFirstOut` | invariant | Calls to an API wait and go out in exactly the order they were made. |
| `NoNeedlessWait` | invariant | A call only waits while its API's bucket has no tokens left. |
| `NoCallLost` | invariant | Every call that was made has either gone out or is still waiting. |
| `BurstGoesOut` | witness | A full bucket's worth of calls can go out immediately at the start. |
| `CallWaits` | witness | A call can have to wait for a token. |
| `WaitingCallGoesOut` | witness | A call that waited can go out once a token refills. |
| `BucketRefills` | witness | A bucket that was spent from can refill back to full. |
| `SendWithoutToken` | bug | A call goes out even though its API's bucket is empty. |
| `JumpQueue` | bug | A refilled token goes to the newest waiting call instead of the oldest. |

Decided on the issue:

- When a call is made and the API's bucket has no tokens left, what should happen? **B.** The call waits until a token refills, and waiting calls go out in the order they were made. (@gitdek)
- How many tokens should a bucket hold when the limiter starts? **A.** The bucket starts full, so a burst of calls up to its capacity can go out immediately. (@gitdek)

Checked within `Apis = {a1, a2}`, `Capacity = 2`, `MaxCalls = 4`, `MaxTime = 3`. To run the gate yourself:

```bash
go run ./cmd/invariant verify examples/04-api-rate-limiter
```
