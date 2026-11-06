package plumbing

import (
	"context"
	_ "embed"

	"github.com/gitdek/invariant/internal/repository"
)

// JestRepositoryAcceptanceAdapter selects one file and literal full title.
type JestRepositoryAcceptanceAdapter struct{}

func (JestRepositoryAcceptanceAdapter) Select(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
	return selectNodeAcceptance(ctx, part, test, "jest")
}

//go:embed jest_runner.cjs
var jestRunner []byte

// Keep even a single test in a separate worker process. Jest normally optimizes
// one-file runs into the controller process, which owns terminal authentication.
const jestScheduler = `'use strict';
const {default: Runner} = require('./node_runner_common.cjs').requireLocked('jest-runner');
module.exports = class extends Runner {
  runTests(tests, watcher, options) {
    return super.runTests(tests, watcher, {...options, serial: false});
  }
};
`
