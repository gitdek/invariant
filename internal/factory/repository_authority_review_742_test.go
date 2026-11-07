package factory

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/scope"
	"github.com/gitdek/invariant/internal/setup"
)

const competingGate742 = "name: competing\non: [push, pull_request]\njobs:\n  success:\n    name: invariant/gate\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo success\n"
const competingGatePath742 = ".github/workflows/competing.yml"

// The first scope and authority check passes. Change base only when the
// final merge refresh reaches scope, so its authority check must stop it.
type finalScope742 struct {
	Repo
	move  func()
	calls int
}

func (r *finalScope742) Scope(ctx context.Context, base, head string, issue int) (scope.Result, error) {
	s, err := r.Repo.Scope(ctx, base, head, issue)
	if err == nil {
		r.calls++
		if r.calls == 2 {
			r.move()
		}
	}
	return s, err
}

func TestConfiguredCompetingGateAuthority742(t *testing.T) {
	goCache := t.TempDir()
	for _, stage := range []string{"ratification", "build", "publication"} {
		t.Run(stage, func(t *testing.T) {
			x := new742(t, "pytest", false, goCache)
			p := x.start()
			move := func() {
				x.changeBranch("main", func(root string) {
					write742(t, root, competingGatePath742, competingGate742)
				})
			}
			switch stage {
			case "ratification":
				move()
			case "build":
				x.r.f.Repo = &afterRatification742{Repo: x.clone, move: move}
			case "publication":
				x.beforeReview = move
			}
			x.ratify(p)
			visible := strings.Split(x.r.gh.last(1).Comment.Body, "<!-- invariant:")[0]
			if !strings.Contains(visible, "invariant/gate") || !strings.Contains(visible, "changed") || !slices.Contains(x.r.gh.labelsOf(1), LabelHumanReview) {
				t.Fatalf("competing gate did not produce an authority hold: %s", visible)
			}
			if len(x.r.gh.prs) != 0 || len(x.hub.calls) != 0 || len(x.hub.mergeHeads) != 0 {
				t.Fatal("competing gate allowed publication")
			}
			if stage == "publication" {
				if x.build.calls != 1 || !x.build.result.Built() {
					t.Fatal("publication refusal must preserve the successful build")
				}
			} else if x.build.calls != 0 {
				t.Fatal("competing gate allowed a build")
			}
		})
	}
}

func TestConfiguredMergeAuthorityRetryGuidance742(t *testing.T) {
	goCache := t.TempDir()
	for _, stage := range []string{"merge", "final refresh"} {
		for _, change := range []string{"contract removed", "gate removed", "gate pin changed", "competing gate"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				x := new742(t, "pytest", false, goCache)
				p := x.start()
				x.ratify(p)
				pr := x.published(false)
				head := x.r.gh.prs[pr.Marker.PR].Head.SHA
				file, reason := setup.WorkflowPath, "gate"
				if change == "contract removed" {
					file, reason = repository.Path, repository.Path
				} else if change == "competing gate" {
					file = competingGatePath742
				}
				move := func() {
					x.changeBranch("main", func(root string) {
						switch change {
						case "competing gate":
							write742(t, root, file, competingGate742)
						case "gate pin changed":
							w, err := setup.Workflow(strings.Repeat("b", 40))
							if err != nil {
								t.Fatal(err)
							}
							write742(t, root, file, w)
						default:
							if err := os.Remove(filepath.Join(root, file)); err != nil {
								t.Fatal(err)
							}
						}
					})
				}
				probe := &publication742{Repo: x.clone}
				x.r.f.Repo = probe
				var refresh *finalScope742
				if stage == "merge" {
					move()
				} else {
					refresh = &finalScope742{Repo: probe, move: move}
					x.r.f.Repo = refresh
				}
				if change == "competing gate" {
					// The real gate failed; a newer same-named check merely
					// succeeded. Authority must prevent merging on that result.
					x.r.gh.ci(pr.Marker.PR, "failure")
				}
				x.r.gh.ci(pr.Marker.PR, "success")
				x.r.poll()
				if refresh != nil && refresh.calls != 2 {
					t.Fatal("fixture did not reach the final merge refresh")
				}
				hold := x.r.expect(1, KindFailed, LabelHumanReview)
				if hold.Marker.Failure != FailUnmergeable {
					t.Fatalf("authority refusal used the wrong recovery state: %+v", hold.Marker)
				}
				visible := strings.ToLower(strings.Split(hold.Comment.Body, "<!-- invariant:")[0])
				for _, want := range []string{reason, "original contract", "capable gate", "base", "commit", "restoring authority alone", "write access", "/invariant retry"} {
					if !strings.Contains(visible, want) {
						t.Errorf("merge hold omits %q: %s", want, visible)
					}
				}
				posts, readies := len(x.r.gh.posts(1)), len(x.hub.calls)
				stillHeld := func() {
					t.Helper()
					if len(x.hub.mergeHeads) != 0 || len(x.r.gh.posts(1)) != posts || len(x.hub.calls) != readies || probe.loads != 0 || probe.pushes != 0 || x.build.calls != 1 {
						t.Fatal("merge hold repeated work or resumed without a writer")
					}
				}
				stillHeld()
				x.restart()
				x.r.poll()
				x.r.poll()
				stillHeld()
				x.r.gh.say(1, "mallory", "/invariant retry")
				x.r.poll()
				stillHeld()
				x.changeBranch("main", func(root string) {
					if change == "competing gate" {
						if err := os.Remove(filepath.Join(root, file)); err != nil {
							t.Fatal(err)
						}
						return
					}
					b, err := x.clone.Show(t.Context(), x.plan.Execution.Base, file)
					if err != nil {
						t.Fatal(err)
					}
					write742(t, root, file, string(b))
				})
				x.restart()
				x.r.poll()
				stillHeld()
				x.r.gh.ci(pr.Marker.PR, "success")
				x.r.gh.say(1, "gitdek", "/invariant retry")
				x.r.poll()
				x.r.poll()
				x.r.expect(1, KindMerged, LabelMerged)
				if !slices.Equal(x.hub.mergeHeads, []string{head}) || x.build.calls != 1 || probe.loads != 0 || probe.pushes != 0 {
					t.Fatal("writer retry must merge only the checked head without reloading or rebuilding")
				}
			})
		}
	}
}
