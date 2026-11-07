package factory

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

func TestConfiguredFactorySetupFailures742(t *testing.T) {
	goCache := t.TempDir()
	for _, mode := range []string{"missing", "branch only", "uncommitted only", "malformed", "missing gate", "obsolete gate", "branch only gate", "uncommitted only gate", "workflow defaults", "workflow env", "job defaults", "job env", "job container"} {
		t.Run(mode, func(t *testing.T) {
			x := new742(t, "pytest", false, goCache)
			configured := read742(t, x.clone.Dir, repository.Path)
			workflow := read742(t, x.clone.Dir, setup.WorkflowPath)
			missing := mode == "missing" || mode == "branch only" || mode == "uncommitted only"
			switch {
			case missing:
				if err := os.Remove(filepath.Join(x.clone.Dir, repository.Path)); err != nil {
					t.Fatal(err)
				}
			case mode == "malformed":
				write742(t, x.clone.Dir, repository.Path, `{"version":1,"parts":"not an array"}`)
			case mode == "missing gate" || mode == "branch only gate" || mode == "uncommitted only gate":
				if err := os.Remove(filepath.Join(x.clone.Dir, setup.WorkflowPath)); err != nil {
					t.Fatal(err)
				}
			case mode == "obsolete gate":
				write742(t, x.clone.Dir, setup.WorkflowPath, "name: old\njobs:\n  gate:\n    name: invariant/gate\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo obsolete\n")
			case mode == "workflow defaults":
				write742(t, x.clone.Dir, setup.WorkflowPath, workflow+"\ndefaults:\n  run:\n    working-directory: worker\n")
			case mode == "workflow env":
				write742(t, x.clone.Dir, setup.WorkflowPath, workflow+"\nenv:\n  BASH_ENV: worker/bootstrap.sh\n")
			default:
				key := map[string]string{
					"job defaults":  "    defaults:\n      run:\n        working-directory: worker\n",
					"job env":       "    env:\n      BASH_ENV: worker/bootstrap.sh\n",
					"job container": "    container: fixture/image\n",
				}[mode]
				if key == "" || !strings.Contains(workflow, "    name: invariant/gate") {
					t.Fatal("invalid gate refusal fixture")
				}
				write742(t, x.clone.Dir, setup.WorkflowPath, strings.Replace(workflow, "    name: invariant/gate", key+"    name: invariant/gate", 1))
			}
			x.commitBase()
			if mode == "branch only" || mode == "branch only gate" {
				wt, err := x.clone.Worktree(t.Context(), "invariant/issue-1-candidate-config", "origin/main")
				if err != nil {
					t.Fatal(err)
				}
				if mode == "branch only" {
					write742(t, wt, repository.Path, configured)
				} else {
					write742(t, wt, setup.WorkflowPath, workflow)
				}
				if _, err = x.clone.Commit(t.Context(), wt, ".", "candidate-only contract"); err != nil {
					t.Fatal(err)
				}
				if err = x.clone.Push(t.Context(), wt, "invariant/issue-1-candidate-config"); err != nil {
					t.Fatal(err)
				}
				if err = x.clone.RemoveWorktree(t.Context(), wt); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "uncommitted only" {
				write742(t, x.clone.Dir, repository.Path, configured)
			}
			if mode == "uncommitted only gate" {
				write742(t, x.clone.Dir, setup.WorkflowPath, workflow)
			}
			heads := git(t, x.r.origin, "for-each-ref", "--format=%(refname)", "refs/heads/")
			x.r.gh.open(1, "gitdek", "Set up plumbing", "Kind: plumbing\n\n/invariant solve")
			for attempt := 0; attempt < 2; attempt++ {
				var reply int64
				if attempt == 1 {
					reply = x.r.gh.say(1, "gitdek", "Try the same request again.\n\n/invariant revise").ID
				}
				_ = x.r.f.Poll(t.Context())
				post := x.r.gh.last(1)
				body := strings.ToLower(strings.Split(post.Comment.Body, "<!-- invariant:")[0])
				if missing && post.Marker.Kind != KindUnsupported {
					t.Errorf("no committed config should remain unsupported: %s", post.Comment.Body)
				}
				if post.Marker.Proposal != nil || !slices.Contains(x.r.gh.labelsOf(1), LabelHumanReview) {
					t.Fatal("setup failure became a proposal or lost human label")
				}
				if attempt == 1 && !slices.Contains(post.Marker.ReplyTo, reply) {
					t.Fatal("setup response did not answer revision")
				}
				if strings.Contains(body, "external issue intake is still disabled") || strings.Contains(body, "only on invariant's own") {
					t.Fatal("obsolete blanket refusal hides available setup")
				}
				for _, want := range []string{"base", "commit"} {
					if !strings.Contains(body, want) {
						t.Errorf("setup explanation omits %q: %s", want, body)
					}
				}
				switch {
				case missing:
					for _, want := range []string{repository.Path, "invariant init", "gate", "review"} {
						if !strings.Contains(body, want) {
							t.Errorf("missing-config response omits %q", want)
						}
					}
				case mode == "malformed":
					if !strings.Contains(body, repository.Path) || !strings.Contains(body, "invalid") && !strings.Contains(body, "malformed") {
						t.Errorf("malformed contract not diagnosed: %s", body)
					}
				default:
					for _, want := range []string{"invariant/gate", "check-repository", "ratification", "scope"} {
						if !strings.Contains(body, want) {
							t.Errorf("gate setup failure omits %q: %s", want, body)
						}
					}
				}
				x.restart()
				x.r.poll()
				x.r.poll()
				if len(x.r.gh.posts(1)) != attempt+1 {
					t.Fatal("setup failure repeated without a command")
				}
			}
			if len(x.planner.requests) != 0 || x.build.calls != 0 || len(x.r.gh.prs) != 0 || x.runner.opened != 0 || len(x.runner.installs) != 0 {
				t.Fatal("setup failure ran agents, checks or publication")
			}
			if got := git(t, x.r.origin, "for-each-ref", "refs/invariant/runs/"); got != "" {
				t.Fatal("setup failure recorded an agent run")
			}
			if got := git(t, x.r.origin, "for-each-ref", "--format=%(refname)", "refs/heads/"); got != heads {
				t.Fatalf("setup failure created branch: %s", got)
			}
		})
	}
}

// Intake must still reach the existing locked-install admission before a
// network-capable installer sees requirements pip could interpret differently.
func TestConfiguredFactoryPythonLockAdmission742(t *testing.T) {
	goCache := t.TempDir()
	for _, mode := range []string{"continued comment", "bare carriage return", "encoding declaration"} {
		t.Run(mode, func(t *testing.T) {
			x := new742(t, "pytest", false, goCache)
			lock := read742(t, x.clone.Dir, "worker/requirements.lock")
			switch mode {
			case "continued comment":
				lock += "# note \\\n--no-binary=:all:\n"
			case "bare carriage return":
				lock = strings.TrimSuffix(lock, "\n") + " #\r--no-binary=:all:\n"
			case "encoding declaration":
				lock = "# -*- coding: utf-7 -*-\n" + lock
			}
			write742(t, x.clone.Dir, "worker/requirements.lock", lock)
			x.commitBase()
			x.bindDraft()
			x.r.gh.open(1, "gitdek", "Return seven", "Kind: plumbing\n\n/invariant solve")
			x.r.poll()
			post := x.r.gh.last(1)
			visible := strings.ToLower(strings.Split(post.Comment.Body, "<!-- invariant:")[0])
			if post.Marker.Proposal != nil || !strings.Contains(visible, "requirements.lock") || !slices.Contains(x.r.gh.labelsOf(1), LabelHumanReview) {
				t.Fatalf("unsafe committed lock did not produce a specific human handoff: %s", post.Comment.Body)
			}
			if len(x.runner.installs) != 0 || x.runner.opened != 0 || x.build.calls != 0 || len(x.r.gh.prs) != 0 {
				t.Fatal("unsafe requirements reached dependency installation, checks or publication")
			}
			x.restart()
			x.r.poll()
			x.r.poll()
			if len(x.r.gh.posts(1)) != 1 || len(x.runner.installs) != 0 {
				t.Fatal("unsafe requirements were retried without a writer command")
			}
		})
	}
}
