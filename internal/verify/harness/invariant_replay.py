"""Gate-owned command replay, separate from sampling and exploration."""
import asyncio
import copy
import hmac
import inspect
import json
import math
import os
import runpy
import subprocess
import sys
import tempfile


async def awaited(value):
    return await value if inspect.isawaitable(value) else value


def member(session, name):
    return session[name] if isinstance(session, dict) else getattr(session, name)


def canonical(value):
    if isinstance(value, dict):
        if value.get('kind') == 'integer':
            return ('integer', str(int(value['atom'])))
        if value.get('kind') in ('bool', 'string', 'model'):
            return (value['kind'], value.get('atom', ''))
        if value.get('kind') == 'set':
            return ('set', tuple(sorted(set(canonical(v) for v in value.get('elements', [])))))
        if value.get('kind') == 'sequence':
            return ('sequence', tuple(canonical(v) for v in value.get('elements', [])))
        if value.get('kind') in ('record', 'function'):
            return (value['kind'], tuple(sorted(canonical(v) for v in value.get('entries', []))))
        return tuple((k, canonical(v)) for k, v in sorted(value.items()))
    if isinstance(value, list):
        return tuple(canonical(v) for v in value)
    return value


async def replay():
    # Consume the private bootstrap pipe before importing application code.
    # Seal the harness's own bytes, never records read from driver files.
    # The seal defends against records written or replayed outside this
    # harness. The driver runs in this process, so it can always reach the
    # seal, the channel and these records, which no in-process harness can
    # rule out: a driver that does is tampering with the gate, and review of
    # the driver is the check against it.
    with os.fdopen(0, 'rb') as bootstrap:
        key = bootstrap.read()
    if len(key) != 32:
        raise ValueError('missing replay authentication')
    authentication = hmac.new(key, digestmod='sha256')
    del key
    encode = json.dumps
    channel = os.fdopen(int(os.environ['INVARIANT_REPLAY_CHANNEL']), 'wb')
    if os.environ.get('INVARIANT_REPLAY_ISOLATE') == '1':
        os.setgroups([])
        os.setgid(65534)
        os.setuid(65534)
    with open(os.environ['INVARIANT_REPLAY_REQUEST']) as source:
        request = json.load(source)
    driver = os.path.join('/src', os.environ['DRIVER'])
    result = dict(version=1, request_id=request['id'], invocation_id=request['invocation_id'], initial=None, calls=[], outcome='failed')
    observed = dict(request_id=request['id'], invocation_id=request['invocation_id'], initial=None, calls=[], started=False, completed=False, exit_code=1)
    session, failure = None, None
    with channel:
        try:
            module = runpy.run_path(driver, run_name='invariant_replay_driver')
            factory = module.get('invariant_replay')
            if not callable(factory):
                raise ValueError('invariant_replay(initial) is required for current bug replay')
            session = await awaited(factory(copy.deepcopy(request['path']['states'][0])))
            if not all(callable(member(session, name)) for name in ('read', 'bind', 'close')):
                raise ValueError('invariant_replay must return read, bind and close')
            observed['started'] = True
            result['initial'] = copy.deepcopy(await awaited(member(session, 'read')()))
            if canonical(result['initial']) != canonical(request['path']['states'][0]):
                raise ValueError('initial state differs from requested replay')
            for index, step in enumerate(request['path']['steps'][:request['path']['offending'] + 1]):
                before = copy.deepcopy(await awaited(member(session, 'read')()))
                if canonical(before) != canonical(request['path']['states'][step['from']]):
                    raise ValueError('replay prefix diverged before dispatch')
                binding = await awaited(member(session, 'bind')(step['action'], copy.deepcopy(step['args'] or [])))
                # None has methods of its own, and calling its class returns
                # None: a binding needs an application object to call.
                if not isinstance(binding, dict) or binding.get('target') is None or not isinstance(binding.get('method'), str) or not isinstance(binding.get('args'), list):
                    raise ValueError('bind must name a real application method and argument list')
                operation = getattr(binding.get('target'), binding['method'])
                if not callable(operation):
                    raise ValueError('bound application method is not callable')
                if canonical(before) != canonical(await awaited(member(session, 'read')())):
                    raise ValueError('bind changed application state')
                refusal = binding.get('refusal')
                if 'refusal' in binding and (not isinstance(refusal, dict) or list(refusal) != ['equals'] or type(refusal['equals']) not in (type(None), bool, int, float, str)):
                    raise ValueError('refusal.equals must be a primitive literal')
                if refusal is not None and type(refusal['equals']) is float and not math.isfinite(refusal['equals']):
                    raise ValueError('refusal.equals must be finite')
                call = dict(index=index, action=step['action'], args=copy.deepcopy(step['args']), before=before, after=None, dispatched=False, outcome='failed')
                result['calls'].append(call)
                call['dispatched'] = True
                returned = await awaited(operation(*binding['args']))
                call['after'] = copy.deepcopy(await awaited(member(session, 'read')()))
                refused = refusal is not None and type(returned) is type(refusal['equals']) and returned == refusal['equals']
                call['outcome'] = 'refused' if refused else 'returned'
                if index < request['path']['offending'] and (call['outcome'] != 'returned' or canonical(call['after']) != canonical(request['path']['states'][step['to']])):
                    raise ValueError('setup call diverged from requested replay')
            result['outcome'] = 'refused' if result['calls'][-1]['outcome'] == 'refused' else 'complete'
        except BaseException as error:
            failure = error
        finally:
            try:
                if session is None:
                    raise ValueError('replay session could not close')
                await awaited(member(session, 'close')())
                if failure is None:
                    observed['completed'] = True
            except BaseException as error:
                if failure is None:
                    failure = error
            observed['initial'] = copy.deepcopy(result['initial'])
            observed['calls'] = copy.deepcopy(result['calls'])
            observed['exit_code'] = 1 if failure is not None else 0
        if failure is not None:
            raise failure
        payload = (encode(result, allow_nan=False) + '\n' + encode(observed, allow_nan=False) + '\n').encode('utf-8')
        authentication.update(payload)
        channel.write(authentication.hexdigest().encode('ascii') + b'\n' + payload)


