package verify

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"syscall"
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
	Image          string            `json:"image"`
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
	// A trusted contract's backing test also records how many calls reached
	// the implementation its run resolved, and each outermost unittest
	// assertion it made, in order (#294).
	BoundaryCalls int                          `json:"boundary_calls,omitempty"`
	Assertions    []PythonApplicationAssertion `json:"assertions,omitempty"`
}

// PythonApplicationAssertion is the runner's record of one unittest assertion:
// passed, failed or error, as it returned or raised.
type PythonApplicationAssertion struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
}

// PythonApplicationBoundary is the implementation a trusted contract's
// backing-test run resolved before any test loaded (#294): the application's
// declaration in its staged source, or the standard library's or an installed
// distribution's code, with its file and, for a distribution, its installed
// name, version and whether its RECORD lists the file's bytes.
type PythonApplicationBoundary struct {
	Contract     string                `json:"contract"`
	Kind         string                `json:"kind"`
	Resolved     bool                  `json:"resolved"`
	Callable     string                `json:"callable"`
	File         project.ComponentFile `json:"file"`
	Distribution string                `json:"distribution"`
	Version      string                `json:"version"`
	Recorded     bool                  `json:"recorded"`
	Python       string                `json:"python"`
	Diagnostics  []string              `json:"diagnostics"`
}

// pythonApplicationContractRequest tells the runner what a backing-test run
// resolves before its tests load.
type pythonApplicationContractRequest struct {
	Contract     string             `json:"contract"`
	Kind         string             `json:"kind"`
	Module       string             `json:"module,omitempty"`
	Symbol       string             `json:"symbol,omitempty"`
	Distribution string             `json:"distribution,omitempty"`
	Declaration  *project.Component `json:"declaration,omitempty"`
}

type PythonApplicationExecutionResult struct {
	Key             PythonApplicationInvocationKey      `json:"key"`
	Declared        project.ResolvedComponentInvocation `json:"declared"`
	Actual          PythonApplicationActual             `json:"actual"`
	Provenance      PythonApplicationProvenance         `json:"provenance"`
	Tests           []PythonApplicationTest             `json:"tests"`
	Boundary        *PythonApplicationBoundary          `json:"boundary,omitempty"`
	ObservationMode string                              `json:"observation_mode,omitempty"`
	Observations    []PythonApplicationObservation      `json:"observations,omitempty"`
	Complete        bool                                `json:"complete"`
	Success         bool                                `json:"success"`
	ExitCode        int                                 `json:"exit_code"`
	// Operational diagnostics may contain host paths or container IDs.
	Diagnostics []string `json:"diagnostics,omitempty"`
	Fingerprint string   `json:"fingerprint"`
}

type pythonApplicationRequest struct {
	Legacy         bool                           `json:"legacy,omitempty"`
	Targets        []project.Component            `json:"targets,omitempty"`
	Drivers        []project.Component            `json:"drivers,omitempty"`
	Delegates      []string                       `json:"delegates,omitempty"`
	Key            PythonApplicationInvocationKey `json:"key"`
	SnapshotSHA256 string                         `json:"snapshot_sha256"`
	RunnerSHA256   string                         `json:"runner_sha256"`
	Check          project.Component              `json:"check"`
	Root           string                         `json:"root"`
	Driver         string                         `json:"driver"`
	Output         string                         `json:"output"`
	Libraries      string                         `json:"libraries"`
	Target         project.Component              `json:"target"`
	Sampling       map[string]string              `json:"sampling"`
	Execution      string                         `json:"execution"`
	// Boundary is set for a trusted contract's backing-test run alone.
	Boundary *pythonApplicationContractRequest `json:"boundary,omitempty"`
}

