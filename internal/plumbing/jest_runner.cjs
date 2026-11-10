'use strict';
const common = require('./node_runner_common.cjs');
const childProcess = require('node:child_process');
const path = require('node:path');
const {isMainThread} = require('node:worker_threads');
let input;
let worker, observation, proposed, hookFailed = false;
const Base = common.requireLocked('jest-environment-node').TestEnvironment;

function fullName(test) {
  const names = [test.name];
  for (let p = test.parent; p?.parent; p = p.parent) names.unshift(p.name);
  return names.join(' ');
}
function testsIn(suite, disabled = false) {
  disabled ||= suite.mode === 'skip' || suite.mode === 'todo';
  return suite.children.flatMap(child => child.type === 'describeBlock' ? testsIn(child, disabled) :
    [{title: fullName(child), disabled: disabled || child.mode === 'skip' || child.mode === 'todo' || child.failing}]);
}

// Circus calls this environment outside the test's module sandbox. Its event
// callbacks distinguish actual function execution from hooks and result reports.
module.exports = class InvariantJestEnvironment extends Base {
  constructor(config, context) {
    super(config, context);
    input = JSON.parse(process.env.INVARIANT_NODE_WORKER);
    if (!isMainThread || process.pid === input.ControllerPID) throw new Error('Jest test execution must use a separate process');
    const endpoint = process.env.INVARIANT_NODE_ENDPOINT;
    delete process.env.INVARIANT_NODE_WORKER;
    delete process.env.INVARIANT_NODE_ENDPOINT;
    proposed = input.Proposed;
    worker = common.open(input, 'worker', endpoint);
    observation = common.observer(worker, proposed);
  }
  async handleTestEvent(event, state) {
    if (event.name === 'run_start') observation.collection(testsIn(state.rootDescribeBlock));
    if (event.name === 'hook_failure' || event.name === 'error') hookFailed = true;
    const selected = event.test && fullName(event.test) === input.Binding.Selector;
    if (selected && event.name === 'test_fn_start') observation.start();
    if (selected && event.name === 'test_fn_success') observation.end();
    if (selected && event.name === 'test_fn_failure') observation.end(event.error);
    if (event.name === 'run_finish') {
      observation.finish(!hookFailed && !state.unhandledErrors.length);
    }
  }
  async teardown() {
    try { await super.teardown(); }
    finally { worker?.close(); }
  }
  // The controller names this file as the TypeScript transformer too, wrapping
  // locked ts-jest. Jest's worker keeps only an exec error's code, message and
  // stack, so ts-jest's structured diagnostic codes travel in code.
  static createTransformer(options) {
    const tsJest = common.requireLocked('ts-jest');
    const inner = (tsJest.default ?? tsJest).createTransformer(options);
    const tagged = error => {
      if (Array.isArray(error?.diagnosticCodes)) error.code = [...error.diagnosticCodes];
      return error;
    };
    const wrapped = {canInstrument: inner.canInstrument};
    for (const name of ['getCacheKey', 'getCacheKeyAsync']) {
      if (typeof inner[name] === 'function') wrapped[name] = (...args) => inner[name](...args);
    }
    if (typeof inner.process === 'function') {
      wrapped.process = (...args) => { try { return inner.process(...args); } catch (error) { throw tagged(error); } };
    }
    if (typeof inner.processAsync === 'function') {
      wrapped.processAsync = async (...args) => { try { return await inner.processAsync(...args); } catch (error) { throw tagged(error); } };
    }
    return wrapped;
  }
};

// jest-worker replaces a worker that exits nonzero and resends its request, so
// a candidate that crashes once before collection could hide behind the
// replacement's complete lifecycle. The controller watches every worker it
// forks: exactly one, which must exit normally at shutdown without a kill.
function watchWorkers() {
  const workers = [];
  const fork = childProcess.fork;
  childProcess.fork = function (...args) {
    const child = fork.apply(this, args);
    const watched = {exited: false, code: null, signal: null, killed: false};
    workers.push(watched);
    const kill = child.kill;
    child.kill = function (...signal) {
      watched.killed = true;
      return kill.apply(this, signal);
    };
    child.once('exit', (code, signal) => Object.assign(watched, {exited: true, code, signal}));
    return child;
  };
  return () => {
    childProcess.fork = fork;
    if (workers.length !== 1 || workers.some(w => !w.exited || w.code !== 0 || w.signal !== null || w.killed)) {
      throw new Error('Jest worker died, restarted or was killed: ' + JSON.stringify(workers));
    }
  };
}

