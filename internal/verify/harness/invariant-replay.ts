// Gate-owned command replay. This module never runs the sampling harness.
import {readFileSync, openSync, writeFileSync, closeSync, mkdtempSync, unlinkSync, rmdirSync, fstatSync, readSync} from 'node:fs';
import {createHmac, randomBytes, timingSafeEqual} from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {tmpdir} from 'node:os';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';

type Value = {kind: string, atom?: string, elements?: Value[], entries?: {key: Value, value: Value}[]};
type State = Record<string, Value>;
type Binding = {target: any, method: string, args: any[], refusal?: {equals: null | boolean | number | string}};
export type ReplaySession = {
  read(): State | Promise<State>;
  bind(action: string, args: Value[]): Binding | Promise<Binding>;
  close(): void | Promise<void>;
};

const clone = (v: any): any => JSON.parse(JSON.stringify(v));
// Read-back equality here only detects mutations made while binding. The host
// checks typed semantic equality, exact prefixes and permitted alternatives.
function canonical(v: any): string {
  if (Array.isArray(v)) return '[' + v.map(canonical).join(',') + ']';
  if (v?.kind === 'integer') return 'integer:' + BigInt(v.atom).toString();
  if (['bool', 'string', 'model'].includes(v?.kind)) return JSON.stringify([v.kind, v.atom || '']);
  if (v?.kind === 'set') return 'set[' + [...new Set((v.elements || []).map(canonical))].sort().join(',') + ']';
  if (v?.kind === 'sequence') return 'sequence[' + (v.elements || []).map(canonical).join(',') + ']';
  if (v?.kind === 'record' || v?.kind === 'function') return v.kind + '[' + (v.entries || []).map(canonical).sort().join(',') + ']';
  if (v && typeof v === 'object') return '{' + Object.keys(v).sort().map(k => JSON.stringify(k)+':'+canonical(v[k])).join(',') + '}';
  return JSON.stringify(v);
}

async function replay() {
  // Consume the private bootstrap pipe before importing application code.
  // Seal the harness's own bytes, never records read from driver files.
  // The seal defends against records written or replayed outside this
  // harness. The driver runs in this process, so it can always reach the
  // seal, the channel and these records, as by patching what the harness
  // calls, which no in-process harness can rule out: a driver that does is
  // tampering with the gate, and review of the driver is the check against it.
  const key = readFileSync(0);
  if (key.length !== 32) throw new Error('missing replay authentication');
  const authentication = createHmac('sha256', key);
  key.fill(0);
  closeSync(0);
  const encode = JSON.stringify, write = writeFileSync;
  if (process.env.INVARIANT_REPLAY_ISOLATE === '1') {
    process.setgroups!([]); process.setgid!(65534); process.setuid!(65534);
  }
  const q = JSON.parse(readFileSync(process.env.INVARIANT_REPLAY_REQUEST!, 'utf8'));
  const driver = pathToFileURL(resolve('/src', process.env.DRIVER!)).href;
  let session: ReplaySession | undefined;
  let failure: unknown;
  let failed = false;
  const result: any = {version: 1, request_id: q.id, invocation_id: q.invocation_id, initial: null, calls: [], outcome: 'failed'};
  const observed: any = {request_id: q.id, invocation_id: q.invocation_id, initial: null, calls: [], started: false, completed: false, exit_code: 1};
  try {
    const module = await import(driver);
    if (typeof module.invariantReplay !== 'function') throw new Error('invariantReplay(initial) is required for current bug replay');
    session = await module.invariantReplay(clone(q.path.states[0]));
    if (!session || typeof session.close !== 'function' || typeof session.read !== 'function' || typeof session.bind !== 'function') throw new Error('invariantReplay must return read, bind and close');
    observed.started = true;
    result.initial = clone(await session.read());
    if (canonical(result.initial) !== canonical(q.path.states[0])) throw new Error('initial state differs from requested replay');
    for (let i = 0; i <= q.path.offending; i++) {
      const step = q.path.steps[i];
      const before = clone(await session.read());
      if (canonical(before) !== canonical(q.path.states[step.from])) throw new Error('replay prefix diverged before dispatch');
      const binding = await session.bind(step.action, clone(step.args || []));
      if (!binding || !binding.target || typeof binding.method !== 'string' || !Array.isArray(binding.args)) throw new Error('bind must name a real application method and argument list');
      const operation = binding.target[binding.method];
      if (typeof operation !== 'function') throw new Error('bound application method is not callable');
      if (canonical(before) !== canonical(clone(await session.read()))) throw new Error('bind changed application state');
      if (binding.refusal !== undefined && (!binding.refusal || Object.keys(binding.refusal).join() !== 'equals' || ![null, 'boolean', 'number', 'string'].includes(binding.refusal.equals === null ? null : typeof binding.refusal.equals))) throw new Error('refusal.equals must be a primitive literal');
      if (typeof binding.refusal?.equals === 'number' && !Number.isFinite(binding.refusal.equals)) throw new Error('refusal.equals must be finite');
      const call: any = {index: i, action: step.action, args: clone(step.args), before, after: null, dispatched: false, outcome: 'failed'};
      result.calls.push(call);
      // Invocation and its receiver belong to the harness, not to bind.
      call.dispatched = true;
      const returned = await Reflect.apply(operation, binding.target, binding.args);
      call.after = clone(await session.read());
      call.outcome = binding.refusal !== undefined && Object.is(returned, binding.refusal.equals) ? 'refused' : 'returned';
      if (i < q.path.offending && (call.outcome !== 'returned' || canonical(call.after) !== canonical(q.path.states[step.to]))) throw new Error('setup call diverged from requested replay');
    }
    result.outcome = result.calls.at(-1).outcome === 'refused' ? 'refused' : 'complete';
  } catch (err) {
    failed = true;
    failure = err;
  } finally {
    try {
      if (session && typeof session.close === 'function') await session.close();
      else throw new Error('replay session could not close');
      if (!failed) observed.completed = true;
    } catch (err) { if (!failed) { failed = true; failure = err; } }
    observed.initial = clone(result.initial);
    observed.calls = clone(result.calls);
    observed.exit_code = failed ? 1 : 0;
  }
  if (failed) throw failure;
  const payload = encode(result) + '\n' + encode(observed) + '\n';
  authentication.update(payload);
  write(3, authentication.digest('hex') + '\n' + payload);
  closeSync(3);
}

