package plumbing

import (
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestRequestRuntimeRetainsTemplateAndDefaultProfile742(t *testing.T) {
	_, _, first := fixture484(t, []repository.Part{part484("go", "api")}, nil)
	_, _, second := fixture484(t, []repository.Part{part484("python", "worker")}, nil)
	defaults := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64"}
	plain := Sandbox{}
	if plain.ForRequest(t.Context()).Repository != nil {
		t.Fatal("modeled and unconfigured requests acquired repository execution")
	}
	bound := plain.ForRequest(WithRepositoryRuntime(t.Context(), first, defaults))
	if bound.Repository.Snapshot != first || bound.Repository.Dependencies.CacheDir != defaults.CacheDir || plain.Repository != nil {
		t.Fatal("request did not bind the snapshot and watcher dependency cache privately")
	}
	template := Sandbox{Repository: &RepositoryChecks{Dependencies: defaults, Adapters: map[string]RepositoryAcceptanceAdapter{"go": GoRepositoryAcceptanceAdapter{}}}}
	for _, snapshot := range []*repository.Snapshot{first, second} {
		ctx := WithRepositoryRuntime(t.Context(), snapshot, DependencyPreparer{})
		runtime := template.ForRequest(ctx)
		if runtime.Repository.Snapshot != snapshot || runtime.Repository.Dependencies.CacheDir != defaults.CacheDir {
			t.Fatal("one request replaced another's authority or dependency cache")
		}
		delete(runtime.Repository.Adapters, "go")
	}
	if template.Repository.Snapshot != nil || template.Repository.Adapters["go"] == nil {
		t.Fatal("binding a request mutated a shared runner")
	}
	if template.ForRequest(WithRepositoryRuntime(t.Context(), nil, defaults)).Repository != nil {
		t.Fatal("an unconfigured request failed to select its built-in runner")
	}
}
