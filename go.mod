module github.com/gitdek/invariant

go 1.27.1

// The factory runs on its own protocol, which the factory wrote and Gobra
// proves (D-0064). It's a module of its own, so CI's gate verifies it like
// any other project.
require github.com/gitdek/invariant/factory/protocol v0.0.0

replace github.com/gitdek/invariant/factory/protocol => ./factory/protocol
