# Add a rate limiter for outgoing API calls

Issue #3 in gitdek/invariant, opened by @gitdek.

Outgoing calls to third-party APIs need to stay under each API's rate limit. Add a token-bucket rate limiter in front of them.

- The bucket holds up to a fixed number of tokens and refills one token at a time, at a fixed rate.
- Every outgoing call spends one token.
- Calls never go out faster than the bucket allows.

## Decided

- **F1. When a call is made and the API's bucket has no tokens left, what should happen?** B. The call waits until a token refills, and waiting calls go out in the order they were made. (decided by @gitdek: https://github.com/gitdek/invariant/issues/3#issuecomment-5840495537)
- **F2. How many tokens should a bucket hold when the limiter starts?** A. The bucket starts full, so a burst of calls up to its capacity can go out immediately. (decided by @gitdek: https://github.com/gitdek/invariant/issues/3#issuecomment-5840495537)
