package plumbing

import (
	"reflect"
	"testing"
	"time"
)

func TestRepositoryAuthorityIsInInvariantsTrustedBase(t *testing.T) {
	// The new check authority requires the same human merge boundary as the
	// gate, while existing Invariant policy remains the compatibility path.
	for _, path := range []string{
		"internal/repository/repository.go", "internal/repository/ownership.go",
		".invariant/repository.json", ".github/workflows/gate.yml", "cmd/invariant/main.go",
		"factory/protocol/core.go", "go.mod", "go.sum", "AGENTS.md", "CLAUDE.md",
		".claude/settings.json", ".codex/config.toml", ".mcp.json",
		"internal/conformance/conformance.go", "internal/decisions/decisions.go",
		"internal/factory/factory.go", "internal/formalize/plan.go", "internal/github/github.go",
		"internal/gobra/gobra.go", "internal/mcp/mcp.go", "internal/plumbing/plan.go",
		"internal/project/project.go", "internal/receipt/receipt.go", "internal/regular/regular.go",
		"internal/scope/scope.go", "internal/setup/setup.go", "internal/synth/synth.go",
		"internal/tla/tla.go", "internal/tlc/tlc.go", "internal/toolchain/toolchain.go",
		"internal/verify/verify.go", "internal/dashboard/act.go", "internal/dashboard/http.go",
	} {
		if !Trusted(path) {
			t.Errorf("%s must require a human merge", path)
		}
	}
	for _, path := range []string{"README.md", "SPEC.md", "docs/factory.md", "internal/dashboard/graph.go", "internal/repositoryish/file.go", ".invariant/repository.json.bak"} {
		if Trusted(path) {
			t.Errorf("%s unexpectedly became trusted", path)
		}
	}
	p := &Plan{Files: []string{"internal/dashboard/web/new.css", "internal/newpkg/new.go"},
		Tests: []Test{{Name: "TestNew", File: "internal/newpkg/new_accept_test.go"}}}
	base := []string{"internal/dashboard/graph.go", "internal/dashboard/old_test.go", "unrelated/x.go"}
	if got := p.GoPackages(base); !reflect.DeepEqual(got, []string{"internal/dashboard", "internal/newpkg"}) {
		t.Errorf("Invariant package ownership changed: %v", got)
	}
	if got := p.RegressionFiles(base, []string{"internal/dashboard/new_test.go", "internal/dashboard/old_test.go", "internal/newpkg/added_test.go", "internal/newpkg/new_accept_test.go", "unrelated/added_test.go"}); !reflect.DeepEqual(got, []string{"internal/dashboard/new_test.go", "internal/newpkg/added_test.go"}) {
		t.Errorf("Invariant regression scope changed: %v", got)
	}
	if SandboxShards != 8 || SandboxTimeout != 12*time.Minute || SandboxShardTimeout != 10*time.Minute || SandboxStartupAllowance != time.Minute || SandboxCleanupAllowance != 15*time.Second {
		t.Error("repository contract changed Invariant's sandbox budgets")
	}
}