var pythonApplicationContainerName = regexp.MustCompile(`invariant-[0-9]+-[0-9]+-[0-9]+`)

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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.fresh(); err != nil {
		return nil, err
	}
	runner, err := os.ReadFile(filepath.Join(s.runtime, "runner.py"))
	if err != nil || pythonApplicationHash(runner) != s.provenance.RunnerSHA256 {
		return nil, fmt.Errorf("Python application runner changed")
	}
	if err := s.freshObserver(); err != nil {
		return nil, err
	}
	output, err := os.MkdirTemp("", "invariant-python-output-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(output)
	v := invocation.Declared
	env := map[string]string{"PYTHONHASHSEED": "0", "PYTHONDONTWRITEBYTECODE": "1", "PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/tmp", "LANG": "C.UTF-8"}
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "FTP_PROXY", "ftp_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		env[k] = ""
	}
	maps.Copy(env, v.Env)
	c := PythonApplicationCommand{Key: key, SnapshotSHA256: s.provenance.Fingerprint, RunnerSHA256: s.provenance.RunnerSHA256,
		Image: s.image, Interpreter: "/usr/local/bin/python", Launcher: "/runtime/runner.py", WorkingDir: path.Join("/src", v.WorkingDir),
		Env: env, Sampling: maps.Clone(invocation.Sampling), Root: s.root, RuntimeDir: s.runtime, OutputDir: output}
	c.Args = append([]string{"-S", "-s", "-P", "-B", c.Launcher, "/runtime/request.json"}, v.Args...)
	req := pythonApplicationRequest{Key: key, SnapshotSHA256: c.SnapshotSHA256, RunnerSHA256: c.RunnerSHA256,
		Legacy: s.legacy, Targets: s.targets, Drivers: s.drivers, Delegates: s.delegates[key.Target],
		Check: s.declarations[key.Check], Root: "/src", Driver: s.driver, Output: "/output/events.jsonl", Libraries: toolchain.PythonLibrary,
		Target: s.declarations[key.Target], Sampling: maps.Clone(c.Sampling), Execution: rand.Text()}
	if s.legacy {
		req.Check = s.drivers[0]
	}
	if contract, ok := s.contracts[key.Target]; ok {
		o := contract.Operation
		req.Boundary = &pythonApplicationContractRequest{Contract: contract.ID, Kind: o.Kind, Module: o.Module, Symbol: o.Symbol, Distribution: o.Distribution}
		if o.Kind == project.OperationApplication {
			d := s.declarations[o.Declaration]
			req.Boundary.Declaration = &d
		}
	}
	// The runner consumes and unlinks this bootstrap secret before importing
	// application code. It never belongs to the application-readable request.
	var authentication []byte
	if key.Kind == "conformance" {
		authentication = make([]byte, 32)
		if _, err := rand.Read(authentication); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(output, "authentication.key"), authentication, 0o600); err != nil {
			return nil, err
		}
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(s.runtime, "request.json"), b, 0o644); err != nil {
		return nil, err
	}
	r := &PythonApplicationExecutionResult{Key: key, Declared: v, Provenance: clonePythonApplicationProvenance(s.provenance),
		Actual: PythonApplicationActual{c.Image, c.Interpreter, c.Launcher, c.RunnerSHA256, slices.Clone(c.Args), c.WorkingDir, maps.Clone(c.Env), maps.Clone(c.Sampling)}, Tests: []PythonApplicationTest{}}
	// The executor owns its command view; mutations cannot alter validation.
	command := c
	command.Args, command.Env, command.Sampling = slices.Clone(c.Args), maps.Clone(c.Env), maps.Clone(c.Sampling)
	process, executionErr := s.execute(ctx, command)
	r.ExitCode = process.ExitCode
	var protocolErr error
	if key.Kind == "conformance" {
		protocolErr = s.observeTransitions(c, req.Execution, authentication, process.Events, r)
	} else {
		protocolErr = s.observe(c, req.Boundary, process.Events, r)
	}
	freshErr := s.fresh()
	freshErr = errors.Join(freshErr, s.freshObserver())
	afterRunner, runnerErr := os.ReadFile(filepath.Join(s.runtime, "runner.py"))
	if runnerErr != nil || pythonApplicationHash(afterRunner) != c.RunnerSHA256 {
		freshErr = fmt.Errorf("Python application runner changed during execution")
	}
	afterRequest, requestErr := os.ReadFile(filepath.Join(s.runtime, "request.json"))
	if requestErr != nil || !bytes.Equal(b, afterRequest) {
		freshErr = fmt.Errorf("Python application request changed during execution")
	}
	if ctx.Err() != nil {
		executionErr = ctx.Err()
	}
	if executionErr != nil || freshErr != nil {
		r.Complete = false
	}
	if process.Stdout != "" {
		r.Diagnostics = append(r.Diagnostics, process.Stdout)
	}
	if process.Stderr != "" {
		r.Diagnostics = append(r.Diagnostics, process.Stderr)
	}
	clean := func(diagnostic string) string {
		diagnostic = strings.NewReplacer(c.Root, "/src", c.RuntimeDir, "/runtime", c.OutputDir, "/output").Replace(diagnostic)
		diagnostic = strings.ReplaceAll(diagnostic, req.Execution, "<execution>")
		if strings.HasPrefix(c.Image, "sha256:") {
			diagnostic = strings.ReplaceAll(diagnostic, c.Image, "<selected image>")
		}
		return pythonApplicationContainerName.ReplaceAllString(diagnostic, "<container>")
	}
	normalize := func() {
		for i, diagnostic := range r.Diagnostics {
			r.Diagnostics[i] = clean(diagnostic)
		}
		if r.Boundary != nil {
			for i, diagnostic := range r.Boundary.Diagnostics {
				r.Boundary.Diagnostics[i] = clean(diagnostic)
			}
		}
	}
	if key.Kind == "conformance" {
		err = errors.Join(executionErr, protocolErr, freshErr)
		r.Success = err == nil && r.Complete && process.ExitCode == 0 && len(r.Observations) > 0
		if r.Success {
			r.ObservationMode = "sampled"
		} else {
			r.Complete = false
			r.Observations = nil
			if err == nil {
				err = fmt.Errorf("Python application driver did not complete successfully")
			}
			r.Diagnostics = append(r.Diagnostics, err.Error())
		}
		normalize()
		r.Fingerprint = pythonApplicationExecutionFingerprint(*r)
		return r, err
	}
	observed, passing := false, len(r.Tests) > 0
	failed := false
	for _, row := range r.Tests {
		if row.Check == key.Check || s.legacy && row.Check != "" {
			observed = true
		}
		passing = passing && row.Outcome == "passed" && row.Exercised && (!s.legacy || row.Check != "")
		failed = failed || row.Outcome == "failed" || row.Outcome == "error" || row.Outcome == "unexpected_success"
	}
	if !observed {
		r.Tests = append(r.Tests, PythonApplicationTest{Check: key.Check, SourceSHA256: s.declarations[key.Check].SourceSHA256, Outcome: "not_run", Diagnostics: []string{"required test was not observed"}})
		passing = false
	}
	if r.Complete && ((failed && process.ExitCode != 1) || (!failed && process.ExitCode != 0)) {
		protocolErr = fmt.Errorf("Python application exit status contradicts observations")
		r.Complete = false
	}
	r.Success = r.Complete && passing && observed && process.ExitCode == 0
	err = errors.Join(executionErr, protocolErr, freshErr)
	if err != nil {
		r.Diagnostics = append(r.Diagnostics, err.Error())
	}
	normalize()
	r.Fingerprint = pythonApplicationExecutionFingerprint(*r)
	return r, err
}

