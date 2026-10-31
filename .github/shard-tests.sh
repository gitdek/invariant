#!/usr/bin/env bash
# Runs one shard of Go packages' tests, so a package whose tests take most of
# a job can run on several runners at once (#345).
#
#   .github/shard-tests.sh K/N [-tag TAG] [-run REGEXP] [GO TEST FLAGS...] -- PACKAGE...
#
# The packages' top-level tests are listed, sorted by package and name, and
# shard K runs every Nth of them, from the Kth, so every test runs in exactly
# one shard, and a new test lands in one with no change to the workflow.
# -run narrows the list first. With -tag, only the tests that build tag adds
# are listed, and they're built with it, since a run without the tag runs
# the rest. Each package's share runs at once beside the others'. A package
# that doesn't build fails the shard, rather than listing no tests.
set -euo pipefail
export LC_ALL=C

usage="usage: $0 K/N [-tag TAG] [-run REGEXP] [GO TEST FLAGS...] -- PACKAGE..."
shard=${1-}
[[ $shard =~ ^([1-9][0-9]*)/([1-9][0-9]*)$ ]] && [ "${BASH_REMATCH[1]}" -le "${BASH_REMATCH[2]}" ] || { echo "$usage" >&2; exit 2; }
k=${BASH_REMATCH[1]} n=${BASH_REMATCH[2]}
shift
tag="" run='.*' flags=()
while [ $# -gt 0 ] && [ "$1" != "--" ]; do
  case $1 in
    -tag) tag=$2; shift 2 ;;
    -run) run=$2; shift 2 ;;
    *) flags+=("$1"); shift ;;
  esac
done
[ "${1-}" = "--" ] && [ $# -gt 1 ] || { echo "$usage" >&2; exit 2; }
shift

# tests PACKAGE [GO TEST FLAGS...] prints the package's tests that -run
# matches, a line each, as "PACKAGE TEST".
tests() {
  local pkg=$1 out
  shift
  out=$(go test "$@" -list "$run" "$pkg") || { printf '%s\n' "$out" >&2; return 1; }
  printf '%s\n' "$out" | { grep -E '^(Test|Fuzz|Example)' || true; } | sort | sed "s|^|$pkg |"
}

all=""
for pkg in "$@"; do
  plain=$(tests "$pkg")
  if [ -n "$tag" ]; then
    tagged=$(tests "$pkg" -tags "$tag")
    plain=$(comm -13 <(printf '%s\n' "$plain") <(printf '%s\n' "$tagged"))
  fi
  all+="$plain"$'\n'
done

mine=$(printf '%s' "$all" | awk -v n="$n" -v k="$k" 'NF && (i++ % n) == k - 1')
echo "Shard $shard runs $(printf '%s\n' "$mine" | grep -c . || true) of $(printf '%s' "$all" | grep -c . || true) tests."

pids=()
for pkg in "$@"; do
  names=$(printf '%s\n' "$mine" | awk -v p="$pkg" '$1 == p { print $2 }' | paste -sd '|' -)
  [ -n "$names" ] || continue
  go test ${tag:+-tags "$tag"} ${flags[@]+"${flags[@]}"} -run "^($names)\$" "$pkg" &
  pids+=($!)
done
status=0
for pid in ${pids[@]+"${pids[@]}"}; do
  wait "$pid" || status=1
done
exit $status
