package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// The sample and every replay start from the same admitted source. Keep a
// pristine copy before the sample can write anything, with its selected image.
type commandReplaySource struct {
	dir, image, driver, language string
	existing                     bool
}

var errReplayCompletion = errors.New("replay command completion is unavailable")

func keepCommandReplay(p *project.Project, src, image, driver string) (s *commandReplaySource, err error) {
	if !CommandReplayAvailable(p.Manifest) {
		return nil, nil
	}
	required := false
	for _, b := range p.Of(project.Bug) {
		required = required || driverBugReplay(p, b)
	}
	if !required {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "invariant-replay-source-")
	if err != nil {
		return nil, err
	}
	s = &commandReplaySource{dir: dir, image: image, driver: driver, language: p.Manifest.Language, existing: len(p.Manifest.Existing) > 0}
	if err = copyReplaySource(src, filepath.Join(dir, "src")); err != nil {
		return nil, errors.Join(err, s.close())
	}
	return s, nil
}

// Selection happened before this snapshot. Do not apply Git or directory
// exclusions again: an explicitly selected file may live under out or bin.
func copyReplaySource(from, to string) error {
	return filepath.WalkDir(from, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, file)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("replay source %s is not regular", rel)
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0644)
	})
}

func (s *commandReplaySource) close() error {
	if s == nil || s.dir == "" {
		return nil
	}
	err := os.RemoveAll(s.dir)
	if err == nil {
		s.dir = ""
	}
	return err
}

