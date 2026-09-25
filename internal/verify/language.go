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
// language. Verify checks the code against its contracts. Check builds and
// tests it, and explores its state space for the agreement check, all in a
// sandbox. The design checks don't depend on it. Go, verified with Gobra, is
// the only language so far; D-0023 and D-0024 cover the others.
type Language interface {
	Verify(ctx context.Context, pkg string) (Code, error)
	Check(ctx context.Context, project, pkg string) (Build, Exploration, error)
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
	default:
		return nil, fmt.Errorf("language %q isn't supported yet (see decisions/D-0024-checking-typescript-and-python.md)", p.Manifest.Language)
	}
}

// Go verifies a package with Gobra, with overflow checks on. It builds,
// tests and explores the package's module in a container from GoImage, on a
// throwaway copy, with no network and none of the host's environment.
type Go struct {
	GobraImage string
	GoImage    string
}

func (g Go) Verify(ctx context.Context, pkg string) (Code, error) {
	res, err := gobra.Run(ctx, g.GobraImage, pkg, true)
	if err != nil {
		return Code{}, err
	}
	return Code{
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
	marker   = regexp.MustCompile(`(?m)^@@invariant (vet|test|agree)=(\d+)$`)
	explored = regexp.MustCompile(`invariant-agreement states=(\d+) depth=(\d+)`)
)

func (g Go) Check(ctx context.Context, projectDir, pkg string) (Build, Exploration, error) {
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

// parseSandbox splits the container's output at its markers.
func parseSandbox(out string) (Build, Exploration, error) {
	marks := marker.FindAllStringSubmatchIndex(out, -1)
	if len(marks) != 3 {
		return Build{}, Exploration{}, fmt.Errorf("the Go sandbox didn't finish:\n%s", lastLines(out, 20))
	}
	section := func(i int) (string, int) {
		start := 0
		if i > 0 {
			start = marks[i-1][1]
		}
		code, _ := strconv.Atoi(out[marks[i][4]:marks[i][5]])
		return strings.TrimSpace(out[start:marks[i][0]]), code
	}
	vetOut, vet := section(0)
	testOut, test := section(1)
	agreeOut, agreed := section(2)
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
