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

// PytestRepositoryAcceptanceAdapter keeps the captured command's flags and
// substitutes one literal node ID. Collection and verdicts belong to pytest.
type PytestRepositoryAcceptanceAdapter struct{}

var pytestIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]*$`)
var pytestPlugin = regexp.MustCompile(`^(no:)?[A-Za-z_][A-Za-z_0-9.]*$`)

func (PytestRepositoryAcceptanceAdapter) Select(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
	bad := func(reason string) (RepositoryAcceptanceSelection, error) {
		return RepositoryAcceptanceSelection{}, errors.New(reason)
	}
	if err := ctx.Err(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	if part.Language != "python" || test.Framework != "pytest" || part.Name != test.Part {
		return bad("pytest acceptance needs its captured Python owner and framework")
	}
	if err := test.validateNative(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	file, err := goAcceptanceFile(part, test.File)
	if err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	selectedFile, node, found := strings.Cut(test.Selector, "::")
	declaration, parameter, parametrized := strings.Cut(node, "[")
	segments := strings.Split(declaration, "::")
	if !found || selectedFile != file || len(segments) > 2 || parametrized && (!strings.HasSuffix(parameter, "]") || len(parameter) < 2) {
		return bad("pytest needs one exact part-relative function, method or concrete parameter node ID")
	}
	for _, name := range segments {
		if !pytestIdentifier.MatchString(name) {
			return bad("pytest node declarations must be identifiers")
		}
	}
	if !strings.HasPrefix(segments[len(segments)-1], "test") {
		return bad("pytest cannot select a file or class")
	}
	command := part.Commands["test_one"]
	argv := slices.Clone(command.Argv)
	start := pytestCommandStart(argv)
	if command.NotApplicable != "" || start == 0 {
		return bad("pytest acceptance supports direct pytest or python3 -m pytest only")
	}
	for _, arg := range argv {
		if !nativeText(arg) {
			return bad("pytest command contains empty or invisible arguments")
		}
	}
	selected := false
	seen := map[string]bool{}
	for i := start; i < len(argv); i++ {
		arg := argv[i]
		if arg == "{test}" {
			if selected {
				return bad("pytest needs exactly one selection placeholder")
			}
			selected = true
			argv[i] = test.Selector
			continue
		}
		flag, value, equal := strings.Cut(arg, "=")
		if seen[flag] && flag != "-p" {
			return bad("pytest refuses repeated flag " + flag)
		}
		seen[flag] = true
		boolean := false
		switch flag {
		case "-q", "-v", "-s", "--strict-markers", "--strict-config", "--disable-warnings", "--no-header", "--no-summary", "--showlocals", "--full-trace":
			boolean = true
		case "-p", "-c", "--tb", "--import-mode", "--capture", "--color":
		default:
			return bad("pytest acceptance does not support argument " + arg)
		}
		if boolean {
			if equal {
				return bad("pytest boolean flags take no value")
			}
			continue
		}
		if !equal {
			i++
			if i == len(argv) {
				return bad("pytest flag needs a value: " + flag)
			}
			value = argv[i]
		}
		valid := false
		switch flag {
		case "-p":
			valid = pytestPlugin.MatchString(value)
		case "-c":
			valid = repositoryAcceptancePath(value) && !strings.ContainsAny(value, "{}")
		case "--tb":
			valid = slices.Contains([]string{"auto", "long", "short", "line", "native", "no"}, value)
		case "--import-mode":
			valid = slices.Contains([]string{"prepend", "append", "importlib"}, value)
		case "--capture":
			valid = slices.Contains([]string{"fd", "sys", "no", "tee-sys"}, value)
		case "--color":
			valid = slices.Contains([]string{"yes", "no", "auto"}, value)
		}
		if !valid {
			return bad(fmt.Sprintf("pytest acceptance does not support %s value %q", flag, value))
		}
	}
	if !selected {
		return bad("pytest needs exactly one selection placeholder")
	}
	return RepositoryAcceptanceSelection{Package: path.Dir(file), Argv: argv}, ctx.Err()
}

func pytestCommandStart(argv []string) int {
	if len(argv) >= 1 && argv[0] == "pytest" {
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
	fail := func(err error) (RepositoryAcceptanceEvidence, []byte, error) {
		return RepositoryAcceptanceEvidence{ExitCode: -1, Records: []RepositoryAcceptanceRecord{{Binding: in.Binding, Kind: "setup", Status: "failed", Reason: err.Error()}}}, nil, err
	}
	test := Test{Name: "pytest acceptance", Part: in.Binding.Part, Framework: in.Binding.Framework, File: in.Binding.File, Selector: in.Binding.Selector}
	selection, err := (PytestRepositoryAcceptanceAdapter{}).Select(ctx, part, test)
	if err != nil {
		return fail(err)
	}
	if !slices.Equal(selection.Argv, in.Argv) || selection.Package != in.Binding.Package || dependencyHash([]byte(in.Source)) != in.Binding.SourceDigest || !nativeText(in.Binding.InvocationID) || !executionDigest.MatchString(in.Binding.ExecutionDigest) {
		return fail(errors.New("pytest invocation differs from captured selection, source or configuration"))
	}
	file, err := goAcceptanceFile(part, in.Binding.File)
	if err != nil {
		return fail(err)
	}
	checkSource := func() error {
		data, err := transport.ReadFile(ctx, file)
		if err != nil {
			return err
		}
		if string(data) != in.Source {
			return errors.New("pytest acceptance source differs from its pinned bytes")
		}
		return ctx.Err()
	}
	if err := checkSource(); err != nil {
		return fail(err)
	}
	helper, err := transport.Stage(ctx, "pytest-"+rand.Text()+"/runner.py", pytestRunner)
	if err != nil {
		return fail(err)
	}
	request, err := json.Marshal(struct {
		Binding RepositoryAcceptanceBinding
		File    string
		Argv    []string
	}{in.Binding, file, slices.Clone(in.Argv[pytestCommandStart(in.Argv):])})
	if err != nil {
		return fail(err)
	}
	prepared, err := transport.Prepare(ctx, RepositoryProcessSpec{Argv: []string{"python3", "-I", "-B", helper, string(request)}, EventEnv: pytestEventEnv})
	if err != nil {
		return fail(err)
	}
	process, err := prepared.Start(ctx)
	if err != nil || process == nil {
		return fail(errors.Join(err, errors.New("pytest process did not start")))
	}
	output, waitErr := process.Wait(ctx)
	evidence, evidenceErr := collectPytestAcceptance(in, output, errors.Join(waitErr, checkSource(), ctx.Err()))
	diagnostics := append(slices.Clone(output.Stdout), output.Stderr...)
	return evidence, diagnostics, evidenceErr
}
