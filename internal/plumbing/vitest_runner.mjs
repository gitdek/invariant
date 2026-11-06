import {fileURLToPath} from 'node:url';
import {isMainThread} from 'node:worker_threads';
import common from './node_runner_common.cjs';

const isMain = process.argv[1] === fileURLToPath(import.meta.url);
let worker, observation, input, proposed, fileFinished = false;
// This file is also the controller, which only starts Vitest. Vitest's runner
// API reads a worker's state as it loads, so only a worker imports it.
const {VitestTestRunner} = isMain ? {VitestTestRunner: class {}} : await common.importLocked('vitest/runners');
const {getFn} = isMain ? {} : await common.importLocked('@vitest/runner');

function title(task) {
  const names = [task.name];
  for (let p = task.suite; p && !p.filepath; p = p.suite) names.unshift(p.name);
  return names.join(' ');
}
function testsIn(suite, disabled = false) {
  disabled ||= suite.mode === 'skip' || suite.mode === 'todo';
  return (suite.tasks || []).flatMap(task => task.type === 'suite' || task.type === 'file' ? testsIn(task, disabled) :
    [{title: title(task), disabled: disabled || task.mode === 'skip' || task.mode === 'todo' || task.fails}]);
}
function hooksPassed(suite) {
  if (suite.result?.errors?.length) return false;
  if (Object.values(suite.result?.hooks || {}).some(status => status !== 'pass')) return false;
  return (suite.tasks || []).every(task => task.type === 'test' ?
    Object.values(task.result?.hooks || {}).every(status => status === 'pass') : hooksPassed(task));
}

// The framework invokes runTask only after setup hooks, then awaits teardown.
// getFn is the prepared runner's actual selected callback, not a result report.
export default class InvariantVitestRunner extends VitestTestRunner {
  constructor(config) {
    super(config);
    input = JSON.parse(process.env.INVARIANT_NODE_WORKER);
    if (!isMainThread || process.pid === input.ControllerPID) throw new Error('Vitest test execution must use a separate process');
    const endpoint = process.env.INVARIANT_NODE_ENDPOINT;
    delete process.env.INVARIANT_NODE_WORKER;
    delete process.env.INVARIANT_NODE_ENDPOINT;
    proposed = input.Proposed;
    worker = common.open(input, 'worker', endpoint);
    observation = common.observer(worker, proposed);
  }
  async onCollected(files) {
    if (super.onCollected) await super.onCollected(files);
    if (files.length !== 1 || files[0].filepath !== common.regular(input.File)) throw new Error('Vitest collected another file');
    observation.collection(testsIn(files[0]));
  }
  async runTask(task) {
    if (title(task) !== input.Binding.Selector || task.fails) throw new Error('Vitest attempted another or expected-failure test');
    const callback = getFn(task);
    if (typeof callback !== 'function') throw new Error('Vitest selected task has no callback');
    observation.start();
    try { await callback(); }
    catch (error) { observation.end(error); throw error; }
    observation.end();
  }
  async onAfterRunSuite(suite) {
    if (super.onAfterRunSuite) await super.onAfterRunSuite(suite);
    if (suite.filepath) {
      if (fileFinished) throw new Error('duplicate Vitest suite completion');
      fileFinished = true;
      observation.finish(hooksPassed(suite));
      worker.close();
    }
  }
}

async function run() {
  const selected = JSON.parse(process.argv[2]);
  const endpoint = process.env.INVARIANT_NODE_EVENTS;
  delete process.env.INVARIANT_NODE_EVENTS;
  const session = common.open(selected, 'parent', endpoint);
  session.emit('begin');
  let ctx, code = 1;
  try {
    const policy = common.options(selected);
    const config = await common.config('vitest', policy.config);
    common.onlyKeys(config, ['test', 'esbuild'], 'Vitest configuration');
    const test = config.test || {};
    common.onlyKeys(test, ['environment', 'globals', 'testTimeout', 'hookTimeout', 'include', 'exclude'], 'Vitest test configuration');
    if (test.environment && test.environment !== 'node') throw new Error('acceptance requires the prepared Node environment');
    if (config.esbuild && typeof config.esbuild !== 'object') throw new Error('unsupported esbuild configuration');
    if (config.esbuild) common.onlyKeys(config.esbuild, ['jsx', 'jsxFactory', 'jsxFragment', 'target'], 'esbuild');
    common.resolve('esbuild');
    const analysis = common.analyze(selected, {globals: test.globals === true});
    process.env.INVARIANT_NODE_WORKER = JSON.stringify({...selected, Proposed: analysis.proposed, ControllerPID: process.pid});
    process.env.INVARIANT_NODE_ENDPOINT = session.endpoint;
    const {createVitest} = await common.importLocked('vitest/node');
    let completed = false;
    const reporter = {
      onFinished(files, errors) {
        if (completed || errors?.length || files.length !== 1 || files[0].filepath !== common.regular(selected.File)) throw new Error('Vitest session did not finish uniquely');
        const selectedTasks = [];
        const walk = suite => { for (const task of suite.tasks || []) task.type === 'test' ? selectedTasks.push(task) : walk(task); };
        walk(files[0]);
        const matches = selectedTasks.filter(t => title(t) === selected.Binding.Selector);
        if (matches.length !== 1 || !['pass', 'fail'].includes(matches[0].result?.state) ||
            selectedTasks.some(t => title(t) !== selected.Binding.Selector && ['pass', 'fail'].includes(t.result?.state))) throw new Error('Vitest final results differ from exact selection');
        if (!hooksPassed(files[0])) throw new Error('Vitest hook or suite error');
        code = matches[0].result.state === 'pass' ? 0 : 1;
        completed = true;
      },
    };
    ctx = await createVitest('test', {root: common.root, config: false, ...test,
      include: [selected.File], watch: false, passWithNoTests: false, testNamePattern: policy.testNamePattern,
      ...(policy.testTimeout ? {testTimeout: policy.testTimeout} : {}),
      pool: 'forks', poolOptions: {forks: {singleFork: true}}, fileParallelism: false,
      isolate: true, retry: 0, runner: fileURLToPath(import.meta.url), reporters: [reporter]}, {esbuild: config.esbuild});
    await ctx.start([common.regular(selected.File)]);
    await ctx.close();
    ctx = null;
    if (!completed) throw new Error('Vitest did not complete its session');
    session.recheck();
    session.emit('session_end', {Code: code, Outcome: code ? 'failed' : 'passed'});
    session.emit('finish', {Code: code});
  } catch (error) {
    session.emit('refused', {Reason: String(error.message).slice(0, 700)});
  } finally {
    if (ctx) await ctx.close();
    delete process.env.INVARIANT_NODE_WORKER;
    delete process.env.INVARIANT_NODE_ENDPOINT;
    session.close();
    process.exitCode = code;
  }
}

if (isMain) run().catch(error => { console.error(error); process.exitCode = 1; });
