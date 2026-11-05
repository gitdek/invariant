"""Opaque process transport for prepared Python images; no pytest policy."""

import ctypes
import errno
import os
import selectors
import signal
import struct
import subprocess
import sys
import time


def frame(kind, payload=b""):
    sys.stdout.buffer.write(kind + struct.pack(">I", len(payload)) + payload)
    sys.stdout.buffer.flush()


def kill_group(group):
    try:
        os.killpg(group, signal.SIGKILL)
    except ProcessLookupError:
        pass


def reap(group, end):
    while True:
        try:
            while os.waitpid(-group, os.WNOHANG)[0] > 0:
                pass
        except ChildProcessError:
            pass
        try:
            os.killpg(group, 0)
        except ProcessLookupError:
            return
        if time.monotonic() >= end:
            raise RuntimeError("process descendants did not exit")
        kill_group(group)
        time.sleep(0.001)


def run():
    if len(sys.argv) < 3:
        raise RuntimeError("process helper needs an endpoint variable and arguments")
    if sys.platform == "linux":
        libc = ctypes.CDLL(None, use_errno=True)
        if libc.prctl(36, 1, 0, 0, 0) != 0:
            raise OSError(ctypes.get_errno(), "adopt process descendants")
    elif sys.platform != "darwin":
        raise RuntimeError("process helper requires Linux or Darwin")
    read, write = os.pipe()
    process = None
    poll = selectors.DefaultSelector()
    try:
        env = os.environ.copy()
        if sys.argv[1]:
            env[sys.argv[1]] = "/dev/fd/" + str(write)
        process = subprocess.Popen(
            sys.argv[2:], env=env, stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            pass_fds=(write,) if sys.argv[1] else (), start_new_session=True,
        )
        os.close(write)
        write = -1
        frame(b"s")
        for fd, kind in ((process.stdout.fileno(), b"o"),
                         (process.stderr.fileno(), b"e"), (read, b"v")):
            os.set_blocking(fd, False)
            poll.register(fd, selectors.EVENT_READ, kind)
        poll.register(sys.stdin.fileno(), selectors.EVENT_READ, b"control")
        end, interrupted, exited = None, False, False
        while True:
            code = process.poll()
            if code is not None and not exited:
                exited = True
                kill_group(process.pid)
                end = time.monotonic() + 2
                frame(b"x", str(code if code >= 0 else -1).encode())
            if end is not None and time.monotonic() >= end:
                raise RuntimeError("process channels did not drain")
            if exited and len(poll.get_map()) == (0 if interrupted else 1):
                break
            for key, _ in poll.select(0.01):
                try:
                    data = os.read(key.fd, 32 << 10)
                except BlockingIOError:
                    continue
                if key.data == b"control":
                    if not data:
                        interrupted = True
                        kill_group(process.pid)
                        # Keep the control descriptor out of subsequent polls.
                        poll.unregister(key.fd)
                    continue
                if data:
                    frame(key.data, data)
                else:
                    poll.unregister(key.fd)
            if interrupted and exited and not poll.get_map():
                break
        reap(process.pid, end)
        if interrupted:
            raise RuntimeError("process interrupted by host")
        frame(b"d")
    finally:
        if process is not None:
            kill_group(process.pid)
            process.wait(timeout=2)
            reap(process.pid, time.monotonic() + 2)
            process.stdout.close()
            process.stderr.close()
        poll.close()
        os.close(read)
        if write >= 0:
            os.close(write)


if __name__ == "__main__":
    sys.stdout.buffer.write(b"invariant-process-v1\n")
    sys.stdout.buffer.flush()
    try:
        run()
    except BaseException as error:
        try:
            frame(b"f", str(error).encode()[:64 << 10])
        except OSError as broken:
            if broken.errno != errno.EPIPE:
                raise
        sys.exit(1)