func executeCommandReplays(ctx context.Context, r *Report, source *commandReplaySource, runner tlc.Runner, modelDir string) error {
	for i := range r.Bugs {
		e := r.Bugs[i].Replay
		if e == nil || !e.Required || e.Status != "pending" || e.Request == nil {
			continue
		}
		if source == nil {
			continue
		}
		result, observed, err := source.run(ctx, *e.Request)
		if err == nil {
			checked, checkErr := CheckBugReplay(ctx, runner, modelDir, *e.Request, result, observed)
			checked.Counterexample = e.Counterexample
			if checked.Request == nil {
				checked.Request = e.Request
			}
			if checked.Result == nil {
				checked.Result = &result
			}
			if checked.Observation == nil {
				checked.Observation = &observed
			}
			*e = checked
			err = checkErr
		} else {
			e.Status, e.Passed = "failed", false
			if errors.Is(err, errReplayCompletion) {
				e.Status = "replay-unavailable"
			}
			e.Message = replayMessage(err, source.dir)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Each bug keeps its own failed evidence; no sibling can mask it.
		if err != nil && e.Message == "" {
			e.Message = replayMessage(err, modelDir)
		}
	}
	return nil
}

func (s *commandReplaySource) run(ctx context.Context, q BugReplayRequest) (result BugReplayResult, observed BugReplayObservation, err error) {
	work, err := os.MkdirTemp("", "invariant-replay-")
	if err != nil {
		return result, observed, err
	}
	// The workspace isn't evidence: what the container leaves in it, as the
	// replay worker's __pycache__ on Linux, which the gate's user can't
	// remove, fails nothing, as in runConformanceRecording.
	defer os.RemoveAll(work)
	src, out, runtime, request := filepath.Join(work, "src"), filepath.Join(work, "out"), filepath.Join(work, "runtime"), filepath.Join(work, "request")
	if err = copyReplaySource(filepath.Join(s.dir, "src"), src); err != nil {
		return
	}
	for _, d := range []string{out, runtime, request} {
		if err = os.MkdirAll(d, 0755); err != nil {
			return
		}
	}
	if err = writeNaginiRuntime(runtime); err != nil {
		return
	}
	if err = writePythonLauncher(runtime); err != nil {
		return
	}
	// A guarded sampling entry point can still import the ordinary harness.
	// Importing it during replay does not invoke exploration or record traces.
	ordinaryName, ordinaryHarness := Harness("python")
	if err = os.WriteFile(filepath.Join(runtime, ordinaryName), ordinaryHarness, 0644); err != nil {
		return
	}
	name, harness := ReplayHarness(s.language)
	if err = os.WriteFile(filepath.Join(runtime, name), harness, 0644); err != nil {
		return
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return
	}
	if err = os.WriteFile(filepath.Join(request, "request.json"), raw, 0644); err != nil {
		return
	}
	args := []string{"run", "--rm", "--network", "none", "--memory", "2g", "--pids-limit", "512",
		"--security-opt", "no-new-privileges", "-e", "INVARIANT_REPLAY_ISOLATE=1",
		"-e", "HOME=/tmp", "-e", "DRIVER=" + s.driver,
		"-e", "INVARIANT_REPLAY_REQUEST=/replay/request.json",
		"-e", "INVARIANT_REPLAY_RESULT=/out/replay-result.json",
		"-e", "INVARIANT_REPLAY_OBSERVATION=/out/replay-observation.json",
		"-v", src + ":/src", "-v", out + ":/out", "-v", runtime + ":/runtime:ro", "-v", request + ":/replay:ro", "-w", "/src"}
	command := "node /runtime/invariant-replay.ts"
	if s.existing {
		command = `ln -s /deps/node_modules node_modules && if [ -x node_modules/.bin/tsx ]; then node_modules/.bin/tsx /runtime/invariant-replay.ts; else node /runtime/invariant-replay.ts; fi`
	}
	if s.language == "python" {
		args = append(args, "--platform", "linux/amd64", "-e", "PYTHONHASHSEED=0")
		command = "/usr/local/bin/python -S -s -P /runtime/invariant_python.py replay"
	}
	// The supervisor owns /out. Only the disposable source is writable by
	// the unprivileged replay worker; recursive chmod does not follow links.
	script := "exec 2>&1\ncd /src\nchmod -R a+rwX /src && chmod 700 /out && " + command + "\ncode=$?\necho \"@@invariant replay=$code\"\nexit \"$code\"\n"
	args = append(args, s.image, "sh", "-c", script)
	cmd := toolchain.Docker(ctx, args...)
	output, processErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		err = ctx.Err()
		return
	}
	sections, markerErr := splitMarkers(string(output), 1)
	marks := marker.FindAllStringSubmatch(string(output), -1)
	if processErr != nil || markerErr != nil || len(marks) != 1 || marks[0][1] != "replay" || sections[0].code != 0 {
		factory := "invariantReplay"
		if s.language == "python" {
			factory = "invariant_replay"
		}
		err = replayCommandError(factory, string(output), processErr, markerErr)
		if markerErr != nil || len(marks) != 1 || marks[0][1] != "replay" {
			err = errors.Join(err, errReplayCompletion)
		}
		return
	}
	if err = readReplayRecord(out, "replay-result.json", &result); err != nil {
		return
	}
	if err = readReplayRecord(out, "replay-observation.json", &observed); err != nil {
		return
	}
	// Success is bound to the host's process result, never a driver's claim.
	if observed.ExitCode != 0 {
		err = fmt.Errorf("replay observation reports a failed process")
		return
	}
	observed.ExitCode = cmd.ProcessState.ExitCode()
	return
}

// replayCommandError says on one line why a replay's command failed, since
// a replay's message keeps only its first: the process's and the marker's
// errors, then the last lines the driver and the harness printed. The
// frames of a Python traceback or a Node stack are indented, and the cause
// is the line after them, so they're left out, as is the marker.
func replayCommandError(factory, output string, errs ...error) error {
	var reasons []string
	for _, err := range errs {
		if err != nil {
			first, _, _ := strings.Cut(err.Error(), "\n")
			reasons = append(reasons, strings.TrimSuffix(strings.TrimSpace(first), ":"))
		}
	}
	var printed []string
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		text := strings.TrimSpace(line)
		if text != "" && text == strings.TrimRightFunc(line, unicode.IsSpace) && text != "Traceback (most recent call last):" && !marker.MatchString(text) {
			printed = append(printed, text)
		}
	}
	if len(printed) > 4 {
		printed = printed[len(printed)-4:]
	}
	return fmt.Errorf("replay process or completion failed (%s(initial) with read, bind and close is required): %s", factory, strings.Join(append(reasons, printed...), "; "))
}

