package plumbing

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/repository"
)

// PytestRepositoryAcceptanceAdapter selects one literal node in its owning
// part. Collection and runtime observations belong to the private runner.
type PytestRepositoryAcceptanceAdapter struct{}

var pytestIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]*$`)
var pytestPlugin = regexp.MustCompile(`^(no:)?[A-Za-z_][A-Za-z_0-9]*(\.[A-Za-z_][A-Za-z_0-9]*)*$`)

func (PytestRepositoryAcceptanceAdapter) Select(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
	if err := ctx.Err(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	fail := func(reason string) (RepositoryAcceptanceSelection, error) {
		return RepositoryAcceptanceSelection{}, errors.New("pytest acceptance: " + reason)
	}
	if part.Language != "python" || test.Framework != "pytest" || part.Name != test.Part {
		return fail("requires its captured Python owner and framework")
	}
	if err := test.validateNative(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	file, err := goAcceptanceFile(part, test.File)
	if err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	selected, node, found := strings.Cut(test.Selector, "::")
	if !found || selected != file {
		return fail("selector must start with the exact part-relative file")
	}
	declaration, parameter, parametrized := strings.Cut(node, "[")
	names := strings.Split(declaration, "::")
	if len(names) < 1 || len(names) > 2 || parametrized && !strings.HasSuffix(parameter, "]") {
		return fail("select one function, class method or concrete parameter node")
	}
	for _, name := range names {
		if !pytestIdentifier.MatchString(name) {
			return fail("unsupported declaration name")
		}
	}
	if !strings.HasPrefix(names[len(names)-1], "test") || len(names) == 2 && !strings.HasPrefix(names[0], "Test") {
		return fail("file, class and group selections are not individual tests")
	}
	command := part.Commands["test_one"]
	argv := slices.Clone(command.Argv)
	start, err := pytestCommandStart(argv)
	if err != nil || command.NotApplicable != "" {
		return fail("supports only pytest or python3 -m pytest with one {test} argument")
	}
	seen := map[string]bool{}
	placeholder := false
	for i := start; i < len(argv); i++ {
		arg := argv[i]
		if !nativeText(arg) {
			return fail("empty or invisible command argument")
		}
		if arg == "{test}" {
			if placeholder {
				return fail("repeated test placeholder")
			}
			placeholder, argv[i] = true, test.Selector
			continue
		}
		flag, value, equal := strings.Cut(arg, "=")
		if seen[flag] {
			return fail("repeated flag " + flag)
		}
		seen[flag] = true
		switch flag {
		case "-q", "-v", "-s", "--strict-markers", "--strict-config", "--disable-warnings":
			if equal {
				return fail("unsupported value for " + flag)
			}
			continue
		case "--tb", "--import-mode", "-p", "-c":
		default:
			return fail("unsupported flag or extra selection " + arg)
		}
		if !equal {
			i++
			if i == len(argv) {
				return fail("missing value for " + flag)
			}
			value = argv[i]
		}
		valid := nativeText(value) && !strings.Contains(value, "{test}")
		switch flag {
		case "--tb":
			valid = valid && slices.Contains([]string{"auto", "long", "short", "line", "native", "no"}, value)
		case "--import-mode":
			valid = valid && slices.Contains([]string{"prepend", "append", "importlib"}, value)
		case "-p":
			valid = valid && pytestPlugin.MatchString(value)
		case "-c":
			valid = valid && repositoryAcceptancePath(value) && !strings.HasPrefix(value, "-")
		}
		if !valid {
			return fail("unsupported value for " + flag)
		}
	}
	if !placeholder {
		return fail("requires exactly one whole {test} argument")
	}
	return RepositoryAcceptanceSelection{Argv: argv}, ctx.Err()
}

func pytestCommandStart(argv []string) (int, error) {
	if len(argv) >= 2 && argv[0] == "pytest" {
		return 1, nil
	}
	if len(argv) >= 4 && slices.Equal(argv[:3], []string{"python3", "-m", "pytest"}) {
		return 3, nil
	}
	return 0, errors.New("unsupported pytest command")
}

//go:embed pytest_runner.py
var pytestRunner []byte

func runRepositoryPytestAcceptance(ctx context.Context, part repository.Part, in RepositoryAcceptanceInvocation, transport RepositoryProcessTransport) (RepositoryAcceptanceEvidence, []byte, error) {
	in = cloneGoInvocation(in)
	fail := func(err error) (RepositoryAcceptanceEvidence, []byte, error) {
		return pytestRefusal(in.Binding, "setup", err.Error()), nil, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	file, err := goAcceptanceFile(part, in.Binding.File)
	if err != nil {
		return fail(err)
	}
	if in.Binding.Framework != "pytest" || in.Binding.Part != part.Name || !nativeText(in.Binding.InvocationID) || !executionDigest.MatchString(in.Binding.ExecutionDigest) || dependencyHash([]byte(in.Source)) != in.Binding.SourceDigest {
		return fail(errors.New("pytest invocation has invalid authority or source binding"))
	}
	checkSource := func() error {
		data, err := transport.ReadFile(ctx, file)
		if err == nil && dependencyHash(data) != in.Binding.SourceDigest {
			err = errors.New("pytest acceptance source differs from the pinned bytes")
		}
		return errors.Join(err, ctx.Err())
	}
	if err := checkSource(); err != nil {
		return fail(err)
	}
	start, err := pytestCommandStart(in.Argv)
	if err != nil {
		return fail(err)
	}
	manifest, err := json.Marshal(struct {
		Binding RepositoryAcceptanceBinding
		File    string
		Argv    []string
	}{in.Binding, file, in.Argv[start:]})
	if err != nil {
		return fail(err)
	}
	// Fingerprint in isolated, host-supplied code with no site initialization
	// or candidate imports. The post-process read includes atexit mutations;
	// neither the workload's final event nor a reusable helper can supply it.
	configuration := func() (string, error) {
		out, err := transport.Run(ctx, []string{"python3", "-I", "-S", "-c", string(pytestRunner), "--configuration", string(manifest)})
		if err == nil && (!strings.HasSuffix(string(out), "\n") || !executionDigest.MatchString(strings.TrimSuffix(string(out), "\n"))) {
			err = errors.New("pytest configuration check returned no exact fingerprint")
		}
		return string(out), errors.Join(err, ctx.Err())
	}
	initialConfig, err := configuration()
	if err != nil {
		return fail(err)
	}
	private := "pytest-" + rand.Text()
	helper, err := transport.Stage(ctx, private+"/runner.py", pytestRunner)
	if err != nil {
		return fail(err)
	}
	input, err := transport.Stage(ctx, private+"/invocation.json", manifest)
	if err != nil {
		return fail(err)
	}
	prepared, err := transport.Prepare(ctx, RepositoryProcessSpec{Argv: []string{"python3", "-I", helper, input}, EventEnv: "INVARIANT_PYTEST_EVENTS"})
	if err != nil {
		return fail(err)
	}
	process, err := prepared.Start(ctx)
	if process == nil {
		if err == nil {
			err = errors.New("pytest process did not start")
		}
		return fail(err)
	}
	output, waitErr := process.Wait(ctx)
	err = errors.Join(err, waitErr, ctx.Err())
	if ctx.Err() == nil {
		finalConfig, configErr := configuration()
		if configErr == nil && finalConfig != initialConfig {
			configErr = errors.New("pytest configuration changed during process execution")
		}
		err = errors.Join(err, configErr)
		err = errors.Join(err, checkSource())
	}
	evidence, collectErr := collectPytestAcceptance(in.Binding, output, err)
	out := append(slices.Clone(output.Stdout), output.Stderr...)
	return evidence, out, errors.Join(err, collectErr)
}

func (e *dockerRepositoryChecks) runPytestAcceptance(ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
	command := slices.Clone(in.Argv)
	for i := range command {
		if command[i] == in.Binding.Selector {
			command[i] = "{test}"
		}
	}
	part := repository.Part{Name: in.Binding.Part, Directory: e.request.Directory, Language: "python", Commands: map[string]repository.Command{"test_one": {Argv: command}}}
	test := Test{Name: "pytest acceptance", Part: in.Binding.Part, Framework: "pytest", File: in.Binding.File, Selector: in.Binding.Selector}
	selection, err := (PytestRepositoryAcceptanceAdapter{}).Select(ctx, part, test)
	if err == nil && !slices.Equal(selection.Argv, in.Argv) {
		err = errors.New("pytest invocation differs from its literal selection")
	}
	if err != nil {
		return pytestRefusal(in.Binding, "setup", err.Error()), nil, fmt.Errorf("pytest selection: %w", err)
	}
	return runRepositoryPytestAcceptance(ctx, part, in, e)
}
