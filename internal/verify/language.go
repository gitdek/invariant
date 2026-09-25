package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/gobra"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Language is the part of the gate that depends on the implementation's
// language; the design checks don't. Verify proves the code against its
// contracts, and returns nil when the language has no proof path for the
// code. Check builds and tests the code in a sandbox and collects its
// evidence of matching the model (D-0024).
type Language interface {
	Verify(ctx context.Context, pkg string) (*Code, error)
	Check(ctx context.Context, p *project.Project) (Build, Evidence, error)
}

// Evidence ties code to its model. A state machine is explored from its
// initial state (Go); other code is run by a conformance driver, which
// records what it does (TypeScript, Python).
type Evidence struct {
	Exploration *Exploration
	Traces      []byte
	Message     string // why there's no evidence, when there isn't
}

// Exploration is the implementation's state space, explored from its initial
// state.
type Exploration struct {
	OK      bool
	States  int64
	Depth   int
	Message string // why exploration failed, when it did
}

func languageFor(p *project.Project, tc toolchain.Toolchain) (Language, error) {
	switch p.Manifest.Language {
	case "", "go":
		return Go{GobraImage: tc.GobraImage, GoImage: tc.GoImage}, nil
	case "typescript", "python":
		if p.Manifest.Conformance == "" {
			return nil, fmt.Errorf("a %s project needs a conformance driver in its manifest", p.Manifest.Language)
		}
		if p.Manifest.Language == "typescript" {
			return TypeScript{Image: tc.NodeImage}, nil
		}
		return Python{Image: tc.PythonImage}, nil
	default:
		return nil, fmt.Errorf("language %q isn't supported (see decisions/D-0024-checking-typescript-and-python.md)", p.Manifest.Language)
	}
}

// Go verifies a package with Gobra, with overflow checks on. It builds,
// tests and explores the package's module in a container from GoImage, on a
// throwaway copy, with no network and none of the host's environment.
type Go struct {
	GobraImage string
	GoImage    string
}

func (g Go) Verify(ctx context.Context, pkg string) (*Code, error) {
	res, err := gobra.Run(ctx, g.GobraImage, pkg, true)
	if err != nil {
		return nil, err
	}
	return &Code{
		Verifier: "Gobra", Passed: res.Passed, Functions: res.Functions, Contracts: res.Contracts,
		Unverified: res.Unverified, Overflow: res.Overflow, Errors: res.Errors,
	}, nil
}

// agreementTest explores the implementation the way TLC explores the model:
// breadth first from Init, following Successors.
const agreementTest = `package %s

// Written by Invariant's gate for the agreement check. Not part of the project.

import (
	"fmt"
	"testing"
)

func TestInvariantAgreement(t *testing.T) {
	start := Init()
	seen := map[State]bool{start: true}
	depth := 0
	for frontier := []State{start}; len(frontier) > 0; depth++ {
		var next []State
		for _, s := range frontier {
			for _, u := range Successors(s) {
				if !seen[u] {
					seen[u] = true
					next = append(next, u)
				}
			}
		}
		frontier = next
		if len(seen) > 10000000 {
			t.Fatal("more than ten million states")
		}
	}
	fmt.Printf("invariant-agreement states=%%d depth=%%d\n", len(seen), depth)
}
`

// sandboxScript runs inside the container. Markers separate the steps.
const sandboxScript = `cd /src
go vet ./... ; echo "@@invariant vet=$?"
go test -count=1 ./... ; echo "@@invariant test=$?"
cp /agree/zz_invariant_agreement_test.go "./$PKG/" && go test -count=1 -run '^TestInvariantAgreement$' -v "./$PKG" ; echo "@@invariant agree=$?"
`

var (
	marker   = regexp.MustCompile(`(?m)^@@invariant (\w+)=(\d+)$`)
	explored = regexp.MustCompile(`invariant-agreement states=(\d+) depth=(\d+)`)
)

func (g Go) Check(ctx context.Context, p *project.Project) (Build, Evidence, error) {
	b, e, err := g.check(ctx, p.Dir, p.CodeDir())
	return b, Evidence{Exploration: &e}, err
}

