package plumbing

import (
	"context"
	"maps"

	"github.com/gitdek/invariant/internal/repository"
)

type repositorySnapshotKey struct{}

type repositoryRuntime struct {
	snapshot     *repository.Snapshot
	dependencies DependencyPreparer
}

// WithRepositoryRuntime carries one request's committed authority through
// runner wrappers. Shared formalizers and builders remain configuration only.
func WithRepositoryRuntime(ctx context.Context, snapshot *repository.Snapshot, dependencies DependencyPreparer) context.Context {
	return context.WithValue(ctx, repositorySnapshotKey{}, repositoryRuntime{snapshot, dependencies})
}

// ForRequest binds the retained snapshot without changing the runtime template,
// including its dependency cache, runner and acceptance adapters.
func (s Sandbox) ForRequest(ctx context.Context) Sandbox {
	request, supplied := ctx.Value(repositorySnapshotKey{}).(repositoryRuntime)
	if !supplied {
		return s
	}
	if request.snapshot == nil {
		s.Repository = nil
		return s
	}
	runtime := RepositoryChecks{Dependencies: request.dependencies}
	if s.Repository != nil {
		runtime = *s.Repository
		runtime.Adapters = maps.Clone(runtime.Adapters)
	}
	runtime.Snapshot = request.snapshot
	s.Repository = &runtime
	return s
}
