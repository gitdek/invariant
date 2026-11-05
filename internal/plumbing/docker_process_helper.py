"""Opaque process transport; framework evidence is never interpreted here."""
import ctypes
import os
import selectors
import signal
import struct
import subprocess
import sys
import time


def frame(kind, data=b""):
    sys.stdout.buffer.write(kind + struct.pack(">I", len(data)) + data)
    sys.stdout.buffer.flush()


def kill_group(pid):
    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        pass


def reap_group(pid, end):
    while True:
        try:
            while os.waitpid(-pid, os.WNOHANG)[0]:
                pass
        except ChildProcessError:
            pass
        try:
            os.killpg(pid, 0)
        except ProcessLookupError:
            return
        if time.monotonic() >= end:
            raise RuntimeError("process descendants did not exit")
        kill_group(pid)
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
    proc = None
    selector = selectors.DefaultSelector()
    try:
        env = os.environ.copy()
        if sys.argv[1]:
            env[sys.argv[1]] = "/dev/fd/" + str(write)
        proc = subprocess.Popen(sys.argv[2:], stdin=subprocess.DEVNULL,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                env=env, pass_fds=(write,) if sys.argv[1] else (),
                                start_new_session=True)
        os.close(write)
        write = -1
        frame(b"s")
        for fd, kind in ((proc.stdout.fileno(), b"o"),
                         (proc.stderr.fileno(), b"e"), (read, b"v")):
            os.set_blocking(fd, False)
            selector.register(fd, selectors.EVENT_READ, kind)
        selector.register(0, selectors.EVENT_READ, b"control")
        end = None
        while True:
            code = proc.poll()
            if code is not None and end is None:
                kill_group(proc.pid)
                end = time.monotonic() + 2
                frame(b"x", str(code if code >= 0 else -1).encode())
                selector.unregister(0)
            if end is not None and not selector.get_map():
                reap_group(proc.pid, end)
                frame(b"d")
                return
            if end is not None and time.monotonic() >= end:
                raise RuntimeError("process channels did not drain")
            for key, _ in selector.select(0.01):
                data = os.read(key.fd, 32768)
                if key.data == b"control":
                    if not data:
                        raise RuntimeError("process interrupted by host")
                elif data:
                    frame(key.data, data)
                else:
                    selector.unregister(key.fd)
    finally:
        if proc is not None:
            kill_group(proc.pid)
            proc.wait(timeout=2)
            reap_group(proc.pid, time.monotonic() + 2)
            proc.stdout.close()
            proc.stderr.close()
        selector.close()
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
            frame(b"f", str(error).encode()[:65536])
        finally:
            sys.exit(1)
