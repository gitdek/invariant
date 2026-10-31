package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"unicode/utf8"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

type PythonApplicationCommand struct {
	Key                          PythonApplicationInvocationKey
	SnapshotSHA256, RunnerSHA256 string
	Image, Interpreter, Launcher string
	Args                         []string
	WorkingDir                   string
	Env, Sampling                map[string]string
	Root, RuntimeDir, OutputDir  string
}

type PythonApplicationProcess struct {
	Events         []byte
	Stdout, Stderr string
	ExitCode       int
}

type PythonApplicationActual struct {
	Image          string            `json:"-"`
	Interpreter    string            `json:"interpreter"`
	Launcher       string            `json:"launcher"`
	LauncherSHA256 string            `json:"launcher_sha256"`
	Args           []string          `json:"args"`
	WorkingDir     string            `json:"working_dir"`
	Env            map[string]string `json:"env"`
	Sampling       map[string]string `json:"sampling"`
}

type PythonApplicationTest struct {
	ID           string   `json:"id"`
	Check        string   `json:"check"`
	SourceSHA256 string   `json:"source_sha256"`
	Exercised    bool     `json:"exercised"`
	Outcome      string   `json:"outcome"`
	Diagnostics  []string `json:"diagnostics"`
}

type PythonApplicationExecutionResult struct {
	Key        PythonApplicationInvocationKey      `json:"key"`
	Declared   project.ResolvedComponentInvocation `json:"declared"`
	Actual     PythonApplicationActual             `json:"actual"`
	Provenance PythonApplicationProvenance         `json:"provenance"`
	Tests      []PythonApplicationTest             `json:"tests"`
	Complete   bool                                `json:"complete"`
	Success    bool                                `json:"success"`
	ExitCode   int                                 `json:"exit_code"`
	// Process output and operational errors can contain host paths and IDs.
	// They are available to callers but excluded from portable evidence.
	Diagnostics []string `json:"-"`
	Fingerprint string   `json:"fingerprint"`
}

