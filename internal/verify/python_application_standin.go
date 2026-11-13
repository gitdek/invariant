package verify

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime/debug"
)

var pythonApplicationGitRevision = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// The remedy uses the running binary's identity and embedded bytes. A source
// checkout need not exist on the machine running verification (#908).
func pythonApplicationStandInRemedy(root string, hashes map[string]string) string {
	info, _ := debug.ReadBuildInfo()
	return pythonApplicationStandInInstructions(root, hashes, info)
}

func pythonApplicationStandInInstructions(root string, hashes map[string]string, info *debug.BuildInfo) string {
	initHash, contractsHash := hashes["nagini_contracts/__init__.py"], hashes["nagini_contracts/contracts.py"]
	text := fmt.Sprintf(`ship Invariant's runtime stand-in at the application's root, exactly as it is, and no other nagini_contracts anywhere else in the application.
Destination: %s
Expected embedded files (SHA-256):
  __init__.py %s
  contracts.py %s
A PyPI distribution or a stand-in injected into staging does not satisfy shipping. Verification does not fetch, install or change application files.
Local verification must use the same Invariant revision pinned by the application's CI. Check that pin yourself; this command has not inspected CI.
`, filepath.Join(root, "nagini_contracts"), initHash, contractsHash)
	revision, identity := pythonApplicationStandInBuild(info)
	text += "Build identity: " + identity + ".\n"
	if revision == "" {
		return text + "Build and run a clean checkout of the revision pinned by the application's CI before obtaining copy instructions. The hashes above describe this binary's actual embedded bytes."
	}
	text += fmt.Sprintf(`Source: https://github.com/gitdek/invariant/tree/%s/internal/verify/naginiruntime/nagini_contracts
If this revision differs from the CI pin, build and run the CI-pinned revision first.
Obtain an Invariant checkout yourself, for example by cloning https://github.com/gitdek/invariant.git into your chosen INVARIANT_CHECKOUT directory, then run git -C "$INVARIANT_CHECKOUT" checkout --detach %s.
Set INVARIANT_CHECKOUT to that checkout and APPLICATION_ROOT to the original application's root shown above. With both directories explicitly supplied, run this offline recipe (requires git and python3). It checks the revision and both source hashes before creating the destination, and refuses any existing destination. Review and explicitly move or remove an existing package before replacing it during an upgrade.
`, revision, revision)
	text += "```sh\n" + fmt.Sprintf(`set -eu
: "${INVARIANT_CHECKOUT:?Set INVARIANT_CHECKOUT to the pinned checkout}"
: "${APPLICATION_ROOT:?Set APPLICATION_ROOT to the original application root}"
if [ ! -d "$INVARIANT_CHECKOUT" ] || [ ! -d "$APPLICATION_ROOT" ]; then
    echo "Both directories must already exist." >&2
    exit 1
fi
if [ "$(git -C "$INVARIANT_CHECKOUT" rev-parse --verify HEAD)" != '%s' ]; then
    echo "Invariant checkout is not at the required revision." >&2
    exit 1
fi
python3 -I -S -B - "$INVARIANT_CHECKOUT" "$APPLICATION_ROOT" <<'PY'
import hashlib
import pathlib
import sys

source = pathlib.Path(sys.argv[1]) / 'internal/verify/naginiruntime/nagini_contracts'
destination = pathlib.Path(sys.argv[2]) / 'nagini_contracts'
expected = {
    '__init__.py': '%s',
    'contracts.py': '%s',
}
files = {}
for name, digest in expected.items():
    data = (source / name).read_bytes()
    if 'sha256:' + hashlib.sha256(data).hexdigest() != digest:
        raise SystemExit('Source hash mismatch: ' + str(source / name))
    files[name] = data
try:
    destination.mkdir()
except FileExistsError:
    raise SystemExit('Refusing existing destination: ' + str(destination))
for name, data in files.items():
    with (destination / name).open('xb') as output:
        output.write(data)
PY
`, revision, initHash, contractsHash) + "```"
	return text
}

// Only a full Git revision explicitly marked unmodified identifies source
// from which the embedded package can be reproduced.
func pythonApplicationStandInBuild(info *debug.BuildInfo) (revision, identity string) {
	var vcs, modified string
	if info != nil {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs":
				vcs = s.Value
			case "vcs.revision":
				revision = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
	}
	switch {
	case modified == "true":
		return "", "modified (Go build metadata reports vcs.modified=true)"
	case vcs == "" && revision == "" && modified == "":
		return "", "unknown (Go build metadata has no VCS identity)"
	case vcs != "git" || !pythonApplicationGitRevision.MatchString(revision) || modified != "false":
		return "", "incomplete or unsupported (requires Git, a full revision and vcs.modified=false in Go build metadata)"
	default:
		return revision, "clean Git revision " + revision
	}
}