async function run() {
  input = JSON.parse(process.argv[2]);
  const endpoint = process.env.INVARIANT_NODE_EVENTS;
  delete process.env.INVARIANT_NODE_EVENTS;
  const session = common.open(input, 'parent', endpoint);
  session.emit('begin');
  let code = 1;
  try {
    const policy = common.options(input);
    const config = await common.config('jest', policy.config);
    common.onlyKeys(config, ['testEnvironment', 'transform', 'testMatch', 'testRegex', 'moduleFileExtensions', 'testTimeout', 'clearMocks', 'resetMocks', 'restoreMocks'], 'Jest configuration');
    if (config.testEnvironment && config.testEnvironment !== 'node') throw new Error('acceptance requires the prepared Node environment');
    const transform = {};
    let compiler, typeChecked = false;
    for (const [pattern, declared] of Object.entries(config.transform || {})) {
      const values = Array.isArray(declared) ? declared : [declared];
      if (values[0] !== 'ts-jest' || values.length > 2) throw new Error('Jest transformer must be locked ts-jest');
      const options = values[1] || {};
      common.onlyKeys(options, ['tsconfig', 'diagnostics', 'isolatedModules'], 'ts-jest');
      if (options.tsconfig && (typeof options.tsconfig !== 'object' || Array.isArray(options.tsconfig))) throw new Error('ts-jest requires inline compiler settings');
      if (options.tsconfig?.plugins?.length) throw new Error('candidate TypeScript plugins are not acceptance observers');
      common.resolve('typescript');
      common.resolve('ts-jest');
      transform[pattern] = [__filename, options];
      if (compiler === undefined && new RegExp(pattern).test(common.regular(input.File))) {
        compiler = options.tsconfig || {};
        // Only a type check refuses the selected file before any candidate code
        // runs, so only then can its transform failure be attributed.
        typeChecked = options.isolatedModules !== true && (options.diagnostics === undefined || options.diagnostics === true);
      }
    }
    proposed = common.analyze(input, {compiler}).proposed;
    process.env.INVARIANT_NODE_WORKER = JSON.stringify({...input, Proposed: proposed, ControllerPID: process.pid});
    process.env.INVARIANT_NODE_ENDPOINT = session.endpoint;
    const {runCLI} = common.requireLocked('jest');
    const effective = {...config, rootDir: common.root, transform, testEnvironment: __filename,
      runner: path.join(__dirname, 'jest_scheduler.cjs'), workerThreads: false,
      testRunner: common.resolve('jest-circus/runner'), reporters: [], cache: false,
      testNamePattern: policy.testNamePattern};
    if (policy.testTimeout) effective.testTimeout = policy.testTimeout;
    const settled = watchWorkers();
    const {results} = await runCLI({_: [common.regular(input.File)], $0: 'jest', maxWorkers: 1,
      runTestsByPath: true, watch: false, watchAll: false, listTests: false, config: JSON.stringify(effective)}, [common.root]);
    settled();
    if (results.testResults.length !== 1 || results.testResults[0].testFilePath !== common.regular(input.File) || results.wasInterrupted) {
      throw new Error('Jest did not complete the exact file');
    }
    const result = results.testResults[0];
    if (result.testExecError) {
      const error = result.testExecError;
      if (!proposed || !typeChecked || !Array.isArray(error.code) ||
          error.code.length !== 1 || error.code[0] !== proposed.code) {
        throw new Error('Jest transform/import failure lacks structured selected API attribution');
      }
      session.emit('compile', {IDs: [input.Binding.Selector], Outcome: 'attributed', Reason: proposed.reason});
    } else {
      const matches = result.testResults.filter(t => t.fullName === input.Binding.Selector);
      if (matches.length !== 1 || !['passed', 'failed'].includes(matches[0].status) ||
          result.testResults.some(t => t.fullName !== input.Binding.Selector && ['passed', 'failed'].includes(t.status))) {
        throw new Error('Jest result differs from exact executed selection');
      }
      if (results.success !== (matches[0].status === 'passed')) {
        throw new Error('Jest result conflicts with actual callback');
      }
      code = results.success ? 0 : 1;
    }
    session.recheck();
    session.emit('session_end', {Code: code, Outcome: code ? 'failed' : 'passed'});
    session.emit('finish', {Code: code});
  } catch (error) {
    if (error.diagnostics) {
      session.diagnostic(error.diagnostics);
      process.stderr.write(error.diagnostics);
    }
    session.emit('refused', {Reason: String(error.message).slice(0, 700)});
  } finally {
    delete process.env.INVARIANT_NODE_WORKER;
    delete process.env.INVARIANT_NODE_ENDPOINT;
    session.close();
    // Do not force an exit: live handles or workers must veto normal shutdown.
    process.exitCode = code;
  }
}

if (require.main === module) run().catch(error => { console.error(error); process.exitCode = 1; });
