#!/bin/sh
# gobra.sh FILE... runs the gate's pinned Gobra on the core's FILEs, the way
# internal/gobra does, and says how long it took. A FILE may name only some
# members, by the lines they start on, as steps.go@26,64; give a file with
# @1 to have it type-checked and none of it verified. GOBRA_ARGS adds
# options, such as --assertTimeout 30000, to fail fast while working on a
# proof; the gate runs without them. Gobra's whole output goes to
# $GOBRA_OUT, by default a file in the temporary directory.
set -u
image=ghcr.io/viperproject/gobra@sha256:775879e8483561186291653d4c8a818c222eafd38f4b50a42d4b8873eebc9481
core=$(cd "$(dirname "$0")/../merges" && pwd)
out=${GOBRA_OUT:-${TMPDIR:-/tmp}/journal-merges-gobra.out}
args=""
for f in "$@"; do args="$args /work/$f"; done
start=$(date +%s)
docker run --rm --network none --platform linux/amd64 -v "$core":/work:ro "$image" --overflow ${GOBRA_ARGS:-} -i $args > "$out" 2>&1
code=$?
end=$(date +%s)
grep -E -A2 'Error at|Gobra found|Exception|Caused by' "$out" | head -80
echo "exit=$code seconds=$((end - start)) output=$out"