func (s *PythonApplicationSession) Run(ctx context.Context, key PythonApplicationInvocationKey) (*PythonApplicationExecutionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("Python application session is closed")
	}
	var invocation *PythonApplicationInvocation
	for _, v := range s.invocations {
		if v.Key == key {
			copy := clonePythonApplicationInvocation(v)
			invocation = &copy
			break
		}
	}
	if invocation == nil {
		return nil, fmt.Errorf("unknown Python application invocation")
	}
	if key.Kind != "test" {
		return nil, fmt.Errorf("Python application %s execution is unsupported", key.Kind)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.fresh(); err != nil {
		return nil, err
	}
	runtime, err := os.MkdirTemp(s.private, "runtime-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(runtime)
	output, err := os.MkdirTemp(s.private, "output-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(output)
	env := maps.Clone(invocation.Declared.Env)
	if env == nil {
		env = map[string]string{}
	}
	for k, v := range map[string]string{"PYTHONHASHSEED": "0", "PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/tmp"} {
		env[k] = v
	}
	if _, declared := env["LANG"]; !declared {
		env["LANG"] = "C.UTF-8"
	}
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "FTP_PROXY", "ftp_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		env[k] = ""
	}
	c := PythonApplicationCommand{
		Key: key, SnapshotSHA256: s.provenance.Fingerprint, RunnerSHA256: s.provenance.RunnerSHA256,
		Image: s.image, Interpreter: "/usr/local/bin/python", Launcher: "/runtime/python_application_runner.py",
		WorkingDir: path.Join("/src", invocation.Declared.WorkingDir), Env: env, Sampling: maps.Clone(invocation.Sampling),
		Root: s.root, RuntimeDir: runtime, OutputDir: output,
	}
	c.Args = append([]string{"-S", "-s", "-P", "-B", c.Launcher, "/runtime/request.json"}, invocation.Declared.Args...)
	config := struct {
		Key         PythonApplicationInvocationKey `json:"key"`
		Snapshot    string                         `json:"snapshot_sha256"`
		Runner      string                         `json:"runner_sha256"`
		Declaration project.Component              `json:"declaration"`
		Driver      string                         `json:"driver"`
		Library     string                         `json:"library"`
	}{key, c.SnapshotSHA256, c.RunnerSHA256, s.declarations[key.Check], s.driver, toolchain.PythonLibrary}
	b, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(runtime, "request.json"), b, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(runtime, "python_application_runner.py"), pythonApplicationRunner, 0o644); err != nil {
		return nil, err
	}
	runtimeBefore, err := pythonApplicationSnapshot(runtime)
	if err != nil {
		return nil, err
	}
	r := &PythonApplicationExecutionResult{
		Key: key, Declared: invocation.Declared, Provenance: clonePythonApplicationProvenance(s.provenance), Tests: []PythonApplicationTest{},
		Actual: PythonApplicationActual{Image: c.Image, Interpreter: c.Interpreter, Launcher: c.Launcher, LauncherSHA256: c.RunnerSHA256,
			Args: slices.Clone(c.Args), WorkingDir: c.WorkingDir, Env: maps.Clone(c.Env), Sampling: maps.Clone(c.Sampling)},
	}
	// The executor gets its own mutable argument containers. It cannot rewrite
	// the request used to validate its returned stream or the session's views.
	command := c
	command.Args, command.Env, command.Sampling = slices.Clone(c.Args), maps.Clone(c.Env), maps.Clone(c.Sampling)
	process, executionErr := s.execute(ctx, command)
	// A completed process can fail its tests. Only transport failures or
	// cancellation make an otherwise complete event stream incomplete.
	var exitError *exec.ExitError
	processFinished := errors.As(executionErr, &exitError) && process.ExitCode >= 0 && ctx.Err() == nil
	r.ExitCode = process.ExitCode
	if process.Stdout != "" {
		r.Diagnostics = append(r.Diagnostics, process.Stdout)
	}
	if process.Stderr != "" {
		r.Diagnostics = append(r.Diagnostics, process.Stderr)
	}
	protocolErr := s.observations(c, process.Events, r)
	freshErr := s.fresh()
	runtimeCheck := &PythonApplicationSession{root: runtime, snapshot: runtimeBefore}
	runtimeErr := runtimeCheck.fresh()
	err = errors.Join(executionErr, protocolErr, freshErr, runtimeErr, ctx.Err())
	if process.ExitCode != 0 {
		err = errors.Join(err, fmt.Errorf("Python application process exited %d", process.ExitCode))
	}
	if err != nil {
		r.Diagnostics = append(r.Diagnostics, err.Error())
		if protocolErr != nil || freshErr != nil || runtimeErr != nil || (executionErr != nil && !processFinished) || ctx.Err() != nil {
			r.Complete = false
		}
	}
	// Missing selections are explicitly uncredited even after a valid end.
	found, passed := false, true
	for _, row := range r.Tests {
		if row.Check == key.Check {
			found = true
		}
		if row.Outcome != "passed" || !row.Exercised {
			passed = false
		}
	}
	if !found {
		r.Tests = append(r.Tests, PythonApplicationTest{Check: key.Check, SourceSHA256: s.declarations[key.Check].SourceSHA256, Outcome: "not_run", Diagnostics: []string{"required declaration was not observed"}})
	}
	r.Success = err == nil && r.Complete && found && passed
	r.Fingerprint = pythonApplicationFingerprint(r)
	return r, err
}

// RunPythonApplicationCommand executes only the prepared immutable image. The
// clean environment also excludes image-provided startup settings. Docker's
// cancellation handler removes the named container before killing the client.
func RunPythonApplicationCommand(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
	var result PythonApplicationProcess
	if c.Image != toolchain.PythonImage && !pythonApplicationHash.MatchString(c.Image) {
		return result, fmt.Errorf("Python execution requires an immutable image")
	}
	args := []string{"run", "--rm", "--pull=never", "--network", "none", "--platform", "linux/amd64", "--memory", "512m", "--pids-limit", "128", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,nosuid,nodev,size=64m",
		"-v", c.Root + ":/src:ro", "-v", c.RuntimeDir + ":/runtime:ro", "-v", c.OutputDir + ":/output:rw", "-w", c.WorkingDir, "--entrypoint", "/usr/bin/env", c.Image, "-i"}
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		args = append(args, k+"="+c.Env[k])
	}
	args = append(args, c.Interpreter)
	args = append(args, c.Args...)
	cmd := toolchain.Docker(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	result.ExitCode = -1
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	filename := filepath.Join(c.OutputDir, "events.jsonl")
	info, readErr := os.Lstat(filename)
	if readErr == nil {
		if !info.Mode().IsRegular() {
			readErr = fmt.Errorf("Python event output is not a regular file")
		} else {
			var file *os.File
			file, readErr = os.Open(filename)
			if readErr == nil {
				opened, statErr := file.Stat()
				if statErr != nil || !os.SameFile(info, opened) {
					readErr = fmt.Errorf("Python event output was replaced")
				} else {
					result.Events, readErr = io.ReadAll(file)
				}
				readErr = errors.Join(readErr, file.Close())
			}
		}
	}
	if readErr != nil {
		err = errors.Join(err, fmt.Errorf("reading Python events: %w", readErr))
	}
	return result, err
}

