# Working in this repo

- `SPEC.md` is the current state and the only document to build against. `decisions/log.md` is the record it's rolled up from.
- A decision that isn't written down didn't happen. When a choice is made during a session, add it to `decisions/log.md` in the same change. A two-way door gets one line. A one-way door also gets a full record in `decisions/D-NNNN-slug.md`: the options considered, why, and what would reopen it.
- If you make a two-way-door call while carrying out ratified work, log it as `decided`. Anything the owner would want a say in, and every one-way door, goes in as `proposed` until @gitdek ratifies it.
- Don't build against anything listed under "Undecided" in `SPEC.md`. Ask instead.
- Never edit a pinned statement or a `ratified.lock` without a ratified decision that changes it.
- Commits and pull requests are authored by @gitdek alone. Don't add `Co-Authored-By` lines or any other attribution to commit messages or PR descriptions.

## Commands

- `go test ./...`: unit tests. Each example is its own Go module, so the root module doesn't include it.
- `go test -tags integration ./internal/verify/`: the gate against the real verifiers, including the ways it must fail. Needs Docker.
- `go run ./cmd/invariant verify examples/02-twophase-commit`: run the gate and print the receipt.
- `go run ./cmd/invariant verify examples/02-twophase-commit-py-proved`: the same, with Nagini proving the Python core. The first run builds the Nagini image for linux/amd64, which downloads about 260 MB.
- `go run ./cmd/invariant synthesize examples/02-twophase-commit`: have headless Claude Code rebuild the model and code from the ratified statements, then gate the result. It uses the owner's Claude account, so only run it when asked.
- `go run ./cmd/invariant watch -repo gitdek/invariant -app-id 5079269`: the factory, acting as its App's bot, `invariant-code-factory[bot]`. The App's key is `~/.config/invariant/factory.pem`: never read it, print it or commit it. It turns issues into merged pull requests, posting as the owner and running agents on the owner's Claude account, so only run it when asked. `formalize` drafts statements for a request file the same way.
- `go run ./cmd/invariant scope -base origin/main` and `go run ./cmd/invariant ratification -repo gitdek/invariant PROJECT...`: the checks CI runs on factory pull requests.
- Never post `/invariant` commands on the owner's behalf. Choosing forks and ratifying are the owner's decisions.