func (g Go) check(ctx context.Context, projectDir, pkg string) (Build, Exploration, error) {
	root, err := moduleRoot(projectDir, pkg)
	if err != nil {
		return Build{}, Exploration{}, err
	}
	rel, err := filepath.Rel(root, pkg)
	if err != nil {
		return Build{}, Exploration{}, err
	}
	name, err := packageName(pkg)
	if err != nil {
		return Build{}, Exploration{}, err
	}
	work, err := os.MkdirTemp("", "invariant-go-")
	if err != nil {
		return Build{}, Exploration{}, err
	}
	defer os.RemoveAll(work)
	src, agreeDir := filepath.Join(work, "src"), filepath.Join(work, "agree")
	if err := copyTree(root, src); err != nil {
		return Build{}, Exploration{}, err
	}
	if err := os.MkdirAll(agreeDir, 0o755); err != nil {
		return Build{}, Exploration{}, err
	}
	test := fmt.Sprintf(agreementTest, name)
	if err := os.WriteFile(filepath.Join(agreeDir, "zz_invariant_agreement_test.go"), []byte(test), 0o644); err != nil {
		return Build{}, Exploration{}, err
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--memory", "2g", "--pids-limit", "512",
		"-e", "GOTOOLCHAIN=local", "-e", "GOFLAGS=-mod=readonly", "-e", "GOCACHE=/tmp/gocache", "-e", "HOME=/tmp",
		"-e", "CGO_ENABLED=0", "-e", "PKG="+filepath.ToSlash(rel),
		"-v", src+":/src", "-v", agreeDir+":/agree:ro", "-w", "/src", g.GoImage, "sh", "-c", sandboxScript)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return Build{}, Exploration{}, fmt.Errorf("running the Go sandbox: %w", err)
		}
	}
	return parseSandbox(out.String())
}

// section is one step of a sandbox run: its output and exit code.
type section struct {
	out  string
	code int
}

// splitMarkers splits a sandbox's output at lines like "@@invariant name=0",
// each closing the step before it.
func splitMarkers(out string, want int) ([]section, error) {
	marks := marker.FindAllStringSubmatchIndex(out, -1)
	if len(marks) != want {
		return nil, fmt.Errorf("the sandbox didn't finish:\n%s", lastLines(out, 20))
	}
	sections := make([]section, len(marks))
	for i, m := range marks {
		start := 0
		if i > 0 {
			start = marks[i-1][1]
		}
		code, _ := strconv.Atoi(out[m[4]:m[5]])
		sections[i] = section{strings.TrimSpace(out[start:m[0]]), code}
	}
	return sections, nil
}

// parseSandbox reads the Go sandbox's output.
func parseSandbox(out string) (Build, Exploration, error) {
	s, err := splitMarkers(out, 3)
	if err != nil {
		return Build{}, Exploration{}, err
	}
	vetOut, vet := s[0].out, s[0].code
	testOut, test := s[1].out, s[1].code
	agreeOut, agreed := s[2].out, s[2].code
	b := Build{Steps: []BuildStep{{Name: "go vet", Passed: vet == 0}, {Name: "go test", Passed: test == 0}}}
	b.Passed = vet == 0 && test == 0
	b.Output = strings.TrimSpace(vetOut + "\n" + testOut)
	var e Exploration
	if m := explored.FindStringSubmatch(agreeOut); m != nil && agreed == 0 {
		e.OK = true
		e.States, _ = strconv.ParseInt(m[1], 10, 64)
		e.Depth, _ = strconv.Atoi(m[2])
	} else {
		e.Message = "couldn't explore the implementation. The package must export Init() State and " +
			"Successors(State) []State, with State comparable:\n" + lastLines(agreeOut, 20)
	}
	return b, e, nil
}

// moduleRoot finds the directory holding pkg's go.mod, within the project.
func moduleRoot(projectDir, pkg string) (string, error) {
	for dir := pkg; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		if dir == projectDir || dir == filepath.Dir(dir) {
			return "", fmt.Errorf("no go.mod between %s and %s; each project is its own Go module (D-0020)", pkg, projectDir)
		}
	}
}

func packageName(pkg string) (string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkg, n), nil, parser.PackageClauseOnly)
			if err != nil {
				return "", err
			}
			return f.Name.Name, nil
		}
	}
	return "", fmt.Errorf("no Go files in %s", pkg)
}

