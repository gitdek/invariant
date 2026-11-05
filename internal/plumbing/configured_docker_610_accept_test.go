package plumbing

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/toolchain"
)

// This opt-in uses only the already-local pinned base, sealing an empty graph
// offline. It never pulls an image or downloads dependencies.
type offlineRunner610 struct {
	*DockerDependencyRunner
	t        *testing.T
	requests []DependencyRequest
}

func (r *offlineRunner610) Install(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
	if len(q.Argv) != 0 || q.Network {
		return DependencyArtifact{}, errors.New("real fixture must have an empty offline install")
	}
	q.Pull = false
	a, err := r.DockerDependencyRunner.Install(ctx, q)
	if a.Image != "" {
		r.t.Cleanup(func() {
			ctx, done := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
			defer done()
			if _, e := dependencyDocker(ctx, "image", "rm", a.Image); e != nil {
				r.t.Error(e)
			}
		})
	}
	return a, err
}
func (r *offlineRunner610) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	r.requests = append(r.requests, q)
	if q.Network || q.Pull {
		return nil, errors.New("real candidate execution is not offline")
	}
	return r.DockerDependencyRunner.OpenChecks(ctx, q)
}

func TestConfiguredGoOfflinePinnedImage610(t *testing.T) {
	// This mandatory public-path control makes the test useful, and failing on
	// the old code, even when Docker is unavailable and the opt-in is unset.
	eligible610(t, configured610(t, t.TempDir(), []repositoryCase582{repositoryControl582()}, false))
	if os.Getenv("INVARIANT_CONFIGURED_GO_REAL") != "1" {
		t.Log("set INVARIANT_CONFIGURED_GO_REAL=1 and INVARIANT_GO_ACCEPTANCE_PLATFORM for the offline pinned-image cases")
		return
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("enabled real test requires Docker: ", err)
	}
	platform := os.Getenv("INVARIANT_GO_ACCEPTANCE_PLATFORM")
	if !dependencyPlatform.MatchString(platform) {
		t.Fatal("set INVARIANT_GO_ACCEPTANCE_PLATFORM to the pinned image's explicit Linux platform")
	}
	ctx, done := context.WithTimeout(t.Context(), 10*time.Minute)
	defer done()
	if _, err := inspectDependencyImage(ctx, toolchain.GoImage, platform); err != nil {
		t.Fatal("enabled real test needs a running daemon and the pinned Go base already local; nothing will be pulled: ", err)
	}
	for _, mode := range []string{"production correction", "skip", "abrupt exit"} {
		t.Run(mode, func(t *testing.T) {
			c := repositoryControl582()
			if mode == "skip" {
				c.source = repositorySource582(`t.Skip("not evidence")`, "")
			}
			if mode == "abrupt exit" {
				c.source = repositorySource582(`os.Exit(19)`, "")
			}
			f := newRepositoryFixture582(t, t.TempDir(), []repositoryCase582{c}, false, nil, func(config *repository.Config) {
				config.Parts[0].Image = toolchain.GoImage
				config.Parts[0].Commands["install"] = repository.Command{NotApplicable: "Dependency-free offline acceptance fixture."}
			})
			runner := &offlineRunner610{DockerDependencyRunner: &DockerDependencyRunner{}, t: t}
			f.sandbox.Repository.Adapters = nil
			f.sandbox.Repository.Dependencies = DependencyPreparer{CacheDir: t.TempDir(), Platform: platform, Runner: runner}
			defer func() {
				cleanup, done := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
				defer done()
				for _, q := range runner.requests {
					if _, err := dependencyDocker(cleanup, "container", "inspect", q.Token); err == nil {
						t.Error("configured check leaked its container")
						_, _ = dependencyDocker(cleanup, "rm", "-f", q.Token)
					}
					if _, err := os.Stat(q.SourceDir); !errors.Is(err, os.ErrNotExist) {
						t.Error("configured check leaked staging")
					}
				}
			}()
			base, err := Check(ctx, n503Draft(t, f.plan), f.root, f.sandbox)
			if base == nil {
				t.Fatalf("lost real base result: %v", err)
			}
			if (base.Problem == "") != (mode == "production correction") {
				t.Fatalf("wrong real base verdict: %v %s", err, base.Feedback())
			}
			f.candidateValue(7)
			r, err := f.sandbox.Run(ctx, f.candidate, f.plan)
			if r.Passed() != (mode == "production correction") {
				t.Fatalf("wrong real candidate verdict: %v %s", err, r.Summary())
			}
			if mode == "production correction" && err != nil {
				t.Fatal(err)
			}
		})
	}
}