func readReplayRecord(dir, name string, dst any) (err error) {
	entry, err := os.Lstat(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	if !entry.Mode().IsRegular() {
		return fmt.Errorf("replay output %s is not a regular file", name)
	}
	f, err := openSandboxOutput(dir, name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !os.SameFile(entry, info) {
		return fmt.Errorf("replay output %s changed while opening it", name)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
		return fmt.Errorf("replay output %s is a hard link", name)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	fields := []string{"request_id", "invocation_id", "initial", "calls", "started", "completed", "exit_code"}
	if _, ok := dst.(*BugReplayResult); ok {
		fields = []string{"version", "request_id", "invocation_id", "outcome", "initial", "calls"}
	}
	// Check surrogate escapes, repeated keys and UTF-8 before decoding can
	// discard them, then validate nested field names and nulls before Go's
	// struct decoder can accept aliases or turn null into a successful zero.
	if err = checkReplayJSONUnicode(b); err != nil {
		return err
	}
	object, err := pythonApplicationJSONFields(b, true, fields...)
	if err != nil {
		return err
	}
	for _, key := range fields {
		if bytes.Equal(object[key], []byte("null")) {
			return fmt.Errorf("null replay field %s", key)
		}
	}
	if err = checkReplayJSONState(object["initial"]); err != nil {
		return err
	}
	var calls []json.RawMessage
	if err = json.Unmarshal(object["calls"], &calls); err != nil {
		return err
	}
	for _, call := range calls {
		fields, err := replayJSONFields(call, []string{"index", "action", "args", "before", "after", "dispatched", "outcome"}, "error")
		if err != nil {
			return err
		}
		for _, key := range []string{"before", "after"} {
			if err := checkReplayJSONState(fields[key]); err != nil {
				return err
			}
		}
		if err := checkReplayJSONValues(fields["args"]); err != nil {
			return err
		}
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(dst); err != nil {
		return err
	}
	return nil
}

// encoding/json replaces unpaired UTF-16 surrogates with U+FFFD. Evidence
// must retain exact strings, including keys, before any semantic comparison.
// JSON syntax and UTF-8 validation are still performed by the caller.
func checkReplayJSONUnicode(b []byte) error {
	quoted := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || b[i] != '\\' {
			continue
		}
		i++
		if i >= len(b) || b[i] != 'u' || i+4 >= len(b) {
			continue
		}
		u, err := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
		if err != nil {
			return fmt.Errorf("invalid replay Unicode escape")
		}
		i += 4
		if u < 0xd800 || u > 0xdfff {
			continue
		}
		if u > 0xdbff || i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
			return fmt.Errorf("unpaired replay Unicode surrogate")
		}
		low, err := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("unpaired replay Unicode surrogate")
		}
		i += 6
	}
	return nil
}

func replayJSONFields(b []byte, required []string, optional ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("missing replay field %s", key)
		}
	}
	for key, value := range fields {
		if !slices.Contains(required, key) && !slices.Contains(optional, key) {
			return nil, fmt.Errorf("unexpected replay field %s", key)
		}
		// Only a call's empty argument list may be spelled null. Optional
		// value fields may be omitted, but must have their type when present.
		if key != "args" && bytes.Equal(value, []byte("null")) {
			return nil, fmt.Errorf("null replay field %s", key)
		}
	}
	return fields, nil
}

func checkReplayJSONState(b []byte) error {
	var state map[string]json.RawMessage
	if err := json.Unmarshal(b, &state); err != nil {
		return err
	}
	if state == nil {
		return fmt.Errorf("null replay state")
	}
	for _, value := range state {
		if err := checkReplayJSONValue(value); err != nil {
			return err
		}
	}
	return nil
}

func checkReplayJSONValues(b []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(b, &values); err != nil {
		return err
	}
	for _, value := range values {
		if err := checkReplayJSONValue(value); err != nil {
			return err
		}
	}
	return nil
}

func checkReplayJSONValue(b []byte) error {
	fields, err := replayJSONFields(b, []string{"kind"}, "atom", "elements", "entries")
	if err != nil {
		return err
	}
	for key, value := range fields {
		switch key {
		case "kind", "atom":
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				return err
			}
		case "elements":
			if err := checkReplayJSONValues(value); err != nil {
				return err
			}
		case "entries":
			var entries []json.RawMessage
			if err := json.Unmarshal(value, &entries); err != nil {
				return err
			}
			for _, entry := range entries {
				pair, err := replayJSONFields(entry, []string{"key", "value"})
				if err != nil {
					return err
				}
				for _, item := range pair {
					if err := checkReplayJSONValue(item); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