function supervise() {
  const key = randomBytes(32);
  // An unlinked descriptor transports completion without a driver-writable
  // pathname or a pipe that can deadlock on a large replay record.
  const dir = mkdtempSync(resolve(tmpdir(), 'invariant-replay-'));
  const channel = openSync(resolve(dir, 'completion'), 'wx+');
  unlinkSync(resolve(dir, 'completion'));
  rmdirSync(dir);
  try {
    const child = spawnSync(process.execPath, [...process.execArgv, process.argv[1]], {
      env: {...process.env, INVARIANT_REPLAY_WORKER: '1'},
      input: key, stdio: ['pipe', 'inherit', 'inherit', channel]
    });
    if (child.error || child.status !== 0) throw new Error('replay worker failed: ' + (child.error || child.signal || child.status));
    const sealed = Buffer.alloc(fstatSync(channel).size);
    let offset = 0;
    while (offset < sealed.length) {
      const n = readSync(channel, sealed, offset, sealed.length - offset, offset);
      if (!n) throw new Error('truncated replay authentication');
      offset += n;
    }
    const payload = sealed.subarray(65);
    const mac = createHmac('sha256', key).update(payload).digest('hex');
    if (sealed.length < 65 || sealed[64] !== 10 || !timingSafeEqual(sealed.subarray(0, 64), Buffer.from(mac))) throw new Error('missing or invalid replay harness authentication');
    const records = payload.toString('utf8').split('\n');
    if (records.length !== 3 || records[2] !== '') throw new Error('incomplete replay records');
    // Only the supervisor publishes authenticated bytes, after process exit.
    // In the container the worker's uid cannot access this directory.
    writeFileSync(process.env.INVARIANT_REPLAY_RESULT!, records[0], {flag: 'wx', mode: 0o644});
    writeFileSync(process.env.INVARIANT_REPLAY_OBSERVATION!, records[1], {flag: 'wx', mode: 0o644});
  } finally {
    key.fill(0);
    closeSync(channel);
  }
}

if (process.env.INVARIANT_REPLAY_REQUEST && process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  if (process.env.INVARIANT_REPLAY_WORKER === '1') {
    replay().catch(err => { console.error(err); process.exitCode = 1; });
  } else {
    try { supervise(); } catch (err) { console.error(err); process.exitCode = 1; }
  }
}
