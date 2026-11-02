"""The plumbing sandbox's bounded, standard-library-only test runner.

Only this process writes records. Child output is diagnostic text, even when
it contains a replay of the record protocol. Go's test2json converter supplies
test lifecycle events; a process exit alone never proves a test ran.
"""

import json
import math
import os
import queue
import re
import selectors
import signal
import stat
import subprocess
import sys
import tempfile
import threading
import time


TIMING_FILE = "invariant-test-times-v1.json"
MAX_TIMING_BYTES = 4 * 1024 * 1024
TOP_LEVEL = re.compile(r"(?:Test|Fuzz|Example)\w*\Z")
TEST_REPORT = re.compile(r"(?m)^\s*--- (PASS|FAIL|SKIP):\s+(\S+) \([0-9.]+s\)\s*$")


class Runner:
    def __init__(self, config):
        self.config = config
        self.root = os.getcwd()
        # Leave the host time to receive the final records before its deadline.
        self.deadline = time.monotonic() + max(
            0, config["deadline"] - time.time() - 0.15
        )
        self.output_lock = threading.Lock()
        self.state_lock = threading.Lock()
        self.stop_signal = 0
        self.deadline_reported = False
        self.active = set()
        self.results = {}
        self.measured = {}
        self.acceptance = config.get("acceptance") or []
        self.packages = {}
        self.tests_ok = True

    def emit(self, kind, **fields):
        with self.output_lock:
            print("@@result " + json.dumps(dict(kind=kind, **fields)), flush=True)

    def diagnostic(self, text):
        if text:
            # Escape anywhere in the line: legacy markers can follow text.
            with self.output_lock:
                print(re.sub(r"@(?=@)", "@ ", text.rstrip("\n")), flush=True)

    def problem(self, phase, message, **fields):
        self.emit("problem", phase=phase, message=message, **fields)

    def phase(self, phase, passed):
        self.emit("phase", phase=phase, status="passed" if passed else "failed")
        if phase in ("vet", "test", "accept"):
            with self.output_lock:
                print("@@invariant %s=%d" % (phase, 0 if passed else 1), flush=True)

    def expired(self, phase):
        if time.monotonic() < self.deadline:
            return False
        with self.state_lock:
            first = not self.deadline_reported
            self.deadline_reported = True
        if first:
            self.emit("deadline", phase=phase, message="Sandbox deadline exceeded during " + phase)
        return True

    def interrupted(self, phase, message):
        self.emit("interrupted", phase=phase, message=message)

    def signal(self, number, _frame):
        # A handler may interrupt this thread while it holds an output or
        # state lock. Defer all locking and reporting to normal execution.
        self.stop_signal = number

    @staticmethod
    def kill(process):
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass

    def command(self, args, phase, cwd=None, limit=None, capture=False, on_line=None, display=True, convert_package=None):
        """Stream and reap a whole process group within its remaining budget."""
        end = min(self.deadline, limit if limit is not None else self.deadline)
        if self.stop_signal or time.monotonic() >= end:
            self.expired(phase)
            return dict(code=None, timed_out=True, output="")
        processes = []
        converter = None
        try:
            if convert_package is not None:
                # Own the test binary ourselves. test2json's command mode
                # masks signal exits as status 1, which loses interruption
                # evidence after a test has already reported a failure.
                converter = subprocess.Popen(
                    ["go", "tool", "test2json", "-t", "-p", convert_package],
                    cwd=cwd or self.root, stdin=subprocess.PIPE,
                    stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                    start_new_session=True,
                )
                processes.append(converter)
            process = subprocess.Popen(
                args, cwd=cwd or self.root,
                stdout=converter.stdin if converter else subprocess.PIPE,
                stderr=subprocess.STDOUT if converter else subprocess.PIPE,
                start_new_session=True,
            )
            processes.append(process)
            if converter:
                converter.stdin.close()
        except OSError as error:
            for child in processes:
                self.kill(child)
                try:
                    child.wait(timeout=0.1)
                except subprocess.TimeoutExpired:
                    pass
                for stream in (child.stdin, child.stdout, child.stderr):
                    if stream:
                        stream.close()
            self.problem(phase, "Could not start %s: %s" % (args[0], error))
            return dict(code=None, timed_out=False, output="")
        with self.state_lock:
            self.active.update(processes)
        stdout = converter.stdout if converter else process.stdout
        stderr = converter.stderr if converter else process.stderr
        selector = selectors.DefaultSelector()
        pending = {}
        collected = []
        for stream in (stdout, stderr):
            selector.register(stream, selectors.EVENT_READ)
            pending[stream] = b""
        timed_out = False
        drain_end = None

        def line(stream, data):
            text = data.decode("utf-8", errors="replace")
            if stream is stdout:
                if capture:
                    collected.append(text)
                if on_line is not None:
                    on_line(text)
                elif display:
                    self.diagnostic(text)
            else:
                self.diagnostic(text)

        try:
            while selector.get_map():
                now = time.monotonic()
                if drain_end is None and (now >= end or self.stop_signal):
                    timed_out = now >= end
                    for child in processes:
                        self.kill(child)
                    drain_end = now + 0.1
                    self.expired(phase)
                if drain_end is not None and now >= drain_end:
                    break
                wait = max(0, min(0.05, (drain_end or end) - now))
                for key, _ in selector.select(wait):
                    stream = key.fileobj
                    chunk = os.read(stream.fileno(), 65536)
                    if not chunk:
                        if pending[stream]:
                            line(stream, pending[stream])
                            pending[stream] = b""
                        selector.unregister(stream)
                        continue
                    pending[stream] += chunk
                    while b"\n" in pending[stream]:
                        complete, pending[stream] = pending[stream].split(b"\n", 1)
                        line(stream, complete + b"\n")
                    # Bound an unterminated diagnostic line. Keep its last byte
                    # so two '@' bytes split across reads cannot forge a marker.
                    if len(pending[stream]) > 1024 * 1024:
                        line(stream, pending[stream][:-1])
                        pending[stream] = pending[stream][-1:]
            # A child can close its pipes without exiting; waiting must still
            # use the same deadline, not begin a fresh subprocess timeout.
            while process.poll() is None:
                now = time.monotonic()
                if now >= end or self.stop_signal:
                    timed_out = timed_out or now >= end
                    for child in processes:
                        self.kill(child)
                    self.expired(phase)
                    break
                try:
                    process.wait(timeout=min(0.05, end - now))
                except subprocess.TimeoutExpired:
                    pass
            self.kill(process)  # also remove descendants of a completed command
            try:
                code = process.wait(timeout=0.1)
            except subprocess.TimeoutExpired:
                code = None
            if converter:
                try:
                    conversion_code = converter.wait(timeout=0.1)
                except subprocess.TimeoutExpired:
                    self.kill(converter)
                    conversion_code = None
                if conversion_code != 0 and not timed_out and not self.stop_signal:
                    self.interrupted(phase, "Go test event conversion did not complete")
                    if code == 0:
                        code = 1
            for stream, data in pending.items():
                if data:
                    line(stream, data)
            if code is not None and code < 0 and not timed_out and not self.stop_signal:
                self.interrupted(phase, "%s was interrupted by signal %d" % (args[0], -code))
            return dict(code=code, timed_out=timed_out, output="".join(collected))
        finally:
            selector.close()
            stdout.close()
            stderr.close()
            for child in processes:
                self.kill(child)
                try:
                    child.wait(timeout=0.1)
                except subprocess.TimeoutExpired:
                    pass
            with self.state_lock:
                self.active.difference_update(processes)

    def record(self, package, name, phase, status, shard=0, **extra):
        row = dict(package=package["ImportPath"], name=name, phase=phase, status=status, shard=shard)
        if phase == "accept":
            for acceptance in self.acceptance:
                if self.package_path(os.path.dirname(acceptance["file"])) == package["path"] and (acceptance["name"] == name or name.startswith(acceptance["name"] + "/")):
                    row["file"] = acceptance["file"]
                    break
        row.update(extra)
        with self.state_lock:
            self.results[(phase, row["package"], name)] = row
        self.emit("test", **row)

    @staticmethod
    def package_path(path):
        return "./" + os.path.normpath(path or ".").removeprefix("./")

    def inventory(self):
        module = ""
        try:
            with open("go.mod", encoding="utf-8") as source:
                match = re.search(r"(?m)^\s*module\s+(\S+)", source.read())
                if match:
                    module = match[1].strip('"')
        except OSError:
            pass
        paths = {self.package_path(path) for path in self.config.get("packages") or []}
        paths.update(self.package_path(os.path.dirname(item["file"])) for item in self.acceptance)
        for path in sorted(paths):
            suffix = "" if path == "./." else path[2:]
            self.packages[path] = dict(
                path=path, Dir=os.path.abspath(path),
                ImportPath=module + ("/" + suffix if suffix else "") if module else path,
            )
        if paths:
            listed = self.command(["go", "list", "-json", *sorted(paths)], "list", capture=True, display=False)
            decoder, remaining = json.JSONDecoder(), listed["output"].lstrip()
            try:
                while remaining:
                    metadata, offset = decoder.raw_decode(remaining)
                    remaining = remaining[offset:].lstrip()
                    path = self.package_path(os.path.relpath(metadata["Dir"], self.root))
                    if path in self.packages:
                        self.packages[path].update(metadata)
            except (ValueError, KeyError, TypeError):
                self.problem("list", "Could not read Go package metadata")
                self.diagnostic(listed["output"])
                self.tests_ok = False
            if listed["code"] != 0:
                self.tests_ok = False
                self.problem("list", "Go package discovery did not complete successfully")
        # Even if package listing or vet times out, every pinned test has an
        # explicit unstarted record, qualified using the module as a fallback.
        for item in self.acceptance:
            package = self.packages[self.package_path(os.path.dirname(item["file"]))]
            self.record(package, item["name"], "accept", "not_run")

    def formatting(self):
        passed = True
        for path in self.config.get("formatted") or []:
            if not os.path.isfile(path):
                continue
            outcome = self.command(["gofmt", "-l", path], "gofmt", capture=True, display=False)
            if outcome["code"] != 0:
                passed = False
                self.problem("gofmt", "gofmt did not complete for " + path)
            for filename in outcome["output"].splitlines():
                if filename == path:
                    passed = False
                    with self.output_lock:
                        print("@@gofmt " + re.sub(r"@(?=@)", "@ ", filename), flush=True)
                else:
                    self.diagnostic(filename)
            if self.expired("gofmt") or self.stop_signal:
                break
        self.phase("gofmt", passed)

    def compile(self, directory):
        for index, package in enumerate(self.packages.values()):
            if self.expired("build") or self.stop_signal:
                self.tests_ok = False
                break
            binary = os.path.join(directory, str(index) + ".test")
            outcome = self.command(
                ["go", "test", "-c", "-vet=off", "-o", binary, package["path"]], "build"
            )
            if outcome["code"] != 0:
                self.tests_ok = False
                self.problem("build", "Package build failed: " + package["ImportPath"], package=package["ImportPath"])
                # A completed compiler failure is evidence that a proposed
                # API is missing; cancellation and timeouts are not evidence.
                if outcome["code"] is not None and outcome["code"] > 0 and not outcome["timed_out"]:
                    for item in self.acceptance:
                        if self.package_path(os.path.dirname(item["file"])) == package["path"]:
                            self.record(package, item["name"], "accept", "failed", reason="build")
                continue
            if os.path.isfile(binary):
                package["binary"] = binary
            elif package.get("TestGoFiles") or package.get("XTestGoFiles"):
                self.tests_ok = False
                self.problem("build", "Package build produced no test binary: " + package["ImportPath"], package=package["ImportPath"])

    def discover(self):
        tests = []
        for package in self.packages.values():
            if self.expired("discovery") or self.stop_signal:
                self.tests_ok = False
                break
            if "binary" not in package:
                continue
            outcome = self.command(
                [package["binary"], "-test.list=^(Test|Fuzz|Example)"], "discovery",
                cwd=package["Dir"], capture=True,
            )
            if outcome["code"] != 0:
                self.tests_ok = False
                self.problem("discovery", "Test discovery failed: " + package["ImportPath"], package=package["ImportPath"])
                continue
            for name in sorted({line for line in outcome["output"].splitlines() if TOP_LEVEL.fullmatch(line)}):
                tests.append((package, name))
                self.record(package, name, "test", "not_run")
        return tests

    def timings(self, tests):
        discovered = {(package["ImportPath"], name) for package, name in tests}
        hints = {}
        if time.monotonic() >= self.deadline or self.stop_signal:
            return hints
        try:
            # A hint is untrusted, including its filesystem type. O_NONBLOCK
            # prevents a substituted FIFO from hanging before fstat; NOFOLLOW
            # avoids following a replacement symlink outside the cache.
            filename = os.path.join(os.environ["GOCACHE"], TIMING_FILE)
            if not stat.S_ISREG(os.lstat(filename).st_mode):
                return hints
            descriptor = os.open(filename, os.O_RDONLY | os.O_NONBLOCK | os.O_NOFOLLOW)
            with os.fdopen(descriptor, "rb") as source:
                metadata = os.fstat(source.fileno())
                if not stat.S_ISREG(metadata.st_mode) or metadata.st_size > MAX_TIMING_BYTES:
                    return hints
                data = source.read(MAX_TIMING_BYTES + 1)
                if len(data) > MAX_TIMING_BYTES:
                    return hints
                records = json.loads(data)
            if not isinstance(records, list):
                return hints
            for record in records:
                if not isinstance(record, dict):
                    continue
                package, name, elapsed = record.get("package"), record.get("name"), record.get("elapsed_seconds")
                if not isinstance(package, str) or not isinstance(name, str):
                    continue
                key = (package, name)
                if key in discovered and type(elapsed) in (int, float) and math.isfinite(elapsed) and elapsed >= 0:
                    hints[key] = elapsed
        except (OSError, ValueError, TypeError, KeyError, OverflowError, RecursionError):
            pass
        return hints

    def save_timings(self, hints):
        hints.update(self.measured)
        if not hints or time.monotonic() >= self.deadline or self.stop_signal:
            return
        filename = None
        try:
            cache = os.environ["GOCACHE"]
            with tempfile.NamedTemporaryFile(mode="w", dir=cache, prefix=".invariant-times-", delete=False) as output:
                filename = output.name
                json.dump([
                    dict(package=package, name=name, elapsed_seconds=elapsed)
                    for (package, name), elapsed in sorted(hints.items())
                ], output, allow_nan=False)
                output.write("\n")
            os.replace(filename, os.path.join(cache, TIMING_FILE))
        except (OSError, ValueError, KeyError):
            # The hint is optional; a read-only or lost cache changes no verdict.
            pass
        finally:
            if filename is not None:
                try:
                    os.unlink(filename)
                except FileNotFoundError:
                    pass

    def test(self, package, name, phase, shard=0, limit=None):
        terminal = {"pass": "passed", "fail": "failed", "skip": "skipped"}
        reported = set()

        def event(line):
            try:
                value = json.loads(line)
            except ValueError:
                self.diagnostic(line)
                return
            if not isinstance(value, dict):
                self.diagnostic(line)
                return
            output = value.get("Output", "")
            self.diagnostic(output)
            if value.get("Package") != package["ImportPath"]:
                return
            # test2json attaches its synthetic process-exit failure to the
            # current test, even when that test never reported a result. Only
            # genuine testing-package reports establish completed evidence.
            # Ordinary diagnostics omit OutputType, even if they look like reports.
            if value.get("OutputType") == "frame":
                for report in TEST_REPORT.finditer(output):
                    reported.add((report[2], report[1].lower()))
                    # A panic can end stdin-mode test2json without a terminal
                    # action. Its framed failure is still completed evidence;
                    # command() separately retains timeouts and interruptions.
                    if report[1] == "FAIL" and (report[2] == name or report[2].startswith(name + "/")):
                        self.record(package, report[2], phase, "failed", shard)
            test_name, action = value.get("Test", ""), value.get("Action")
            if not isinstance(test_name, str):
                return
            if test_name != name and not test_name.startswith(name + "/"):
                return
            if action == "run":
                self.record(package, test_name, phase, "running", shard)
            elif action in terminal and (test_name, action) in reported:
                elapsed = value.get("Elapsed")
                extra = {}
                if type(elapsed) in (int, float) and math.isfinite(elapsed) and elapsed >= 0:
                    extra["elapsed_seconds"] = elapsed
                    if phase == "test" and test_name == name:
                        with self.state_lock:
                            self.measured[(package["ImportPath"], name)] = elapsed
                self.record(package, test_name, phase, terminal[action], shard, **extra)

        end = min(self.deadline, limit if limit is not None else self.deadline)
        remaining = max(0.001, end - time.monotonic())
        outcome = self.command(
            [package["binary"],
             "-test.v=test2json", "-test.run=^" + re.escape(name) + "$", "-test.timeout=%.6fs" % remaining],
            phase, cwd=package["Dir"], limit=limit, on_line=event,
            convert_package=package["ImportPath"],
        )
        with self.state_lock:
            status = self.results[(phase, package["ImportPath"], name)]["status"]
        passed = outcome["code"] == 0 and not outcome["timed_out"] and status in ("passed", "skipped")
        if not passed:
            detail = "timeout" if outcome["timed_out"] else "failed or unfinished"
            self.problem(phase, "%s %s/%s %s" % ("Shard %d" % shard if shard else "Acceptance", package["ImportPath"], name, detail), package=package["ImportPath"], name=name, shard=shard, status="timeout" if outcome["timed_out"] else "failed")
        return passed

    def workers(self, tests, hints):
        def order(item):
            key = (item[0]["ImportPath"], item[1])
            return (1, -hints[key], *key) if key in hints else (0, 0, *key)

        work = queue.Queue()
        for item in sorted(tests, key=order):
            work.put(item)
        end = min(self.deadline, time.monotonic() + self.config["shard_timeout"])

        def worker(shard):
            while time.monotonic() < end and not self.stop_signal:
                try:
                    package, name = work.get_nowait()
                except queue.Empty:
                    break
                try:
                    if not self.test(package, name, "test", shard, end):
                        self.tests_ok = False
                except Exception as error:
                    self.tests_ok = False
                    self.problem("test", "Shard %d failed: %s" % (shard, error), package=package["ImportPath"], name=name, shard=shard)
                finally:
                    work.task_done()

        threads = [threading.Thread(target=worker, args=(shard,)) for shard in range(1, 9)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        if not work.empty() or time.monotonic() >= end:
            self.tests_ok = False
            self.problem("test", "Shared test queue timeout; unstarted tests remain" if not work.empty() else "Shared test queue timeout", status="timeout")
        self.expired("test")
        self.phase("test", self.tests_ok and not self.stop_signal)

    def accept(self):
        passed = True
        for item in self.acceptance:
            if self.expired("accept") or self.stop_signal:
                passed = False
                break
            package = self.packages[self.package_path(os.path.dirname(item["file"]))]
            if "binary" not in package:
                passed = False
                continue
            if not self.test(package, item["name"], "accept"):
                passed = False
        self.phase("accept", passed)

    def run(self):
        self.emit("start")
        self.inventory()
        self.formatting()
        if not self.expired("vet") and not self.stop_signal:
            outcome = self.command(["go", "vet", "./..."], "vet")
            self.phase("vet", outcome["code"] == 0 and not outcome["timed_out"])
            if outcome["code"] != 0:
                self.problem("vet", "go vet failed or did not finish")
        with tempfile.TemporaryDirectory(prefix="invariant-test-binaries-") as directory:
            self.compile(directory)
            tests = self.discover()
            hints = self.timings(tests)
            self.workers(tests, hints)
            self.save_timings(hints)
            self.accept()
        self.expired("runner")
        if self.stop_signal:
            self.interrupted("runner", "Sandbox runner received signal %d" % self.stop_signal)
        self.emit("done")


def main():
    with open(sys.argv[1], encoding="utf-8") as source:
        runner = Runner(json.load(source))
    signal.signal(signal.SIGTERM, runner.signal)
    signal.signal(signal.SIGINT, runner.signal)
    try:
        runner.run()
    except Exception as error:
        runner.problem("runner", "Sandbox runner failed: " + str(error))
        runner.interrupted("runner", "Sandbox runner did not complete")
        with runner.state_lock:
            active = list(runner.active)
        for process in active:
            runner.kill(process)
        raise


if __name__ == "__main__":
    main()