def supervise():
    key = os.urandom(32)
    # An unlinked descriptor transports completion without a driver-writable
    # pathname or a pipe that can deadlock on a large replay record.
    with tempfile.TemporaryFile() as channel:
        env = dict(os.environ, INVARIANT_REPLAY_WORKER='1', INVARIANT_REPLAY_CHANNEL=str(channel.fileno()))
        # Keep the trusted launcher's flags, path order and dependency image.
        child = subprocess.Popen([sys.executable, *sys.orig_argv[1:]], env=env,
                                 stdin=subprocess.PIPE, pass_fds=(channel.fileno(),))
        child.communicate(key)
        if child.returncode != 0:
            raise ValueError('replay worker failed: ' + str(child.returncode))
        channel.seek(0)
        sealed = channel.read()
    payload = sealed[65:]
    mac = hmac.new(key, payload, 'sha256').hexdigest().encode('ascii')
    if len(sealed) < 65 or sealed[64:65] != b'\n' or not hmac.compare_digest(sealed[:64], mac):
        raise ValueError('missing or invalid replay harness authentication')
    records = payload.split(b'\n')
    if len(records) != 3 or records[2] != b'':
        raise ValueError('incomplete replay records')
    # Only the supervisor publishes authenticated bytes, after process exit.
    # In the container the worker's uid cannot access this directory.
    for name, record in zip(('INVARIANT_REPLAY_RESULT', 'INVARIANT_REPLAY_OBSERVATION'), records):
        with open(os.environ[name], 'xb') as output:
            output.write(record)


if __name__ == '__main__':
    if os.environ.get('INVARIANT_REPLAY_WORKER') == '1':
        asyncio.run(replay())
    else:
        supervise()
