'use strict';
// Opaque transport. Only the host interprets framework evidence.
const fs = require('node:fs');
const path = require('node:path');
const cp = require('node:child_process');
const write = fs.writeSync.bind(fs);
function frame(kind, bytes = Buffer.alloc(0)) {
  bytes = Buffer.from(bytes);
  do {
    const piece = bytes.subarray(0, 65536);
    const header = Buffer.alloc(5);
    header[0] = kind.charCodeAt(0);
    header.writeUInt32BE(piece.length, 1);
    write(1, header);
    if (piece.length) write(1, piece);
    bytes = bytes.subarray(piece.length);
  } while (bytes.length);
}
write(1, 'invariant-process-v1\n');
let child, descriptor, polling, deadline, failed = false, finished = false;
function kill() {
  if (child?.pid) {
    try { process.kill(-child.pid, 'SIGKILL'); } catch (e) { if (e.code !== 'ESRCH') throw e; }
  }
}
function drain() {
  const bytes = Buffer.alloc(32768);
  while (true) {
    let count;
    try { count = fs.readSync(descriptor, bytes, 0, bytes.length, null); }
    catch (e) { if (e.code === 'EAGAIN') return; throw e; }
    if (!count) return;
    frame('v', bytes.subarray(0, count));
  }
}
function fail(error) {
  if (finished || failed) return;
  failed = true;
  try { drain(); } catch (_) {}
  frame('f', String(error).slice(0, 16000));
  try { kill(); } catch (_) {}
  clearInterval(polling);
  clearTimeout(deadline);
  // The container's init reaps descendants; the host retains container cleanup.
  setTimeout(() => process.exit(1), 100).unref();
  process.stdin.destroy();
  process.exitCode = 1;
}
// Unlink before spawning: no workload can reopen a named evidence endpoint or
// leave a replacement for another invocation. The child inherits only its end.
function pipe(name) {
  const endpoint = path.join(__dirname, name);
  const made = cp.spawnSync('mkfifo', ['-m', '600', '--', endpoint], {stdio: 'pipe'});
  if (made.error || made.status !== 0) throw new Error('cannot create private event pipe');
  const read = fs.openSync(endpoint, fs.constants.O_RDWR | fs.constants.O_NONBLOCK);
  const send = fs.openSync(endpoint, fs.constants.O_WRONLY | fs.constants.O_NONBLOCK);
  fs.unlinkSync(endpoint);
  return [read, send];
}

function start(eventEnv, executable, args, credential) {
  const [events, send] = pipe('events');
  descriptor = events;
  const env = {...process.env};
  if (eventEnv) env[eventEnv] = '/dev/fd/3';
  const stdio = ['ignore', 'pipe', 'pipe', send];
  let bootstrap;
  if (credential) {
    const [read, write] = pipe('credential');
    bootstrap = read;
    if (fs.writeSync(write, credential) !== credential.length) throw new Error('short credential write');
    fs.closeSync(write);
    credential.fill(0);
    stdio.push(read);
  }
  try { child = cp.spawn(executable, args, {env, detached: true, stdio}); }
  finally {
    fs.closeSync(send);
    if (bootstrap !== undefined) fs.closeSync(bootstrap);
  }
  child.once('error', fail);
  child.once('spawn', () => {
    frame('s');
    polling = setInterval(() => { try { drain(); } catch (e) { fail(e); } }, 5);
  });
  child.stdout.on('data', data => frame('o', data));
  child.stderr.on('data', data => frame('e', data));
  child.once('exit', (code, signal) => {
    frame('x', String(signal || code === null ? -1 : code));
    deadline = setTimeout(() => fail(new Error('process channels did not drain')), 2000);
  });
  child.once('close', () => {
    function settled() {
      if (failed) return;
      try {
        // Allow init to reap normally exiting descendants within the original
        // drain deadline. A surviving worker still vetoes completion.
        try { process.kill(-child.pid, 0); setTimeout(settled, 10); return; }
        catch (e) { if (e.code !== 'ESRCH') throw e; }
        drain();
        clearInterval(polling);
        clearTimeout(deadline);
        fs.closeSync(descriptor);
        frame('d');
        finished = true;
        process.stdin.destroy();
      } catch (e) { fail(e); }
    }
    settled();
  });
}

try {
  const [eventEnv, executable, ...args] = process.argv.slice(2);
  if (!executable) throw new Error('missing workload');
  process.stdin.on('end', () => fail(new Error('process interrupted by host')));
  process.stdin.on('error', fail);
  if (eventEnv === 'INVARIANT_NODE_EVENTS') {
    let credential = Buffer.alloc(0);
    process.stdin.on('data', bytes => {
      try {
        if (child || credential.length + bytes.length > 32) throw new Error('invalid host credential frame');
        credential = Buffer.concat([credential, bytes]);
        bytes.fill(0);
        if (credential.length === 32) {
          start(eventEnv, executable, args, credential);
          credential = Buffer.alloc(0);
        }
      } catch (e) { fail(e); }
    });
  } else {
    start(eventEnv, executable, args);
    process.stdin.resume();
  }
} catch (e) { fail(e); }
