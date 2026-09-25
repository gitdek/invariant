# Working in this repo

- `SPEC.md` is the current state and the only document to build against. `decisions/log.md` is the record it's rolled up from.
- A decision that isn't written down didn't happen. When a choice is made during a session, add it to `decisions/log.md` in the same change. A two-way door gets one line. A one-way door also gets a full record in `decisions/D-NNNN-slug.md`: the options considered, why, and what would reopen it.
- Only ratified decisions go into `SPEC.md`. Your own proposals go into the log with status `proposed` until @gitdek ratifies them.
- Don't build against anything listed under "Undecided" in `SPEC.md`. Ask instead.
- Never edit a pinned statement without a ratified decision that changes it.
- Commits and pull requests are authored by @gitdek alone. Don't add `Co-Authored-By` lines or any other attribution to commit messages or PR descriptions.
