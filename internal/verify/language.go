package verify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/gitdek/invariant/internal/gobra"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Language is the part of the gate that depends on the implementation's
// language: checking the code against its contracts, then building and
// testing it. The design checks don't depend on it. Go, verified with Gobra,
// is the only language so far; D-0023 covers adding others.
type Language interface {
	Verify(ctx context.Context, dir string) (Code, error)
	Build(ctx context.Context, dir string) Build
}

func languageFor(p *project.Project, tc toolchain.Toolchain) (Language, error) {
	switch p.Manifest.Language {
	case "", "go":
		return Go{GobraImage: tc.GobraImage}, nil
	default:
		return nil, fmt.Errorf("language %q isn't supported yet (see decisions/D-0023-more-languages.md)", p.Manifest.Language)
	}
}

// Go verifies a package with Gobra, with overflow checks on, then runs go
// vet and go test.
type Go struct {
	GobraImage string
}

func (g Go) Verify(ctx context.Context, dir string) (Code, error) {
	res, err := gobra.Run(ctx, g.GobraImage, dir, true)
	if err != nil {
		return Code{}, err
	}
	return Code{
		Verifier: "Gobra", Passed: res.Passed, Functions: res.Functions, Contracts: res.Contracts,
		Overflow: res.Overflow, Errors: res.Errors,
	}, nil
}

func (Go) Build(ctx context.Context, dir string) Build {
	var b Build
	var out strings.Builder
	for _, args := range [][]string{{"vet", "."}, {"test", "-count=1", "."}} {
		var buf bytes.Buffer
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &buf, &buf
		err := cmd.Run()
		b.Steps = append(b.Steps, BuildStep{Name: "go " + args[0], Passed: err == nil})
		out.WriteString(buf.String())
	}
	b.Passed = b.Steps[0].Passed && b.Steps[1].Passed
	b.Output = strings.TrimSpace(out.String())
	return b
}

func goVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "go", "env", "GOVERSION").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
