#!/usr/bin/env bash
# Says which of the opt-in real suites a change can affect, from its paths,
# one a line on stdin, as lines for $GITHUB_OUTPUT (#416):
#
#   .github/real-suites.sh < paths
#   python=true
#   tlaps=false
#
# python is every Python test with real Docker, TLC, Nagini and wheels: the
# application's execution, gate and proof, and its dependencies. tlaps is
# the pinned tlapm's. A path that matches neither, such as the factory's or
# the docs', can't change what they check. The rest of the verifier, its
# toolchain, receipts, manifests and this workflow can change both.
set -euo pipefail
export LC_ALL=C

python=false tlaps=false
while IFS= read -r f || [ -n "$f" ]; do
  case "$f" in
    internal/verify/tlaps*|internal/verify/testdata/examples/05-connection-pool/*|examples/05-connection-pool/*)
      tlaps=true ;;
    internal/verify/python*|internal/verify/*.py|internal/verify/declared_refusal*)
      python=true ;;
    internal/verify/*|internal/conformance/*|internal/project/*|internal/receipt/*|internal/toolchain/*|internal/tla/*|cmd/invariant/*|go.mod|go.sum|.github/*)
      python=true tlaps=true ;;
  esac
done
echo "python=$python"
echo "tlaps=$tlaps"
