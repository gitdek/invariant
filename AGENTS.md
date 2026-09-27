# Working in this repo

These are the rules for any coding agent working here, whatever the agent. `CLAUDE.md` imports this file, and Codex reads it directly.

- `SPEC.md` is the current state and the only document to build against. `decisions/log.md` is the record it's rolled up from.
- A decision that isn't written down didn't happen. When a choice is made during a session, add it to `decisions/log.md` in the same change. A two-way door gets one line. A one-way door also gets a full record in `decisions/D-NNNN-slug.md`: the options considered, why, and what would reopen it.
- If you make a two-way-door call while carrying out ratified work, log it as `decided`, with `agent` as who. Anything the owner would want a say in, and every one-way door, goes in as `proposed` until @gitdek ratifies it.
- Don't build against anything listed under "Undecided" in `SPEC.md`. Ask instead.
- Never edit a pinned statement or a `ratified.lock` without a ratified decision that changes it.
- Invariant is agent-agnostic. The product, its docs and its pages name roles, such as "a coding agent", never the agent writing them.
- Commits and pull requests are authored by @gitdek alone. Don't add `Co-Authored-By` lines or any other attribution to commit messages or PR descriptions.

## Commands

- `go test ./...`: unit tests. Each example is its own Go module, so the root module doesn't include it.
- `go test -tags integration ./internal/verify/`: the gate against the real verifiers, including the ways it must fail. Needs Docker.
- `go run ./cmd/invariant verify examples/02-twophase-commit`: run the gate and print the receipt.
- `go run ./cmd/invariant verify examples/02-twophase-commit-py-proved`: the same, with Nagini proving the Python core. The first run builds the Nagini image for linux/amd64, which downloads about 260 MB.
- `go run ./cmd/invariant synthesize examples/02-twophase-commit`: have a headless coding agent rebuild the model and code from the ratified statements, then gate the result. It runs on the owner's agent account, so only run it when asked.
- `go run ./cmd/invariant watch -repo gitdek/invariant -app-id 5079269`: the factory, acting as its App's bot, `invariant-code-factory[bot]`. The App's key is `~/.config/invariant/factory.pem`: never read it, print it or commit it. It turns issues into merged pull requests and runs its agents on the owner's account, so only run it when asked. `formalize` drafts statements for a request file the same way.
- `go run ./cmd/invariant dashboard -repo gitdek/invariant`: the live dashboard on http://127.0.0.1:8484. The public page only reads. `cloudflared tunnel run --url http://127.0.0.1:8484 invariant` publishes it at invariant.puglisij.com. The tunnel's credentials are in `~/.cloudflared`: never read them, print them or commit them.
  - With `-access-team puglisij.cloudflareaccess.com -access-aud <tag> -access-email <email>`, @gitdek can post commands from `/act`, behind Cloudflare Access.
  - With `-agent-addr 127.0.0.1:8485`, a coding agent can post through the same narrow check, with the token in the dashboard's cache directory. Use it only for commands the owner explicitly asked for. Choosing forks and ratifying remain his decisions.
- `go run ./cmd/invariant scope -base origin/main` and `go run ./cmd/invariant ratification -repo gitdek/invariant PROJECT...`: the checks CI runs on factory pull requests.
- Choosing forks and ratifying are the owner's decisions, so never post `/invariant` commands on your own initiative. Post one on the owner's behalf only when they explicitly ask for that exact command, and say that you did.