// RunPythonApplicationCommand is the production executor, also usable by
// recording wrappers. All declaration values remain individual argv values.
func RunPythonApplicationCommand(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
	args := []string{"run", "--rm", "--pull=never", "--network", "none", "--platform", "linux/amd64",
		"--memory", "512m", "--pids-limit", "128", "--cpus", "1", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--hostname", "localhost", "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m", "-w", c.WorkingDir,
		"-v", c.Root + ":/src:ro", "-v", c.RuntimeDir + ":/runtime:ro", "-v", c.OutputDir + ":/output:rw",
		"--entrypoint", "/usr/bin/env", c.Image, "-i"}
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, k+"="+c.Env[k])
	}
	args = append(args, c.Interpreter)
	args = append(args, c.Args...)
	cmd := toolchain.Docker(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	p := PythonApplicationProcess{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: -1}
	if cmd.ProcessState != nil {
		p.ExitCode = cmd.ProcessState.ExitCode()
	}
	var eventsErr error
	p.Events, eventsErr = readPythonApplicationEvents(c.OutputDir)
	if err != nil {
		err = fmt.Errorf("running selected Python application image: %w", err)
	}
	return p, errors.Join(err, eventsErr)
}

const pythonApplicationEventsLimit = 16 << 20

func readPythonApplicationEvents(dir string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	// Output belongs to the application. Refuse links atomically and avoid
	// blocking on a pipe before checking the opened file's type.
	f, err := root.OpenFile("events.jsonl", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("opening Python application events: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Python application events must be a regular file")
	}
	// Retain the bounded prefix even when Stat already reports an oversized
	// file, so complete preceding observations survive a large diagnostic.
	b, err := io.ReadAll(io.LimitReader(f, pythonApplicationEventsLimit+1))
	if len(b) > pythonApplicationEventsLimit {
		return b[:pythonApplicationEventsLimit], fmt.Errorf("Python application events exceed %d bytes", pythonApplicationEventsLimit)
	}
	return b, err
}

