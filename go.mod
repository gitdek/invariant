module github.com/gitdek/invariant

go 1.27.1

replace github.com/gitdek/invariant/factory/protocol => ./factory/protocol

require (
	// A ratified plan of issues is worked only as its own core allows, which
	// the factory wrote and Gobra proves (#94, #126).
	github.com/gitdek/invariant/factory/decision-state v0.0.0
	github.com/gitdek/invariant/factory/ids v0.0.0
	github.com/gitdek/invariant/factory/plans v0.0.0
	// The factory runs on its own protocol, which the factory wrote and Gobra
	// proves (D-0064). It's a module of its own, so CI's gate verifies it like
	// any other project.
	github.com/gitdek/invariant/factory/protocol v0.0.0
	// It takes each effect only as its recovery core allows, which the factory
	// also wrote and Gobra proves (D-0069).
	github.com/gitdek/invariant/factory/recovery v0.0.0
	modernc.org/sqlite v1.60.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.48.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/gitdek/invariant/factory/recovery => ./factory/recovery

replace github.com/gitdek/invariant/factory/plans => ./factory/plans

replace github.com/gitdek/invariant/factory/decision-state => ./factory/decision-state

replace github.com/gitdek/invariant/factory/ids => ./factory/ids
