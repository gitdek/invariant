package plumbing

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/repository"
)

// PytestRepositoryAcceptanceAdapter admits literal node IDs under captured
// command policy. Collection and runtime evidence belong to the private runner.
type PytestRepositoryAcceptanceAdapter struct{}

var pytestIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]*$`)
var pytestPlugin = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]*(\.[A-Za-z_][A-Za-z_0-9]*)*$`)

func (PytestRepositoryAcceptanceAdapter) Select(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
	fail := func(reason string) (RepositoryAcceptanceSelection, error) {
		return RepositoryAcceptanceSelection{}, errors.New(reason)
	}
	if err := ctx.Err(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	definition, _ := lookupAcceptanceFramework("pytest")
	if part.Language != definition.language || test.Framework != "pytest" || part.Name != test.Part {
		return fail("pytest acceptance needs its captured Python owner and framework")
	}
	if err := test.validateNative(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	file, err := goAcceptanceFile(part, test.File)
	if err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	nodeFile, node, found := strings.Cut(test.Selector, "::")
	declaration, parameter, parametrized := strings.Cut(node, "[")
	names := strings.Split(declaration, "::")
	if !found || nodeFile != file || !repositoryAcceptancePath(file) || len(names) > 2 || parametrized && !strings.HasSuffix(parameter, "]") {
		return fail("pytest acceptance needs one exact part-relative function, method or concrete parameter node ID")
	}
	for i, name := range names {
		if !pytestIdentifier.MatchString(name) || i == len(names)-1 && !strings.HasPrefix(name, "test") {
			return fail("pytest acceptance requires a discoverable function or method")
		}
	}
	command := part.Commands["test_one"]
	argv := slices.Clone(command.Argv)
	start := pytestCommandStart(argv)
	if command.NotApplicable != "" || start == 0 {
		return fail("pytest acceptance supports only direct pytest or python3 -m pytest")
	}
	selected := false
	seen := map[string]bool{}
	for i := start; i < len(argv); i++ {
		arg := argv[i]
		if !nativeText(arg) {
			return fail("pytest command contains an empty or invisible argument")
		}
		if arg == "{test}" {
			if selected {
				return fail("pytest acceptance requires exactly one placeholder")
			}
			selected, argv[i] = true, test.Selector
			continue
		}
		flag, value, equal := strings.Cut(arg, "=")
		if seen[flag] && flag != "-p" {
			return fail("pytest refuses repeated flag " + flag)
		}
		seen[flag] = true
		boolean := false
		switch flag {
		case "-q", "-v", "-s", "--strict-markers", "--strict-config", "--disable-warnings", "--showlocals":
			boolean = true
		case "--tb", "--capture", "--color", "--import-mode", "-p", "-c":
		default:
			return fail("unsupported pytest argument " + arg)
		}
		if boolean {
			if equal {
				return fail("pytest boolean flag cannot have a value")
			}
			continue
		}
		if !equal {
			i++
			if i == len(argv) {
				return fail("pytest flag needs a value: " + flag)
			}
			value = argv[i]
		}
		valid := false
		switch flag {
		case "--tb":
			valid = slices.Contains([]string{"auto", "long", "short", "line", "native", "no"}, value)
		case "--capture":
			valid = slices.Contains([]string{"fd", "sys", "no", "tee-sys"}, value)
		case "--color":
			valid = slices.Contains([]string{"yes", "no", "auto"}, value)
		case "--import-mode":
			valid = slices.Contains([]string{"prepend", "append", "importlib"}, value)
		case "-p":
			valid = pytestPlugin.MatchString(value)
		case "-c":
			valid = repositoryAcceptancePath(value) && !strings.ContainsAny(value, "{}:*?[]") && !strings.HasPrefix(value, "-")
		}
		if !valid {
			return fail(fmt.Sprintf("unsupported pytest %s value %q", flag, value))
		}
	}
	if !selected {
		return fail("pytest acceptance requires exactly one standalone {test} placeholder")
	}
	return RepositoryAcceptanceSelection{Argv: argv}, ctx.Err()
}

func pytestCommandStart(argv []string) int {
	if len(argv) > 0 && argv[0] == "pytest" {
		return 1
	}
	if len(argv) >= 3 && slices.Equal(argv[:3], []string{"python3", "-m", "pytest"}) {
		return 3
	}
	return 0
}

//go:embed pytest_runner.py
var pytestRunner []byte

const pytestEventEnv = "INVARIANT_PYTEST_EVENTS"

func runRepositoryPytestAcceptance(ctx context.Context, part repository.Part, in RepositoryAcceptanceInvocation, transport RepositoryProcessTransport) (RepositoryAcceptanceEvidence, []byte, error) {
	in = cloneGoInvocation(in)
	fail := func(err error) (RepositoryAcceptanceEvidence, []byte, error) {
		return pytestRefusal(in.Binding, "setup", err.Error()), nil, err
	}
	test := Test{Name: "pytest acceptance", Part: in.Binding.Part, Framework: in.Binding.Framework, File: in.Binding.File, Selector: in.Binding.Selector}
	selection, err := (PytestRepositoryAcceptanceAdapter{}).Select(ctx, part, test)
	if err != nil {
		return fail(err)
	}
	if !slices.Equal(in.Argv, selection.Argv) || !nativeText(in.Binding.InvocationID) || in.Binding.SourceDigest != dependencyHash([]byte(in.Source)) || !nativeText(in.Binding.ExecutionDigest) || in.Binding.Package != "" {
		return fail(errors.New("pytest invocation differs from its captured selection or source binding"))
	}
	file, _ := goAcceptanceFile(part, in.Binding.File)
	recheck := func() error {
		data, err := transport.ReadFile(ctx, file)
		if err != nil {
			return err
		}
		if string(data) != in.Source {
			return errors.New("pytest acceptance source differs from pinned bytes")
		}
		return ctx.Err()
	}
	if err := recheck(); err != nil {
		return fail(err)
	}
	helper, err := transport.Stage(ctx, path.Join("pytest-"+rand.Text(), "runner.py"), pytestRunner)
	if err != nil {
		return fail(err)
	}
	data, err := json.Marshal(struct {
		Binding RepositoryAcceptanceBinding
		File    string
		Args    []string
	}{in.Binding, file, in.Argv[pytestCommandStart(in.Argv):]})
	if err != nil {
		return fail(err)
	}
	prepared, err := transport.Prepare(ctx, RepositoryProcessSpec{Argv: []string{"python3", "-I", "-B", helper, string(data)}, EventEnv: pytestEventEnv})
	if err != nil {
		return fail(err)
	}
	process, startErr := prepared.Start(ctx)
	if process == nil {
		return fail(errors.Join(startErr, errors.New("pytest process did not start")))
	}
	out, waitErr := process.Wait(ctx)
	evidence, collectErr := collectPytestAcceptance(in, out)
	err = errors.Join(startErr, waitErr, collectErr, recheck(), ctx.Err())
	if err != nil {
		evidence.Complete = false
	}
	return evidence, append(slices.Clone(out.Stdout), out.Stderr...), err
}

func (e *dockerRepositoryChecks) runPytestAcceptance(ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
	e.mu.Lock()
	if e.used == nil {
		e.used = map[string]bool{}
	}
	invalid := e.closed || e.used[in.Binding.InvocationID] || e.request.Env["PIP_NO_INDEX"] != "1"
	e.used[in.Binding.InvocationID] = true
	e.mu.Unlock()
	if invalid {
		err := errors.New("Docker pytest acceptance is closed, consumed or not Python")
		return pytestRefusal(in.Binding, "setup", err.Error()), nil, err
	}
	argv := slices.Clone(in.Argv)
	for i := pytestCommandStart(argv); i < len(argv); i++ {
		if argv[i] == in.Binding.Selector {
			argv[i] = "{test}"
		}
	}
	part := repository.Part{Name: in.Binding.Part, Directory: e.request.Directory, Language: "python", Commands: map[string]repository.Command{"test_one": {Argv: argv}}}
	return runRepositoryPytestAcceptance(ctx, part, in, e)
}
