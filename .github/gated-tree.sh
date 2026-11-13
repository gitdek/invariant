#!/usr/bin/env bash
# Says whether a pull request's gate has already passed on a tree, as a line
# for $GITHUB_OUTPUT (#874):
#
#   GH_TOKEN=... GITHUB_REPOSITORY=owner/name .github/gated-tree.sh TREE
#   tested=true
#
# When a pull request's gate passes, it uploads an artifact named gated-TREE,
# for the tree it tested: the pull request merged into its base. A push to
# main with the same tree holds exactly the files that gate checked, so its
# checks and tests needn't run again. A tree's hash names its files and
# nothing else, so no other tree answers to it.
#
# Only a run of this workflow on a pull request from this repository counts,
# and only one that passed: a fork's run uploads to the same store. Whatever
# can't be asked or read says tested=false, and then everything runs.
set -uo pipefail
export LC_ALL=C

tree=${1-}
tested=false
if [[ $tree =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ]] && [ -n "${GITHUB_REPOSITORY-}" ]; then
  runs=$(gh api "repos/$GITHUB_REPOSITORY/actions/artifacts?name=gated-$tree&per_page=100" \
    --jq '.artifacts[] | select(.expired == false and .workflow_run.repository_id == .workflow_run.head_repository_id) | .workflow_run.id' 2> /dev/null) || runs=""
  for run in $runs; do
    [[ $run =~ ^[0-9]+$ ]] || continue
    passed=$(gh api "repos/$GITHUB_REPOSITORY/actions/runs/$run" \
      --jq 'select(.event == "pull_request" and .conclusion == "success" and .path == ".github/workflows/gate.yml") | .id' 2> /dev/null) || passed=""
    if [ "$passed" = "$run" ]; then
      tested=true
      break
    fi
  done
fi
echo "tested=$tested"
