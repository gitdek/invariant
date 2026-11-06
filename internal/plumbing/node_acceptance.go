package plumbing

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/repository"
)

func defaultNodeAdapter(adapter RepositoryAcceptanceAdapter) bool {
	switch adapter.(type) {
	case VitestRepositoryAcceptanceAdapter, JestRepositoryAcceptanceAdapter:
		return true
	}
	return false
}

func selectNodeAcceptance(ctx context.Context, part repository.Part, test Test, framework string) (RepositoryAcceptanceSelection, error) {
	fail := func(reason string) (RepositoryAcceptanceSelection, error) {
		return RepositoryAcceptanceSelection{}, errors.New(reason)
	}
	if err := ctx.Err(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	if part.Language != "typescript" || test.Framework != framework || part.Name != test.Part {
		return fail("Node acceptance needs its captured TypeScript owner and framework")
	}
	if err := test.validateNative(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	file, err := goAcceptanceFile(part, test.File)
	if err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	if !repositoryAcceptancePath(file) || strings.HasPrefix(file, "-") {
		return fail("Node acceptance needs a confined part-relative file")
	}
	command := part.Commands["test_one"]
	argv := slices.Clone(command.Argv)
	start := 1
	if framework == "vitest" {
		start = 2
	}
	if command.NotApplicable != "" || len(argv) <= start || argv[0] != framework || framework == "vitest" && argv[1] != "run" {
		return fail("Node acceptance requires direct vitest run or jest")
	}
	seen := map[string]bool{}
	for i := start; i < len(argv); i++ {
		flag, value, equal := strings.Cut(argv[i], "=")
		key := flag
		if key == "-t" {
			key = "--testNamePattern"
		}
		if seen[key] {
			return fail("repeated Node selection flag " + key)
		}
		seen[key] = true
		if framework == "jest" && flag == "--runInBand" && !equal {
			continue
		}
		switch key {
		case "--testNamePattern", "--config", "--testTimeout":
		default:
			return fail("unsupported Node acceptance argument " + argv[i])
		}
		position := i
		if !equal {
			i++
			if i == len(argv) {
				return fail("Node flag needs a value: " + flag)
			}
			value = argv[i]
		}
		switch key {
		case "--testNamePattern":
			if value != "{test}" {
				return fail("Node title filter needs exactly one {test} placeholder")
			}
			value = "^" + regexp.QuoteMeta(test.Selector) + "$"
			if equal {
				argv[position] = flag + "=" + value
			} else {
				argv[i] = value
			}
		case "--config":
			if !repositoryAcceptancePath(value) || strings.ContainsAny(value, "{}:*?[]") || strings.HasPrefix(value, "-") {
				return fail("Node configuration must be a confined part-relative file")
			}
		case "--testTimeout":
			n, err := strconv.ParseUint(value, 10, 31)
			if err != nil || n == 0 || strconv.FormatUint(n, 10) != value {
				return fail("Node testTimeout must be a positive integer in milliseconds")
			}
		}
	}
	if !seen["--testNamePattern"] {
		return fail("Node acceptance needs exactly one title filter")
	}
	if framework == "jest" {
		argv = append(argv, "--runTestsByPath")
	}
	return RepositoryAcceptanceSelection{Argv: append(argv, file)}, ctx.Err()
}

//go:embed node_runner_common.cjs
var nodeRunnerCommon []byte

const nodeEventEnv = "INVARIANT_NODE_EVENTS"

// Transport-only metadata, removed before constructing any Docker arguments or
// workload environment. The credential travels on the host control connection.
const nodeAuthenticationEnv = "INVARIANT_NODE_AUTHENTICATION"

func runRepositoryNodeAcceptance(ctx context.Context, part repository.Part, in RepositoryAcceptanceInvocation, transport RepositoryProcessTransport) (RepositoryAcceptanceEvidence, []byte, error) {
	in = cloneGoInvocation(in)
	fail := func(err error) (RepositoryAcceptanceEvidence, []byte, error) {
		return nodeRefusal(in.Binding, err.Error()), nil, err
	}
	test := Test{Name: "Node acceptance", Part: in.Binding.Part, Framework: in.Binding.Framework, File: in.Binding.File, Selector: in.Binding.Selector}
	selection, err := selectNodeAcceptance(ctx, part, test, in.Binding.Framework)
	if err != nil {
		return fail(err)
	}
	if !slices.Equal(in.Argv, selection.Argv) || !nativeText(in.Binding.InvocationID) || in.Binding.SourceDigest != dependencyHash([]byte(in.Source)) || !executionDigest.MatchString(in.Binding.ExecutionDigest) || in.Binding.Package != "" {
		return fail(errors.New("Node invocation differs from captured selection or source binding"))
	}
	file, _ := goAcceptanceFile(part, in.Binding.File)
	recheck := func() error {
		data, err := transport.ReadFile(ctx, file)
		if err != nil {
			return err
		}
		if string(data) != in.Source {
			return errors.New("Node acceptance source differs from pinned bytes")
		}
		return ctx.Err()
	}
	if err := recheck(); err != nil {
		return fail(err)
	}
	private := "node-" + rand.Text()
	if _, err := transport.Stage(ctx, path.Join(private, "node_runner_common.cjs"), nodeRunnerCommon); err != nil {
		return fail(err)
	}
	asset, name := vitestRunner, "vitest_runner.mjs"
	if in.Binding.Framework == "jest" {
		asset, name = jestRunner, "jest_runner.cjs"
		if _, err := transport.Stage(ctx, path.Join(private, "jest_scheduler.cjs"), []byte(jestScheduler)); err != nil {
			return fail(err)
		}
	}
	helper, err := transport.Stage(ctx, path.Join(private, name), asset)
	if err != nil {
		return fail(err)
	}
	data, err := json.Marshal(struct {
		Binding RepositoryAcceptanceBinding
		File    string
		Args    []string
	}{in.Binding, file, in.Argv})
	if err != nil {
		return fail(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fail(err)
	}
	prepared, err := transport.Prepare(ctx, RepositoryProcessSpec{Argv: []string{"node", helper, string(data)}, EventEnv: nodeEventEnv,
		Env: map[string]string{nodeAuthenticationEnv: hex.EncodeToString(key)}})
	if err != nil {
		return fail(err)
	}
	process, startErr := prepared.Start(ctx)
	if process == nil {
		return fail(errors.Join(startErr, errors.New("Node process did not start")))
	}
	out, waitErr := process.Wait(ctx)
	evidence, collectErr := collectNodeAcceptance(in, out, key)
	err = errors.Join(startErr, waitErr, collectErr, recheck(), ctx.Err())
	if err != nil {
		evidence.Complete = false
	}
	return evidence, append(slices.Clone(out.Stdout), out.Stderr...), err
}

func (e *dockerRepositoryChecks) runNodeAcceptance(ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
	e.mu.Lock()
	if e.used == nil {
		e.used = map[string]bool{}
	}
	invalid := e.closed || e.used[in.Binding.InvocationID] || e.request.Env["NPM_CONFIG_OFFLINE"] != "true"
	e.used[in.Binding.InvocationID] = true
	e.mu.Unlock()
	if invalid {
		err := errors.New("Docker Node acceptance is closed, consumed or not Node")
		return nodeRefusal(in.Binding, err.Error()), nil, err
	}
	argv := slices.Clone(in.Argv)
	remove := 1
	if in.Binding.Framework == "jest" {
		remove = 2
	}
	if len(argv) <= remove {
		err := errors.New("incomplete Node selection")
		return nodeRefusal(in.Binding, err.Error()), nil, err
	}
	argv = argv[:len(argv)-remove]
	for i := 1; i < len(argv); i++ {
		flag, _, equal := strings.Cut(argv[i], "=")
		if flag == "-t" || flag == "--testNamePattern" {
			if equal {
				argv[i] = flag + "={test}"
			} else if i+1 < len(argv) {
				i++
				argv[i] = "{test}"
			}
		}
	}
	part := repository.Part{Name: in.Binding.Part, Directory: e.request.Directory, Language: "typescript", Commands: map[string]repository.Command{"test_one": {Argv: argv}}}
	return runRepositoryNodeAcceptance(ctx, part, in, e)
}

func checkNodeInputs(ctx context.Context, snapshot *repository.Snapshot, part repository.Part, transport RepositoryProcessTransport) error {
	for _, name := range append(slices.Clone(part.Inputs), part.Lockfiles...) {
		want, err := snapshot.ReadFileContext(ctx, name)
		if err != nil {
			return err
		}
		file, err := goAcceptanceFile(part, name)
		if err != nil {
			return err
		}
		got, err := transport.ReadFile(ctx, file)
		if err != nil || string(got) != string(want) {
			return fmt.Errorf("Node captured input %s changed: %v", name, err)
		}
	}
	// Configuration is captured policy too, including automatic discovery. It
	// cannot be introduced or replaced by a preceding workload in this part.
	argv := part.Commands["test_one"].Argv
	if len(argv) == 0 {
		return errors.New("missing captured Node command")
	}
	var configs []string
	for i := 1; i < len(argv); i++ {
		flag, value, equal := strings.Cut(argv[i], "=")
		if flag == "--config" {
			if !equal && i+1 < len(argv) {
				i++
				value = argv[i]
			}
			configs = append(configs, value)
		}
	}
	if len(configs) == 0 {
		for _, ext := range []string{"cjs", "js", "mjs", "json", "ts", "mts"} {
			configs = append(configs, argv[0]+".config."+ext)
		}
	}
	for _, file := range configs {
		want, sourceErr := snapshot.ReadFileContext(ctx, path.Join(part.Directory, file))
		got, readErr := transport.ReadFile(ctx, file)
		if sourceErr != nil && readErr != nil {
			continue // The runner independently refuses an explicit missing config.
		}
		if sourceErr != nil || readErr != nil || string(got) != string(want) {
			return fmt.Errorf("Node captured configuration %s changed", file)
		}
	}
	return ctx.Err()
}

// Seal before ordinary commands run, then compare around each acceptance. Cache
// output is excluded; no executable resolution is allowed from those directories.
const nodeDependencySeal = `const fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const hash=crypto.createHash('sha256');
function walk(p){const s=fs.lstatSync(p);hash.update(JSON.stringify([p,s.mode]));
if(s.isSymbolicLink())hash.update(fs.readlinkSync(p));
else if(s.isDirectory()){for(const n of fs.readdirSync(p).sort()){if(n!=='.vite'&&n!=='.cache')walk(path.join(p,n));}}
else if(s.isFile())hash.update(fs.readFileSync(p));else throw Error('nonregular prepared dependency');}
walk('node_modules');process.stdout.write(hash.digest('hex'));`

func sealNodeDependencies(ctx context.Context, environment RepositoryCheckEnvironment) (string, error) {
	out, err := environment.Run(ctx, []string{"node", "-e", nodeDependencySeal})
	if err != nil {
		return "", fmt.Errorf("seal prepared Node dependencies: %w", err)
	}
	seal := string(out)
	if !executionDigest.MatchString("sha256:" + seal) {
		return "", errors.New("Node dependency seal is not a complete digest")
	}
	return seal, ctx.Err()
}

func checkNodeDependencies(ctx context.Context, environment RepositoryCheckEnvironment, want string) error {
	got, err := sealNodeDependencies(ctx, environment)
	if err != nil {
		return err
	}
	if got != want || want == "" {
		return errors.New("prepared Node dependencies changed during checks")
	}
	return nil
}
