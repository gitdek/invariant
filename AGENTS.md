# Working in this repo

These are the rules for any coding agent working here, whatever the agent. `CLAUDE.md` imports this file, and Codex reads it directly.

- `SPEC.md` is the current state and the only document to build against. The decision journal, `decisions/journal/`, is the record it's rolled up from, and `decisions/log.md` is the journal's table.
- A decision that isn't written down didn't happen. When a choice is made during a session, record it in the same change with `invariant decisions decide`, which writes its journal file and its row in `decisions/log.md`. Never edit a journal file or the log's table by hand: CI rebuilds them and fails a change that doesn't match. A two-way door gets one line. A one-way door also gets a full record in `decisions/D-NNNN-slug.md`, named with `-record slug`: the options considered, why, and what would reopen it.
- If you make a two-way-door call while carrying out ratified work, record it as `decided`, with `agent` as who. Anything the owner would want a say in, and every one-way door, goes in as `proposed` until @gitdek ratifies it. Record his ratification with `invariant decisions ratify -by @gitdek` only when he asks for it.
- Before changing what a decision covers, ask what rests on it: `invariant decisions dependents D-NNNN`, or the `dependents` tool.
- Don't build against anything listed under "Undecided" in `SPEC.md`. Ask instead.
- Never edit a pinned statement or a `ratified.lock` without a ratified decision that changes it.
- Invariant is agent-agnostic. The product, its docs and its pages name roles, such as "a coding agent", never the agent writing them.
- Commits and pull requests are authored by @gitdek alone. Don't add `Co-Authored-By` lines or any other attribution to commit messages or PR descriptions.

## Commands

- `go test ./...`: unit tests. Each example is its own Go module, so the root module doesn't include it.
- `go test -tags integration ./internal/verify/ ./internal/conformance/`: the gate against the real verifiers, including the ways it must fail. Needs Docker.
- `go run ./cmd/invariant verify examples/02-twophase-commit`: run the gate and print the receipt.
- `go run ./cmd/invariant decisions decide -door two-way -status decided "What was decided, and why."`: record a decision, and print its ID. `decisions show`, `dependents`, `implementers`, `grounds`, `search` and `sql` ask the graph, and `decisions check` is what CI runs. The store lives on this machine, and `decisions rebuild` refreshes it from a checkout.
- `go run ./cmd/invariant mcp -decisions -write`: the same graph as MCP tools for a coding agent: `decision`, `dependents`, `implementers`, `grounds`, `search_decisions` and `query_decisions`, and with `-write`, `decide` and `link_decision`. They never ratify. `.mcp.json` starts it for agents that read that file.
- `go run ./cmd/invariant verify examples/02-twophase-commit-py-proved`: the same, with Nagini proving the Python core. The first run builds the Nagini image for linux/amd64, which downloads about 260 MB.
- `go run ./cmd/invariant synthesize examples/02-twophase-commit`: have a headless coding agent rebuild the model and code from the ratified statements, then gate the result. It runs on the owner's agent account, so only run it when asked.
- `go run ./cmd/invariant watch -repo gitdek/invariant -app-id 5079269`: the factory, acting as its App's bot, `invariant-code-factory[bot]`. The App's key is `~/.config/invariant/factory.pem`: never read it, print it or commit it. It turns issues into merged pull requests and runs its agents on the owner's account, so only run it when asked. `formalize` drafts statements for a request file the same way.
- `go run ./cmd/invariant dashboard -repo gitdek/invariant`: the live dashboard on http://127.0.0.1:8484. The public page only reads. `cloudflared tunnel run --url http://127.0.0.1:8484 invariant` publishes it at invariant.puglisij.com. The tunnel's credentials are in `~/.cloudflared`: never read them, print them or commit them.
  - With `-access-team puglisij.cloudflareaccess.com -access-aud <tag> -access-email <email>`, @gitdek can post commands from `/act`, behind Cloudflare Access.
  - With `-agent-addr 127.0.0.1:8485`, a coding agent can post through the same narrow check, with the token in the dashboard's cache directory. Use it only for commands the owner explicitly asked for. Choosing forks and ratifying remain his decisions.
- `go run ./cmd/invariant scope -base origin/main` and `go run ./cmd/invariant ratification -repo gitdek/invariant PROJECT...`: the checks CI runs on factory pull requests.
- Labeling an issue `invariant`, or commenting `/invariant solve` on it, hands its text to the factory's agents. Anyone can write an issue, so never do either for an issue someone else wrote unless the owner asks for that issue.
- Choosing forks and ratifying are the owner's decisions, so never post `/invariant` commands on your own initiative. Post one on the owner's behalf only when they explicitly ask for that exact command, and say that you did.
