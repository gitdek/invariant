package plumbing

import (
	"context"
	_ "embed"

	"github.com/gitdek/invariant/internal/repository"
)

// VitestRepositoryAcceptanceAdapter selects one file and literal full title.
type VitestRepositoryAcceptanceAdapter struct{}

func (VitestRepositoryAcceptanceAdapter) Select(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
	return selectNodeAcceptance(ctx, part, test, "vitest")
}

//go:embed vitest_runner.mjs
var vitestRunner []byte
