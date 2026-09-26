# Refuse calls when too many are already waiting

Issue #5 in gitdek/invariant, opened by @gitdek.

Right now, a call waits for as long as it takes when its API's bucket is empty. When calls pile up, that queue grows without end. A call should be refused right away once too many calls are already waiting for the same API.

Project: examples/04-api-rate-limiter
