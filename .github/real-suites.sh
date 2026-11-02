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
# the pinned tlapm's. A path that matches neither, such as the factory's, or
# any doc, can't change what they check. Every other package their tests
# import, which internal/ci's test reads from Go's import graph, can change
# both, and so can the module and this workflow (#436).
set -euo pipefail
export LC_ALL=C

python=false tlaps=false
while IFS= read -r f || [ -n "$f" ]; do
  case "$f" in
    *.md)
      ;;
    internal/verify/tlaps*|internal/verify/testdata/examples/05-connection-pool/*|examples/05-connection-pool/*)
      tlaps=true ;;
    internal/verify/python*|internal/verify/*.py|internal/verify/declared_refusal*|cmd/invariant/*)
      python=true ;;
    internal/verify/*|internal/conformance/*|internal/decisions/*|internal/gobra/*|internal/mcp/*|internal/project/*|internal/receipt/*|internal/tla/*|internal/tlc/*|internal/toolchain/*|factory/decision-state/*|factory/ids/*|factory/journal-merges/*|factory/store-journal/*|go.mod|go.sum|.github/*)
      python=true tlaps=true ;;
  esac
done
echo "python=$python"
echo "tlaps=$tlaps"