// Validate structure before decoding into Go zero values. Duplicate object
// keys, including nested keys, are never allowed to replace earlier evidence.
func pythonApplicationJSON(b []byte, required ...string) (map[string]json.RawMessage, error) {
	return pythonApplicationJSONFields(b, false, required...)
}

func pythonApplicationJSONFields(b []byte, allowNull bool, required ...string) (map[string]json.RawMessage, error) {
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("event is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value func() error
	value = func() error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := t.(json.Delim)
		if !ok {
			if t == nil && !allowNull {
				return fmt.Errorf("null event field")
			}
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				k, ok := key.(string)
				if !ok || seen[k] {
					return fmt.Errorf("duplicate event field")
				}
				seen[k] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("invalid event JSON")
		}
		_, err = d.Token()
		return err
	}
	if err := value(); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing event JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	if len(fields) != len(required) {
		return nil, fmt.Errorf("unexpected or missing event fields")
	}
	for _, k := range required {
		if _, ok := fields[k]; !ok {
			return nil, fmt.Errorf("missing event field %s", k)
		}
	}
	return fields, nil
}

func (s *PythonApplicationSession) observe(c PythonApplicationCommand, boundary *pythonApplicationContractRequest, events []byte, r *PythonApplicationExecutionResult) error {
	seen := map[string]bool{}
	begun, ended := false, false
	testFields := []string{"type", "id", "check", "source_sha256", "exercised", "outcome", "diagnostics"}
	if boundary != nil {
		testFields = append(testFields, "boundary_calls", "assertions")
	}
	for len(events) > 0 {
		n := bytes.IndexByte(events, '\n')
		if n < 0 {
			return fmt.Errorf("truncated Python application event")
		}
		line := events[:n]
		events = events[n+1:]
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &kind); err != nil {
			return fmt.Errorf("malformed Python application event: %w", err)
		}
		if ended {
			return fmt.Errorf("event follows end")
		}
		switch kind.Type {
		case "begin":
			fields, err := pythonApplicationJSON(line, "type", "version", "key", "snapshot_sha256", "runner_sha256")
			if err != nil {
				return err
			}
			if _, err := pythonApplicationJSON(fields["key"], "target", "check", "kind", "id"); err != nil {
				return err
			}
			var b struct {
				Version  int                            `json:"version"`
				Key      PythonApplicationInvocationKey `json:"key"`
				Snapshot string                         `json:"snapshot_sha256"`
				Runner   string                         `json:"runner_sha256"`
			}
			if err := json.Unmarshal(line, &b); err != nil {
				return err
			}
			if begun || b.Version != 1 || b.Key != c.Key || b.Snapshot != c.SnapshotSHA256 || b.Runner != c.RunnerSHA256 {
				return fmt.Errorf("foreign or duplicate begin record")
			}
			begun = true
		case "boundary":
			if boundary == nil || !begun || r.Boundary != nil || len(r.Tests) > 0 {
				return fmt.Errorf("unexpected boundary record")
			}
			resolved, err := s.boundary(c, boundary, line)
			if err != nil {
				return err
			}
			r.Boundary = resolved
		case "test":
			if !begun {
				return fmt.Errorf("test before begin")
			}
			if boundary != nil && r.Boundary == nil {
				return fmt.Errorf("test before its contract's boundary record")
			}
			if _, err := pythonApplicationJSON(line, testFields...); err != nil {
				return err
			}
			var row PythonApplicationTest
			if err := json.Unmarshal(line, &row); err != nil {
				return err
			}
			if boundary != nil {
				if err := pythonApplicationBoundaryTest(line, r.Boundary, &row); err != nil {
					return err
				}
			}
			if row.ID == "" || seen[row.ID] {
				return fmt.Errorf("missing or duplicate runtime test ID")
			}
			if !s.legacy && row.Check != "" && (row.Check != c.Key.Check || row.SourceSHA256 != s.declarations[c.Key.Check].SourceSHA256) {
				return fmt.Errorf("foreign test declaration or source")
			}
			if s.legacy && row.Check != "" {
				prefix, source, ok := strings.Cut(row.Check, "@")
				module, symbol, named := strings.Cut(prefix, "#")
				if !ok || !named || symbol == "" || !strings.HasSuffix(source, ".py") || module != "python:"+strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".") || s.snapshot[source].hash != row.SourceSHA256 {
					return fmt.Errorf("foreign discovered test declaration or source")
				}
			}
			if row.SourceSHA256 != "" {
				known := false
				for _, f := range s.provenance.Files {
					known = known || f.SHA256 == row.SourceSHA256
				}
				if !known {
					return fmt.Errorf("unknown test source")
				}
			}
			switch row.Outcome {
			case "passed", "expected_failure", "unexpected_success":
				if !row.Exercised {
					return fmt.Errorf("unexercised test has an executed outcome")
				}
			case "skipped", "not_run":
				if row.Exercised {
					return fmt.Errorf("unexecuted test credited as exercised")
				}
			case "failed", "error":
				// Fixtures can fail before the test method is exercised.
			default:
				return fmt.Errorf("invalid test outcome")
			}
			seen[row.ID] = true
			for i, diagnostic := range row.Diagnostics {
				row.Diagnostics[i] = strings.NewReplacer(c.Root, "/src", c.RuntimeDir, "/runtime", c.OutputDir, "/output").Replace(diagnostic)
			}
			r.Tests = append(r.Tests, row)
		case "end":
			if !begun {
				return fmt.Errorf("end before begin")
			}
			if _, err := pythonApplicationJSON(line, "type", "count"); err != nil {
				return err
			}
			var e struct {
				Count int `json:"count"`
			}
			if err := json.Unmarshal(line, &e); err != nil {
				return err
			}
			if e.Count != len(r.Tests) {
				return fmt.Errorf("test count mismatch")
			}
			if boundary != nil && r.Boundary == nil {
				return fmt.Errorf("missing contract boundary record")
			}
			ended = true
		default:
			return fmt.Errorf("unknown Python application event")
		}
	}
	if !ended {
		return fmt.Errorf("missing Python application end record")
	}
	r.Complete = true
	return nil
}
