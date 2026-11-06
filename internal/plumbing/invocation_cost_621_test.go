package plumbing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const costDockerSwitch621 = "--invariant-docker-cost-621"

// Time actual compiler invocations at the Docker boundary, including the
// transport compiler called inside Prepare. Forward all three streams intact.
func init() {
	if len(os.Args) < 5 || os.Args[1] != costDockerSwitch621 {
		return
	}
	argv := os.Args[4:]
	cmd := exec.Command(os.Args[2], argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	start := time.Now()
	err := cmd.Run()
	compiler := false
	if i := slices.Index(argv, "invariant-check"); i >= 0 && i+2 < len(argv) && argv[i+1] == "go" {
		compiler = argv[i+2] == "build" || argv[i+2] == "test" && slices.Contains(argv[i+3:], "-c")
	}
	if compiler {
		f, writeErr := os.OpenFile(os.Args[3], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if writeErr == nil {
			writeErr = json.NewEncoder(f).Encode(time.Since(start))
			writeErr = errors.Join(writeErr, f.Close())
		}
		err = errors.Join(err, writeErr)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			os.Exit(exit.ExitCode())
		}
		os.Exit(125)
	}
	os.Exit(0)
}

func costCompiler621(t *testing.T, log string) time.Duration {
	t.Helper()
	data, err := os.ReadFile(log)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	var total time.Duration
	for _, line := range strings.Fields(string(data)) {
		var elapsed time.Duration
		if err := json.Unmarshal([]byte(line), &elapsed); err != nil {
			t.Fatal(err)
		}
		total += elapsed
	}
	return total
}

// The shared arm reproduces the former one-environment selector loop. Both
// arms use the same prepared image, pinned sources, discovery and collectors;
// there is deliberately no production switch back to shared acceptance.
func TestInvocationOfflineCosts621(t *testing.T) {
	if os.Getenv("INVARIANT_ACCEPTANCE_ISOLATION_REAL") != "1" {
		t.Log("enable INVARIANT_ACCEPTANCE_ISOLATION_REAL with both local pinned images and platform to report shared/fresh costs")
		return
	}
	platform := os.Getenv("INVARIANT_ACCEPTANCE_ISOLATION_PLATFORM")
	if !dependencyPlatform.MatchString(platform) {
		t.Fatal("cost comparison needs an explicit Linux platform")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	docker, err = filepath.Abs(docker)
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "compiler.jsonl")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nexec " + quote(self) + " " + quote(costDockerSwitch621) + " " + quote(docker) + " " + quote(log) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, framework := range []string{"go", "pytest"} {
		t.Run(framework, func(t *testing.T) {
			variable := "INVARIANT_ACCEPTANCE_ISOLATION_GO_IMAGE"
			source := "package fixture\nimport \"testing\"\nfunc TestFirst(t *testing.T){if Value!=7{t.Fatal(Value)}}\nfunc TestLater(t *testing.T){if Value!=7{t.Fatal(Value)}}\n"
			if framework == "pytest" {
				variable = "INVARIANT_ACCEPTANCE_ISOLATION_PYTEST_IMAGE"
				source = "import product\ndef test_first(): assert product.VALUE == 7\ndef test_later(): assert product.VALUE == 7\n"
			}
			image := os.Getenv(variable)
			if !dependencyBase.MatchString(image) {
				t.Fatal("cost comparison needs an already-local digest-pinned image in ", variable)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
			defer cancel()
			if _, err := inspectDependencyImage(ctx, image, platform); err != nil {
				t.Fatal("local image prerequisite failed; nothing is downloaded: ", err)
			}
			args := []string{"run", "--rm", "--pull", "never", "--network", "none", "--platform", platform}
			if framework == "go" {
				args = append(args, "--entrypoint", "go", image, "version")
			} else {
				args = append(args, "--entrypoint", "python3", image, "-I", "-c", "import sys,pytest; assert sys.version_info[:2]==(3,13); assert pytest.__version__=='8.3.5'; print(sys.version, pytest.__version__)")
			}
			out, err := dependencyDocker(ctx, args...)
			if err != nil || framework == "go" && !strings.Contains(string(out), "go1.27.") {
				t.Fatalf("pinned runtime prerequisite failed: %v %s", err, out)
			}
			s, candidate, plan, runner := realFixture621(t, framework, image, platform, source)
			parts := s.Repository.Snapshot.Config().Parts
			part := parts[0]
			selections, err := s.selectRepositoryAcceptance(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			session, err := s.prepareRepositoryGo(ctx, s.Repository.Dependencies, parts)
			if err != nil {
				t.Fatal(err)
			}
			prepared := session.parts[part.Name]
			t.Logf("framework=%s image=%s prepared=%s platform=%s runtime=%q", framework, image, prepared.artifact.Image, platform, strings.TrimSpace(string(out)))
			for _, base := range []bool{true, false} {
				input := t.TempDir()
				if base {
					err = stageRepositoryBase(ctx, s.Original, s.Repository.Snapshot, input)
				} else {
					body := "VALUE = 7\n"
					if framework == "go" {
						body = "package fixture\nconst Value=7\n"
					}
					write468(t, candidate, plan.Files[0], body)
					err = CopyCheckout(ctx, candidate, input)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := stageRepositoryAcceptance(ctx, input, plan); err != nil {
					t.Fatal(err)
				}
				if err := s.validateRepositoryInputs(ctx, input, parts); err != nil {
					t.Fatal(err)
				}
				for repeat := range 3 {
					modes := []bool{false, true}
					if repeat%2 == 1 {
						slices.Reverse(modes)
					}
					for _, fresh := range modes {
						func() {
							// Every comparison has one unchanged whole-run and test
							// deadline, even though the enclosing test runs repeats.
							whole, stop := context.WithTimeout(ctx, SandboxTimeout)
							defer stop()
							run, done := context.WithTimeout(whole, SandboxShardTimeout)
							defer done()
							var env RepositoryCheckEnvironment
							var stage string
							closeWork := func() {
								cleanup, finish := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
								defer finish()
								var closeErr error
								if env != nil {
									closeErr = env.Close(cleanup)
									env = nil
								}
								if stage != "" {
									closeErr = errors.Join(closeErr, removeRepositoryStage(cleanup, stage))
									stage = ""
								}
								if closeErr != nil {
									t.Fatal(closeErr)
								}
							}
							defer closeWork()
							for i, test := range plan.Tests {
								start, before := time.Now(), costCompiler621(t, log)
								var staging, startup time.Duration
								if env == nil {
									at := time.Now()
									stage, err = stageDependencySource(run, DependencyRequest{SourceDir: input, Directory: "."}, false)
									staging = time.Since(at)
									if err != nil {
										t.Fatal(err)
									}
									q := cloneDependencyRequest(prepared.request)
									q.Image, q.SourceDir = prepared.artifact.Image, filepath.Join(stage, "invariant", "work")
									q.Network, q.Pull, q.Argv = false, false, nil
									q.Env = dependencyEnv(part.Language, false)
									q.Token, err = dependencyToken()
									if err != nil {
										t.Fatal(err)
									}
									at = time.Now()
									env, err = runner.OpenChecks(run, q)
									startup = time.Since(at)
									if err != nil {
										t.Fatal(err)
									}
								}
								in := repositoryInvocation(plan, test, selections[i])
								if adapter, ok := s.repositoryAdapter(framework).(RepositoryAcceptanceDiscoverer); ok {
									discovery, err := adapter.Discover(run, part, in, env)
									if err != nil {
										t.Fatal(err)
									}
									in.Binding = discovery.Binding
								}
								transport := env.(RepositoryProcessTransport)
								var evidence RepositoryAcceptanceEvidence
								if framework == "go" {
									evidence, out, err = runRepositoryGoAcceptance(run, part, in, transport)
								} else {
									evidence, out, err = runRepositoryPytestAcceptance(run, part, in, transport)
								}
								result, evidenceErr := repositoryEvidenceMode(in.Binding, evidence, unstartedAcceptance(plan).TestResults[i], framework == "go")
								want := "passed"
								if base {
									want = "failed"
								}
								if err != nil || evidenceErr != nil || result.Kind != "test" || result.Status != want {
									t.Fatalf("cost sample lacks authentic evidence: %v %v %+v\n%s", err, evidenceErr, result, out)
								}
								if fresh || i+1 == len(plan.Tests) {
									closeWork()
								}
								total := time.Since(start)
								compilation := costCompiler621(t, log) - before
								if framework == "go" && compilation <= 0 {
									t.Fatal("cost sample missed actual compilation")
								}
								t.Logf("framework=%s base=%v repeat=%d fresh=%v invocation=%d staging=%s startup=%s compilation=%s total=%s", framework, base, repeat+1, fresh, i+1, staging, startup, compilation, total)
							}
						}()
					}
				}
			}
		})
	}
}