// Decode an object without JSON's usual last-key-wins behavior. Missing/null
// fields are rejected before typed decoding, including nested invocation keys.
func pythonApplicationRecord(b []byte, fields ...string) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("event must be an object")
	}
	m := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok || m[name] != nil {
			return nil, fmt.Errorf("duplicate event field")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("null event field %s", name)
		}
		m[name] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing event data")
	}
	if len(m) != len(fields) {
		return nil, fmt.Errorf("unexpected or missing event fields")
	}
	for _, field := range fields {
		if m[field] == nil {
			return nil, fmt.Errorf("missing event field %s", field)
		}
	}
	return m, nil
}

func (s *PythonApplicationSession) observations(c PythonApplicationCommand, events []byte, r *PythonApplicationExecutionResult) error {
	bad := func() error { return fmt.Errorf("invalid or incomplete Python application events") }
	lines := bytes.Split(events, []byte{'\n'})
	seen := map[string]bool{}
	begun, ended := false, false
	for i, line := range lines {
		if i == len(lines)-1 {
			if len(line) != 0 {
				return bad()
			}
			break
		}
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &kind); err != nil || ended || !utf8.Valid(line) {
			return bad()
		}
		switch kind.Type {
		case "begin":
			m, err := pythonApplicationRecord(line, "type", "version", "key", "snapshot_sha256", "runner_sha256")
			if err != nil || begun || i != 0 {
				return bad()
			}
			if _, err := pythonApplicationRecord(m["key"], "target", "check", "kind", "id"); err != nil {
				return bad()
			}
			var begin struct {
				Version  int                            `json:"version"`
				Key      PythonApplicationInvocationKey `json:"key"`
				Snapshot string                         `json:"snapshot_sha256"`
				Runner   string                         `json:"runner_sha256"`
			}
			if json.Unmarshal(line, &begin) != nil || begin.Version != 1 || begin.Key != c.Key || begin.Snapshot != c.SnapshotSHA256 || begin.Runner != c.RunnerSHA256 {
				return bad()
			}
			begun = true
		case "test":
			if !begun {
				return bad()
			}
			if _, err := pythonApplicationRecord(line, "type", "id", "check", "source_sha256", "exercised", "outcome", "diagnostics"); err != nil {
				return bad()
			}
			var row PythonApplicationTest
			if json.Unmarshal(line, &row) != nil || row.ID == "" || seen[row.ID] {
				return bad()
			}
			// null diagnostics entries cannot silently become empty strings.
			var diagnostics struct {
				Values []*string `json:"diagnostics"`
			}
			if json.Unmarshal(line, &diagnostics) != nil {
				return bad()
			}
			for _, v := range diagnostics.Values {
				if v == nil {
					return bad()
				}
			}
			if row.Check != "" && (row.Check != c.Key.Check || row.SourceSHA256 != s.declarations[c.Key.Check].SourceSHA256) {
				return bad()
			}
			if row.SourceSHA256 != "" {
				known := false
				for _, f := range s.provenance.Files {
					if f.SHA256 == row.SourceSHA256 {
						known = true
						break
					}
				}
				if !known {
					return bad()
				}
			}
			switch row.Outcome {
			case "passed", "failed", "expected_failure", "unexpected_success":
				if !row.Exercised {
					return bad()
				}
			case "not_run":
				if row.Exercised {
					return bad()
				}
			case "skipped":
				// skipTest and skipped subtests may occur inside an entered method.
			case "error":
			default:
				return bad()
			}
			seen[row.ID] = true
			r.Tests = append(r.Tests, row)
		case "end":
			if !begun {
				return bad()
			}
			if _, err := pythonApplicationRecord(line, "type", "count"); err != nil {
				return bad()
			}
			var end struct {
				Count int `json:"count"`
			}
			if json.Unmarshal(line, &end) != nil || end.Count != len(r.Tests) {
				return bad()
			}
			ended = true
		default:
			return bad()
		}
	}
	if !begun || !ended {
		return bad()
	}
	// unittest reports these outcomes with a failing process, even when some
	// other tests passed. A zero exit contradicts the reported execution.
	for _, row := range r.Tests {
		if (row.Outcome == "failed" || row.Outcome == "error" || row.Outcome == "unexpected_success") && r.ExitCode == 0 {
			return bad()
		}
	}
	r.Complete = true
	return nil
}
