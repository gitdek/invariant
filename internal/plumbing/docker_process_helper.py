"""Opaque process transport, staged from the host for each invocation."""
import ctypes
import os
import selectors
import signal
import struct
import subprocess
import sys
import time


def write(data):
    while data:
        data = data[os.write(1, data):]


def frame(kind, data=b""):
    write(kind + struct.pack(">I", len(data)) + data)


def kill_group(pid):
    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        pass


def reap(pid):
    while True:
        try:
            child, _ = os.waitpid(-pid, os.WNOHANG)
        except ChildProcessError:
            break
        if child == 0:
            break
    try:
        os.killpg(pid, 0)
    except ProcessLookupError:
        return True
    return False


def run():
    if len(sys.argv) < 3:
        raise RuntimeError("process helper needs an endpoint variable and arguments")
    if sys.platform == "linux":
        libc = ctypes.CDLL(None, use_errno=True)
        if libc.prctl(36, 1, 0, 0, 0) != 0:
            raise OSError(ctypes.get_errno(), "adopt process descendants")
    elif sys.platform != "darwin":
        raise RuntimeError("process helper requires Linux or Darwin")
    event_read, event_write = os.pipe()
    env = os.environ.copy()
    if sys.argv[1]:
        env[sys.argv[1]] = "/dev/fd/" + str(event_write)
    process = None
    poll = selectors.DefaultSelector()
    try:
        process = subprocess.Popen(
            sys.argv[2:], env=env, stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            pass_fds=(event_write,) if sys.argv[1] else (), start_new_session=True,
        )
        os.close(event_write)
        event_write = -1
        frame(b"s")
        for file, kind in ((process.stdout, b"o"), (process.stderr, b"e"),
                           (event_read, b"v"), (0, b"i")):
            poll.register(file, selectors.EVENT_READ, kind)
        end = None
        while True:
            code = process.poll()
            if code is not None and end is None:
                kill_group(process.pid)
                end = time.monotonic() + 2
                frame(b"x", str(code).encode("ascii"))
            for key, _ in poll.select(0.01):
                data = os.read(key.fd, 32768)
                if key.data == b"i":
                    if not data:
                        raise RuntimeError("process interrupted by host")
                elif data:
                    frame(key.data, data)
                else:
                    poll.unregister(key.fileobj)
            if end is not None:
                joined = reap(process.pid)
                if joined and len(poll.get_map()) == 1:
                    frame(b"d")
                    return
                if time.monotonic() >= end:
                    raise RuntimeError("process descendants or channels did not finish")
                kill_group(process.pid)
    finally:
        if process is not None:
            kill_group(process.pid)
            process.wait(timeout=2)
            end = time.monotonic() + 2
            while not reap(process.pid) and time.monotonic() < end:
                time.sleep(0.001)
            process.stdout.close()
            process.stderr.close()
        poll.close()
        os.close(event_read)
        if event_write >= 0:
            os.close(event_write)


write(b"invariant-process-v1\n")
try:
    run()
except BaseException as error:
    try:
        frame(b"f", str(error).encode("utf-8", "replace")[:65536])
    finally:
        sys.exit(1)