// copyTree copies a module for the sandbox, leaving out version control and
// build output.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "out" || d.Name() == "bin") && rel != "." {
			return filepath.SkipDir
		}
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

func goVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "go", "env", "GOVERSION").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Conformance runs: how many, how long, and from which seed. Fixed, so a
// receipt's numbers are reproducible.
const (
	conformanceRuns  = 1000
	conformanceSteps = 40
	conformanceSeed  = 1
)

// TypeScript runs a project's tests and its conformance driver in a Node
// container. Node 24 runs .ts files directly, so nothing is installed and
// nothing needs the network. There's no proof path yet (D-0024: Dafny, later).
type TypeScript struct{ Image string }

func (TypeScript) Verify(context.Context, string) (*Code, error) { return nil, nil }

func (t TypeScript) Check(ctx context.Context, p *project.Project) (Build, Evidence, error) {
	return runConformance(ctx, t.Image, p, []string{"node --test"}, `cd /src
node --test ; echo "@@invariant test=$?"
node "$DRIVER" ; echo "@@invariant conform=$?"
`)
}

// Python runs a project's tests and its conformance driver in a Python
// container, with the standard library only. There's no proof path yet
// (D-0024: Nagini, after a spike).
type Python struct{ Image string }

func (Python) Verify(context.Context, string) (*Code, error) { return nil, nil }

func (py Python) Check(ctx context.Context, p *project.Project) (Build, Evidence, error) {
	return runConformance(ctx, py.Image, p, []string{"compileall", "unittest"}, `cd /src
python -m compileall -q . > /dev/null ; echo "@@invariant compile=$?"
python -m unittest discover -s . -p "test_*.py" ; echo "@@invariant test=$?"
python "$DRIVER" ; echo "@@invariant conform=$?"
`)
}

// runConformance runs a project's build steps and then its conformance
// driver, in image, on a throwaway copy, with no network and none of the
// host's environment. The driver writes its runs to $INVARIANT_TRACES.
func runConformance(ctx context.Context, image string, p *project.Project, steps []string, script string) (Build, Evidence, error) {
	work, err := os.MkdirTemp("", "invariant-run-")
	if err != nil {
		return Build{}, Evidence{}, err
	}
	defer os.RemoveAll(work)
	src, out := filepath.Join(work, "src"), filepath.Join(work, "out")
	if err := copyTree(p.Dir, src); err != nil {
		return Build{}, Evidence{}, err
	}
	if err := os.MkdirAll(out, 0o777); err != nil {
		return Build{}, Evidence{}, err
	}
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--memory", "2g", "--pids-limit", "512",
		"-e", "HOME=/tmp", "-e", "PYTHONHASHSEED=0", "-e", "DRIVER="+filepath.ToSlash(p.Manifest.Conformance),
		"-e", "INVARIANT_TRACES=/out/traces.json",
		"-e", fmt.Sprintf("INVARIANT_RUNS=%d", conformanceRuns), "-e", fmt.Sprintf("INVARIANT_STEPS=%d", conformanceSteps),
		"-e", fmt.Sprintf("INVARIANT_SEED=%d", conformanceSeed),
		"-v", src+":/src", "-v", out+":/out", "-w", "/src", image, "sh", "-c", script)
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return Build{}, Evidence{}, fmt.Errorf("running the sandbox: %w", err)
		}
	}
	sections, err := splitMarkers(buf.String(), len(steps)+1)
	if err != nil {
		return Build{}, Evidence{}, err
	}
	var b Build
	var output []string
	b.Passed = true
	for i, name := range steps {
		ok := sections[i].code == 0
		b.Steps = append(b.Steps, BuildStep{Name: name, Passed: ok})
		b.Passed = b.Passed && ok
		output = append(output, sections[i].out)
	}
	b.Output = strings.TrimSpace(strings.Join(output, "\n"))
	driver := sections[len(steps)]
	traces, readErr := os.ReadFile(filepath.Join(out, "traces.json"))
	if driver.code != 0 || readErr != nil {
		return b, Evidence{Message: "the conformance driver failed:\n" + lastLines(driver.out, 30)}, nil
	}
	return b, Evidence{Traces: traces}, nil
}
